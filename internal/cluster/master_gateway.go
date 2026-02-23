package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

type MasterGatewayOptions struct {
	Secret             []byte
	InstanceID         string
	Registry           *SlaveRegistry
	Presence           PresenceStore
	PresenceTTLSeconds int
	Files              FilesConfig
	HeartbeatInterval  time.Duration
	MaxMessageBytes    int64
	AllowedClockSkew   time.Duration
	NonceCache         *NonceCache
	Logf               func(format string, args ...any)
	AcceptOriginAny    bool
	AllowedOrigins     []string
	RegisterTimeout    time.Duration
	HeartbeatAckEnable bool
}

type MasterGateway struct {
	secret            []byte
	instanceID        string
	registry          *SlaveRegistry
	presence          PresenceStore
	presenceTTLSeconds int
	files              FilesConfig
	heartbeatInterval time.Duration
	maxMessageBytes   int64
	registerTimeout   time.Duration
	sendHeartbeatAck  bool

	auth AuthVerifier
	logf func(format string, args ...any)

	originPatterns []string

	pendingMu sync.Mutex
	pending   map[string]chan AgentResultPayload

	fileMu              sync.Mutex
	pendingFileReplies  map[string]chan fileOfferReply
	pendingFileOffers   map[string]chan FileOfferPayload
	pendingFileAcks     map[string]chan FileAckPayload
	incomingFileTransfers map[string]*incomingFileTransfer
}

func NewMasterGateway(opts MasterGatewayOptions) (*MasterGateway, error) {
	if len(opts.Secret) == 0 {
		return nil, errors.New("cluster secret is required")
	}
	reg := opts.Registry
	if reg == nil {
		reg = NewSlaveRegistry()
	}
	instanceID := strings.TrimSpace(opts.InstanceID)
	if instanceID == "" {
		instanceID = NewID("master")
	}
	hb := opts.HeartbeatInterval
	if hb <= 0 {
		hb = 5 * time.Second
	}
	maxMsg := opts.MaxMessageBytes
	if maxMsg <= 0 {
		maxMsg = 4 << 20 // 4MiB
	}
	regTimeout := opts.RegisterTimeout
	if regTimeout <= 0 {
		regTimeout = 10 * time.Second
	}

	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	presence := opts.Presence
	if presence == nil {
		presence = NoopPresenceStore{}
	}
	ttlSeconds := opts.PresenceTTLSeconds
	if ttlSeconds <= 0 {
		ttlSeconds = 15
	}

	filesCfg := normalizeFilesConfig(opts.Files)
	if err := ensureFileRootDirs(filesCfg.RootDir); err != nil {
		return nil, err
	}

	skew := opts.AllowedClockSkew
	if skew <= 0 {
		skew = DefaultAuthSkew
	}
	nonces := opts.NonceCache
	if nonces == nil {
		nonces = NewNonceCache(DefaultNonceTTL, DefaultNonceMax)
	}

	originPatterns := opts.AllowedOrigins
	if opts.AcceptOriginAny || len(originPatterns) == 0 {
		originPatterns = []string{"*"}
	}

	g := &MasterGateway{
		secret:            opts.Secret,
		instanceID:        instanceID,
		registry:          reg,
		presence:          presence,
		presenceTTLSeconds: ttlSeconds,
		files:              filesCfg,
		heartbeatInterval: hb,
		maxMessageBytes:   maxMsg,
		registerTimeout:   regTimeout,
		sendHeartbeatAck:  opts.HeartbeatAckEnable,
		logf:              logf,
		originPatterns:    originPatterns,
		pending:           make(map[string]chan AgentResultPayload),
		pendingFileReplies:  make(map[string]chan fileOfferReply),
		pendingFileOffers:   make(map[string]chan FileOfferPayload),
		pendingFileAcks:     make(map[string]chan FileAckPayload),
		incomingFileTransfers: make(map[string]*incomingFileTransfer),
	}
	g.auth = AuthVerifier{
		Secret: opts.Secret,
		Skew:   skew,
		Nonces: nonces,
		Now:    func() time.Time { return time.Now().UTC() },
	}
	return g, nil
}

func (g *MasterGateway) InstanceID() string {
	if g == nil {
		return ""
	}
	return g.instanceID
}

func (g *MasterGateway) Registry() *SlaveRegistry {
	if g == nil {
		return nil
	}
	return g.registry
}

func (g *MasterGateway) WSHandler() http.Handler {
	return http.HandlerFunc(g.handleWS)
}

func (g *MasterGateway) handleWS(w http.ResponseWriter, r *http.Request) {
	if g == nil {
		http.Error(w, "gateway not configured", http.StatusInternalServerError)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: g.originPatterns,
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(g.maxMessageBytes)
	remote := strings.TrimSpace(r.RemoteAddr)
	go g.handleConn(conn, remote)
}

func (g *MasterGateway) handleConn(conn *websocket.Conn, remoteAddr string) {
	defer func() {
		_ = conn.Close(websocket.StatusNormalClosure, "bye")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), g.registerTimeout)
	defer cancel()

	mt, data, err := conn.Read(ctx)
	if err != nil {
		g.logf("ws: read register failed remote=%s err=%v", remoteAddr, err)
		_ = conn.Close(websocket.StatusPolicyViolation, "register required")
		return
	}
	if mt != websocket.MessageText {
		_ = conn.Close(websocket.StatusUnsupportedData, "text frames only")
		return
	}
	env, err := UnmarshalEnvelope(data)
	if err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "invalid message")
		return
	}
	if env.Type != MsgTypeRegister {
		_ = conn.Close(websocket.StatusPolicyViolation, "register required")
		return
	}
	var reg RegisterPayload
	if len(env.Payload) > 0 {
		if err := json.Unmarshal(env.Payload, &reg); err != nil {
			_ = conn.Close(websocket.StatusPolicyViolation, "invalid register payload")
			return
		}
	}
	reg.SlaveID = strings.TrimSpace(reg.SlaveID)
	if reg.SlaveID == "" {
		_ = conn.Close(websocket.StatusPolicyViolation, "slave_id required")
		return
	}
	if err := g.auth.VerifyRegister(reg.SlaveID, reg.Auth); err != nil {
		g.logf("ws: register auth failed slave_id=%s remote=%s err=%v", reg.SlaveID, remoteAddr, err)
		_ = conn.Close(websocket.StatusPolicyViolation, "auth failed")
		return
	}

	now := time.Now().UTC()
	info := SlaveInfo{
		SlaveID:      reg.SlaveID,
		Name:         strings.TrimSpace(reg.Name),
		Version:      strings.TrimSpace(reg.Version),
		Capabilities: reg.Capabilities,
		Meta:         reg.Meta,
		Status:       SlaveStatusOnline,
		RemoteAddr:   remoteAddr,
		ConnectedAt:  now,
		LastSeen:     now,
	}
	session := &SlaveSession{conn: conn}
	if replaced := g.registry.SetOnline(info, session); replaced != nil && replaced != session {
		replaced.Close(websocket.StatusPolicyViolation, "replaced by new connection")
	}
	g.upsertPresence(info)

	ackPayload := RegisterAckPayload{
		Accepted:                true,
		HeartbeatIntervalMillis: g.heartbeatInterval.Milliseconds(),
		ServerInstanceID:        g.instanceID,
	}
	ackEnv, err := NewEnvelope(MsgTypeRegisterAck, env.ID, ackPayload)
	if err == nil {
		if msg, err := ackEnv.Marshal(); err == nil {
			_ = session.WriteText(context.Background(), msg)
		}
	}
	g.logf("ws: slave registered slave_id=%s name=%s remote=%s", info.SlaveID, info.Name, remoteAddr)

	lastSeen := now
	for {
		mt, data, err := conn.Read(context.Background())
		if err != nil {
			break
		}
		switch mt {
		case websocket.MessageText:
			env, err := UnmarshalEnvelope(data)
			if err != nil {
				continue
			}
			switch env.Type {
			case MsgTypeHeartbeat:
				lastSeen = time.Now().UTC()
				g.registry.MarkSeen(info.SlaveID, lastSeen)
				if rec, ok := g.registry.Get(info.SlaveID); ok {
					g.upsertPresence(rec.Info)
				}
				if g.sendHeartbeatAck {
					ack, err := NewEnvelope(MsgTypeHeartbeatAck, env.ID, HeartbeatAckPayload{ServerTimeUnix: time.Now().UTC().Unix()})
					if err == nil {
						if msg, err := ack.Marshal(); err == nil {
							_ = session.WriteText(context.Background(), msg)
						}
					}
				}
			case MsgTypeAgentResult:
				var res AgentResultPayload
				if len(env.Payload) > 0 {
					if err := json.Unmarshal(env.Payload, &res); err != nil {
						continue
					}
				}
				g.deliverAgentResult(env.ID, res)
			case MsgTypeFileAccept, MsgTypeFileReject, MsgTypeFileOffer, MsgTypeFileAck, MsgTypeFileComplete, MsgTypeFileCancel:
				g.handleFileEnvelope(info.SlaveID, session, env)
			default:
				// ignore
			}
		case websocket.MessageBinary:
			g.handleFileBinary(info.SlaveID, session, data)
		default:
			continue
		}
	}

	g.registry.SetOffline(info.SlaveID, session, lastSeen)
	g.deletePresence(info.SlaveID)
	g.abortFileTransfers(info.SlaveID, errors.New("slave disconnected"))
	g.logf("ws: slave disconnected slave_id=%s remote=%s", info.SlaveID, remoteAddr)
}

func (g *MasterGateway) upsertPresence(info SlaveInfo) {
	if g == nil || g.presence == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = g.presence.Upsert(ctx, info, g.instanceID, g.presenceTTLSeconds)
}

func (g *MasterGateway) deletePresence(slaveID string) {
	if g == nil || g.presence == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = g.presence.Delete(ctx, slaveID)
}

func (g *MasterGateway) deliverAgentResult(requestID string, res AgentResultPayload) {
	if g == nil {
		return
	}
	id := strings.TrimSpace(requestID)
	if id == "" {
		return
	}
	g.pendingMu.Lock()
	ch := g.pending[id]
	g.pendingMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- res:
	default:
	}
}

func (g *MasterGateway) SendAgentRun(ctx context.Context, slaveID string, payload AgentRunPayload, timeout time.Duration) (string, AgentResultPayload, error) {
	if g == nil {
		return "", AgentResultPayload{}, errors.New("gateway is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	id := strings.TrimSpace(slaveID)
	if id == "" {
		return "", AgentResultPayload{}, errors.New("slave id is required")
	}

	rec, ok := g.registry.Get(id)
	if !ok || rec.Session == nil {
		return "", AgentResultPayload{}, fmt.Errorf("slave offline: %s", id)
	}
	reqID := NewID("req")
	env, err := NewEnvelope(MsgTypeAgentRun, reqID, payload)
	if err != nil {
		return reqID, AgentResultPayload{}, err
	}
	msg, err := env.Marshal()
	if err != nil {
		return reqID, AgentResultPayload{}, err
	}

	ch := make(chan AgentResultPayload, 1)
	g.pendingMu.Lock()
	if g.pending == nil {
		g.pending = make(map[string]chan AgentResultPayload)
	}
	g.pending[reqID] = ch
	g.pendingMu.Unlock()
	defer func() {
		g.pendingMu.Lock()
		delete(g.pending, reqID)
		g.pendingMu.Unlock()
	}()

	if err := rec.Session.WriteText(ctx, msg); err != nil {
		return reqID, AgentResultPayload{}, err
	}

	waitCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	select {
	case res := <-ch:
		return reqID, res, nil
	case <-waitCtx.Done():
		return reqID, AgentResultPayload{}, waitCtx.Err()
	}
}
