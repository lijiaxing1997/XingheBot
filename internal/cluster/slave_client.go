package cluster

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
)

type SlaveClientOptions struct {
	MasterURL          string
	Secret             []byte
	SlaveID            string
	Name               string
	Version            string
	Capabilities       []string
	Meta               map[string]any
	Files              FilesConfig
	HeartbeatInterval  time.Duration
	MaxMessageBytes    int64
	InsecureSkipVerify bool
	Runner             AgentRunner
	MaxInflightRuns    int
	StopRequested      func() bool
	Logf               func(format string, args ...any)
}

type SlaveClient struct {
	masterURL    string
	secret       []byte
	slaveID      string
	name         string
	version      string
	capabilities []string
	meta         map[string]any
	files        FilesConfig
	masterID     string

	heartbeatInterval time.Duration
	maxMessageBytes   int64
	tlsConfig         *tls.Config

	runner   AgentRunner
	inflight chan struct{}
	wg       sync.WaitGroup

	fileMu             sync.Mutex
	pendingFileReplies map[string]chan fileOfferReply
	pendingFileAcks    map[string]chan FileAckPayload
	incomingTransfers  map[string]*incomingFileTransfer

	stopRequested func() bool
	draining      atomic.Bool
	activeRuns    atomic.Int64

	logf func(format string, args ...any)
}

var ErrStopRequested = errors.New("stop requested")

func NewSlaveClient(opts SlaveClientOptions) (*SlaveClient, error) {
	url := strings.TrimSpace(opts.MasterURL)
	if url == "" {
		return nil, errors.New("master url is required")
	}
	if len(opts.Secret) == 0 {
		return nil, errors.New("cluster secret is required")
	}
	slaveID := strings.TrimSpace(opts.SlaveID)
	if slaveID == "" {
		slaveID = NewID("slave")
	}
	hb := opts.HeartbeatInterval
	if hb <= 0 {
		hb = 5 * time.Second
	}
	maxMsg := opts.MaxMessageBytes
	if maxMsg <= 0 {
		maxMsg = 4 << 20
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	var tlsCfg *tls.Config
	if strings.HasPrefix(strings.ToLower(url), "wss://") {
		tlsCfg = &tls.Config{InsecureSkipVerify: opts.InsecureSkipVerify} //nolint:gosec
	}

	maxInflight := opts.MaxInflightRuns
	if maxInflight <= 0 {
		maxInflight = 1
	}

	filesCfg := normalizeFilesConfig(opts.Files)
	if err := ensureFileRootDirs(filesCfg.RootDir); err != nil {
		return nil, err
	}

	return &SlaveClient{
		masterURL:          url,
		secret:             opts.Secret,
		slaveID:            slaveID,
		name:               strings.TrimSpace(opts.Name),
		version:            strings.TrimSpace(opts.Version),
		capabilities:       opts.Capabilities,
		meta:               opts.Meta,
		files:              filesCfg,
		heartbeatInterval:  hb,
		maxMessageBytes:    maxMsg,
		tlsConfig:          tlsCfg,
		runner:             opts.Runner,
		inflight:           make(chan struct{}, maxInflight),
		pendingFileReplies: make(map[string]chan fileOfferReply),
		pendingFileAcks:    make(map[string]chan FileAckPayload),
		incomingTransfers:  make(map[string]*incomingFileTransfer),
		stopRequested:      opts.StopRequested,
		logf:               logf,
	}, nil
}

func (c *SlaveClient) SlaveID() string {
	if c == nil {
		return ""
	}
	return c.slaveID
}

func (c *SlaveClient) Run(ctx context.Context) error {
	if c == nil {
		return errors.New("slave client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.stopRequested != nil && c.stopRequested() {
			c.draining.Store(true)
			if c.activeRuns.Load() == 0 {
				return ErrStopRequested
			}
		}
		err := c.runOnce(ctx)
		if err == nil {
			continue
		}
		if errors.Is(err, ErrStopRequested) {
			return err
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		c.logf("ws: disconnected slave_id=%s err=%v", c.slaveID, err)

		retry := c.heartbeatInterval
		if retry <= 0 {
			retry = 5 * time.Second
		}
		jitter := time.Duration(rand.IntN(250)) * time.Millisecond
		sleep := retry + jitter
		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *SlaveClient) runOnce(ctx context.Context) error {
	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var dialOpts websocket.DialOptions
	if c.tlsConfig != nil {
		dialOpts.HTTPClient = &http.Client{
			Transport: &http.Transport{
				Proxy:           http.ProxyFromEnvironment,
				TLSClientConfig: c.tlsConfig,
			},
		}
	}

	c.logf("ws: connecting slave_id=%s master=%s", c.slaveID, c.masterURL)
	conn, _, err := websocket.Dial(dialCtx, c.masterURL, &dialOpts)
	if err != nil {
		return err
	}
	conn.SetReadLimit(c.maxMessageBytes)

	session := &SlaveSession{conn: conn}
	defer session.Close(websocket.StatusNormalClosure, "bye")

	if err := c.sendRegister(connCtx, session); err != nil {
		return err
	}
	ack, err := c.waitRegisterAck(connCtx, conn)
	if err != nil {
		return err
	}
	if !ack.Accepted {
		return fmt.Errorf("register rejected: %s", strings.TrimSpace(ack.Reason))
	}
	c.masterID = strings.TrimSpace(ack.ServerInstanceID)

	hb := c.heartbeatInterval
	if ack.HeartbeatIntervalMillis > 0 {
		hb = time.Duration(ack.HeartbeatIntervalMillis) * time.Millisecond
	}
	c.heartbeatInterval = hb
	ticker := time.NewTicker(hb)
	defer ticker.Stop()
	stopTicker := time.NewTicker(300 * time.Millisecond)
	defer stopTicker.Stop()

	readErr := make(chan error, 1)
	go func() {
		for {
			mt, data, err := conn.Read(connCtx)
			if err != nil {
				readErr <- err
				return
			}
			switch mt {
			case websocket.MessageText:
				env, err := UnmarshalEnvelope(data)
				if err != nil {
					continue
				}
				switch env.Type {
				case MsgTypeHeartbeatAck:
					// ignore
				case MsgTypeAgentRun:
					var req AgentRunPayload
					if len(env.Payload) > 0 {
						if err := json.Unmarshal(env.Payload, &req); err != nil {
							continue
						}
					}
					c.dispatchAgentRun(connCtx, session, env.ID, req)
				case MsgTypeFileOffer, MsgTypeFileAccept, MsgTypeFileReject, MsgTypeFileAck, MsgTypeFileComplete, MsgTypeFilePull, MsgTypeFileCancel:
					c.handleFileEnvelope(connCtx, session, env)
				default:
					// ignore
				}
			case websocket.MessageBinary:
				c.handleFileBinary(session, data)
			default:
				continue
			}
		}
	}()

	c.logf("ws: registered slave_id=%s server_instance=%s", c.slaveID, ack.ServerInstanceID)

	for {
		select {
		case <-connCtx.Done():
			return connCtx.Err()
		case err := <-readErr:
			return err
		case <-stopTicker.C:
			if c.stopRequested != nil && c.stopRequested() {
				c.draining.Store(true)
				if c.activeRuns.Load() == 0 {
					return ErrStopRequested
				}
			}
		case <-ticker.C:
			hbEnv, err := NewEnvelope(MsgTypeHeartbeat, "", HeartbeatPayload{SlaveID: c.slaveID})
			if err != nil {
				continue
			}
			msg, err := hbEnv.Marshal()
			if err != nil {
				continue
			}
			_ = session.WriteText(context.Background(), msg)
		}
	}
}

func (c *SlaveClient) sendRegister(ctx context.Context, session *SlaveSession) error {
	now := time.Now().UTC().Unix()
	nonce := NewID("n")
	sig, err := SignRegister(c.secret, c.slaveID, now, nonce)
	if err != nil {
		return err
	}
	payload := RegisterPayload{
		SlaveID: c.slaveID,
		Name:    c.name,
		Auth: RegisterAuth{
			TS:    now,
			Nonce: nonce,
			Sig:   sig,
		},
		Version:      c.version,
		Capabilities: c.capabilities,
		Meta:         c.meta,
	}
	env, err := NewEnvelope(MsgTypeRegister, "", payload)
	if err != nil {
		return err
	}
	msg, err := env.Marshal()
	if err != nil {
		return err
	}
	return session.WriteText(ctx, msg)
}

func (c *SlaveClient) waitRegisterAck(ctx context.Context, conn *websocket.Conn) (RegisterAckPayload, error) {
	ackCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		mt, data, err := conn.Read(ackCtx)
		if err != nil {
			return RegisterAckPayload{}, err
		}
		if mt != websocket.MessageText {
			continue
		}
		env, err := UnmarshalEnvelope(data)
		if err != nil {
			continue
		}
		if env.Type != MsgTypeRegisterAck {
			continue
		}
		var out RegisterAckPayload
		if len(env.Payload) > 0 {
			if err := json.Unmarshal(env.Payload, &out); err != nil {
				return RegisterAckPayload{}, err
			}
		}
		return out, nil
	}
}

type AgentRunner interface {
	Run(ctx context.Context, task string, opts AgentRunOptions, metadata map[string]any) (output string, runID string, err error)
}

const maxAgentOutputChars = 200000

func (c *SlaveClient) dispatchAgentRun(ctx context.Context, session *SlaveSession, requestID string, req AgentRunPayload) {
	if c == nil {
		return
	}
	if c.stopRequested != nil && c.stopRequested() {
		c.draining.Store(true)
	}
	if c.draining.Load() {
		c.logf("cmd: agent_run rejected (restarting) request_id=%s", strings.TrimSpace(requestID))
		c.sendAgentResult(ctx, session, requestID, AgentResultPayload{
			Status: "failed",
			Error:  "slave is restarting",
		})
		return
	}
	task := strings.TrimSpace(req.Task)
	if task == "" {
		c.logf("cmd: agent_run rejected (empty task) request_id=%s", strings.TrimSpace(requestID))
		c.sendAgentResult(ctx, session, requestID, AgentResultPayload{
			Status: "failed",
			Error:  "task is required",
		})
		return
	}

	c.logf(
		"cmd: agent_run received request_id=%s timeout_seconds=%d max_turns=%d max_tokens=%d temperature=%v task=%s",
		strings.TrimSpace(requestID),
		req.Options.TimeoutSeconds,
		req.Options.MaxTurns,
		req.Options.MaxTokens,
		req.Options.Temperature,
		previewOneLine(task, 200),
	)

	select {
	case c.inflight <- struct{}{}:
	default:
		c.logf("cmd: agent_run rejected (busy) request_id=%s max_inflight=%d", strings.TrimSpace(requestID), cap(c.inflight))
		c.sendAgentResult(ctx, session, requestID, AgentResultPayload{
			Status: "busy",
			Error:  "slave is busy (max inflight reached)",
		})
		return
	}

	c.activeRuns.Add(1)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer func() { <-c.inflight }()
		defer c.activeRuns.Add(-1)

		start := time.Now()
		runCtx := ctx
		cancel := func() {}
		if req.Options.TimeoutSeconds > 0 {
			runCtx, cancel = context.WithTimeout(ctx, time.Duration(req.Options.TimeoutSeconds)*time.Second)
		}
		defer cancel()

		var (
			out   string
			runID string
			err   error
		)
		if c.runner == nil {
			err = errors.New("no runner configured on slave")
		} else {
			metadata := map[string]any{
				"request_id": strings.TrimSpace(requestID),
			}
			for k, v := range req.Metadata {
				metadata[k] = v
			}
			out, runID, err = c.runner.Run(runCtx, task, req.Options, metadata)
		}

		dur := time.Since(start)
		res := AgentResultPayload{
			Status:     "completed",
			Output:     truncateString(out, maxAgentOutputChars),
			DurationMS: dur.Milliseconds(),
			RunID:      runID,
		}
		if err != nil {
			res.Status = "failed"
			res.Error = err.Error()
		}
		c.logf(
			"cmd: agent_run finished request_id=%s status=%s duration_ms=%d run_id=%s error=%s output=%s",
			strings.TrimSpace(requestID),
			strings.TrimSpace(res.Status),
			res.DurationMS,
			strings.TrimSpace(runID),
			previewOneLine(res.Error, 160),
			previewOneLine(out, 240),
		)
		c.sendAgentResult(ctx, session, requestID, res)
	}()
}

func (c *SlaveClient) sendAgentResult(ctx context.Context, session *SlaveSession, requestID string, payload AgentResultPayload) {
	env, err := NewEnvelope(MsgTypeAgentResult, requestID, payload)
	if err != nil {
		return
	}
	msg, err := env.Marshal()
	if err != nil {
		return
	}
	_ = session.WriteText(ctx, msg)
}

func truncateString(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	if max < 16 {
		return s[:max]
	}
	return s[:max-14] + "\n... (truncated)"
}

func previewOneLine(s string, max int) string {
	text := strings.TrimSpace(s)
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ")
	return truncateString(text, max)
}
