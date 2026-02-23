package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const ProtocolVersion = 1

const (
	MsgTypeRegister      = "register"
	MsgTypeRegisterAck   = "register_ack"
	MsgTypeHeartbeat     = "heartbeat"
	MsgTypeHeartbeatAck  = "heartbeat_ack"
	MsgTypeAgentRun      = "agent.run"
	MsgTypeAgentResult   = "agent.result"
	MsgTypeFileOffer     = "file.offer"
	MsgTypeFileAccept    = "file.accept"
	MsgTypeFileReject    = "file.reject"
	MsgTypeFileComplete  = "file.complete"
	MsgTypeFileCancel    = "file.cancel"
	MsgTypeFileError     = "file.error"
	MsgTypeFileAck       = "file.ack"
	MsgTypeFilePull      = "file.pull"
	MsgTypeFileChunk     = "file.chunk" // reserved; MVP uses WS binary frames.
)

type Envelope struct {
	Type            string          `json:"type"`
	ID              string          `json:"id"`
	TS              int64           `json:"ts"`
	ProtocolVersion int             `json:"protocol_version"`
	Payload         json.RawMessage `json:"payload"`
}

func NewEnvelope(msgType string, id string, payload any) (Envelope, error) {
	typ := strings.TrimSpace(msgType)
	if typ == "" {
		return Envelope{}, errors.New("message type is required")
	}
	if strings.TrimSpace(id) == "" {
		id = NewID("msg")
	}
	var raw json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return Envelope{}, err
		}
		raw = data
	}
	return Envelope{
		Type:            typ,
		ID:              strings.TrimSpace(id),
		TS:              time.Now().UTC().Unix(),
		ProtocolVersion: ProtocolVersion,
		Payload:         raw,
	}, nil
}

func (e Envelope) Marshal() ([]byte, error) {
	if strings.TrimSpace(e.Type) == "" {
		return nil, errors.New("envelope.type is required")
	}
	if strings.TrimSpace(e.ID) == "" {
		return nil, errors.New("envelope.id is required")
	}
	if e.ProtocolVersion == 0 {
		e.ProtocolVersion = ProtocolVersion
	}
	data, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func UnmarshalEnvelope(data []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Envelope{}, err
	}
	env.Type = strings.TrimSpace(env.Type)
	env.ID = strings.TrimSpace(env.ID)
	if env.Type == "" || env.ID == "" {
		return Envelope{}, fmt.Errorf("invalid envelope (type=%q id=%q)", env.Type, env.ID)
	}
	if env.ProtocolVersion == 0 {
		env.ProtocolVersion = ProtocolVersion
	}
	return env, nil
}

type RegisterAuth struct {
	TS    int64  `json:"ts"`
	Nonce string `json:"nonce"`
	Sig   string `json:"sig"`
}

type RegisterPayload struct {
	SlaveID      string         `json:"slave_id"`
	Name         string         `json:"name,omitempty"`
	Auth         RegisterAuth   `json:"auth"`
	Version      string         `json:"version,omitempty"`
	Capabilities []string       `json:"capabilities,omitempty"`
	Meta         map[string]any `json:"meta,omitempty"`
}

type RegisterAckPayload struct {
	Accepted                bool   `json:"accepted"`
	Reason                  string `json:"reason,omitempty"`
	HeartbeatIntervalMillis int64  `json:"heartbeat_interval_millis,omitempty"`
	ServerInstanceID        string `json:"server_instance_id,omitempty"`
}

type HeartbeatPayload struct {
	SlaveID string `json:"slave_id,omitempty"`
}

type HeartbeatAckPayload struct {
	ServerTimeUnix int64 `json:"server_time_unix"`
}

type AgentRunOptions struct {
	MaxTurns       int     `json:"max_turns,omitempty"`
	Temperature    float64 `json:"temperature,omitempty"`
	MaxTokens      int     `json:"max_tokens,omitempty"`
	TimeoutSeconds int     `json:"timeout_seconds,omitempty"`
}

type AgentRunPayload struct {
	Task     string         `json:"task"`
	Options  AgentRunOptions `json:"options,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type AgentResultPayload struct {
	Status     string `json:"status"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	RunID      string `json:"run_id,omitempty"`
}

type FileOfferPayload struct {
	TransferID   string         `json:"transfer_id"`
	Direction    string         `json:"direction,omitempty"` // push|pull
	Filename     string         `json:"filename"`
	SizeBytes    int64          `json:"size_bytes"`
	SHA256       string         `json:"sha256,omitempty"`
	ContentType  string         `json:"content_type,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type FileAcceptPayload struct {
	TransferID        string `json:"transfer_id"`
	ChunkSizeBytes    int    `json:"chunk_size_bytes"`
	MaxInflightChunks int    `json:"max_inflight_chunks"`
	SaveHint          string `json:"save_hint,omitempty"`
}

type FileRejectPayload struct {
	TransferID string `json:"transfer_id"`
	Reason     string `json:"reason,omitempty"`
}

type FileCompletePayload struct {
	TransferID string `json:"transfer_id"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
}

type FilePullPayload struct {
	TransferID  string `json:"transfer_id"`
	RemotePath  string `json:"remote_path"`
}

type FileAckPayload struct {
	TransferID     string `json:"transfer_id"`
	Status         string `json:"status"` // accepted|in_progress|completed|failed
	SizeBytes      int64  `json:"size_bytes,omitempty"`
	BytesReceived  int64  `json:"bytes_received,omitempty"`
	SavedPath      string `json:"saved_path,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
	Error          string `json:"error,omitempty"`
}

type FileChunkHeader struct {
	TransferID string `json:"transfer_id"`
	Seq        int64  `json:"seq"`
	Offset     int64  `json:"offset"`
	Len        int    `json:"len"`
}
