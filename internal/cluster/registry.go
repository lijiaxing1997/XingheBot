package cluster

import (
	"context"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

type SlaveStatus string

const (
	SlaveStatusOnline  SlaveStatus = "online"
	SlaveStatusOffline SlaveStatus = "offline"
)

type SlaveInfo struct {
	SlaveID      string         `json:"slave_id"`
	Name         string         `json:"name,omitempty"`
	Version      string         `json:"version,omitempty"`
	Capabilities []string       `json:"capabilities,omitempty"`
	Meta         map[string]any `json:"meta,omitempty"`

	Status      SlaveStatus `json:"status"`
	RemoteAddr  string      `json:"remote_addr,omitempty"`
	ConnectedAt time.Time   `json:"connected_at,omitempty"`
	LastSeen    time.Time   `json:"last_seen,omitempty"`
}

type SlaveSession struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func (s *SlaveSession) Close(status websocket.StatusCode, reason string) {
	if s == nil || s.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.conn.Close(status, reason)
	_ = s.conn.CloseRead(ctx)
}

func (s *SlaveSession) WriteText(ctx context.Context, data []byte) error {
	if s == nil || s.conn == nil {
		return context.Canceled
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.Write(ctx, websocket.MessageText, data)
}

func (s *SlaveSession) WriteBinary(ctx context.Context, data []byte) error {
	if s == nil || s.conn == nil {
		return context.Canceled
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.Write(ctx, websocket.MessageBinary, data)
}

type SlaveRecord struct {
	Info    SlaveInfo
	Session *SlaveSession
}

type SlaveRegistry struct {
	mu     sync.RWMutex
	slaves map[string]*SlaveRecord
}

func NewSlaveRegistry() *SlaveRegistry {
	return &SlaveRegistry{slaves: make(map[string]*SlaveRecord)}
}

func (r *SlaveRegistry) SetOnline(info SlaveInfo, session *SlaveSession) (replaced *SlaveSession) {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.slaves == nil {
		r.slaves = make(map[string]*SlaveRecord)
	}
	rec := r.slaves[info.SlaveID]
	if rec == nil {
		rec = &SlaveRecord{}
		r.slaves[info.SlaveID] = rec
	}
	replaced = rec.Session
	rec.Info = info
	rec.Session = session
	return replaced
}

func (r *SlaveRegistry) SetOffline(slaveID string, session *SlaveSession, lastSeen time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.slaves[slaveID]
	if rec == nil {
		return
	}
	if session != nil && rec.Session != session {
		return
	}
	rec.Session = nil
	rec.Info.Status = SlaveStatusOffline
	if !lastSeen.IsZero() {
		rec.Info.LastSeen = lastSeen
	}
}

func (r *SlaveRegistry) MarkSeen(slaveID string, when time.Time) {
	if r == nil {
		return
	}
	if when.IsZero() {
		when = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.slaves[slaveID]
	if rec == nil {
		return
	}
	rec.Info.LastSeen = when
	if rec.Session != nil {
		rec.Info.Status = SlaveStatusOnline
	}
}

func (r *SlaveRegistry) Get(slaveID string) (*SlaveRecord, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec := r.slaves[slaveID]
	if rec == nil {
		return nil, false
	}
	cp := *rec
	return &cp, true
}

func (r *SlaveRegistry) Snapshot(onlyOnline bool) []SlaveInfo {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SlaveInfo, 0, len(r.slaves))
	for _, rec := range r.slaves {
		if rec == nil {
			continue
		}
		if onlyOnline && (rec.Session == nil || rec.Info.Status != SlaveStatusOnline) {
			continue
		}
		out = append(out, rec.Info)
	}
	return out
}

