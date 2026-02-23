package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (c *SlaveClient) handleFileEnvelope(ctx context.Context, session *SlaveSession, env Envelope) {
	if c == nil {
		return
	}
	switch env.Type {
	case MsgTypeFileOffer:
		var offer FileOfferPayload
		if len(env.Payload) > 0 {
			if err := json.Unmarshal(env.Payload, &offer); err != nil {
				return
			}
		}
		c.handleIncomingOffer(ctx, session, offer)
	case MsgTypeFileAccept:
		var p FileAcceptPayload
		if len(env.Payload) > 0 {
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return
			}
		}
		c.deliverFileOfferReply(fileOfferReply{Accepted: true, Accept: p})
	case MsgTypeFileReject:
		var p FileRejectPayload
		if len(env.Payload) > 0 {
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return
			}
		}
		c.deliverFileOfferReply(fileOfferReply{Accepted: false, Reject: p})
	case MsgTypeFileAck:
		var p FileAckPayload
		if len(env.Payload) > 0 {
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return
			}
		}
		c.deliverFileAck(p)
	case MsgTypeFileComplete:
		var p FileCompletePayload
		if len(env.Payload) > 0 {
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return
			}
		}
		c.handleIncomingComplete(session, p)
	case MsgTypeFilePull:
		var p FilePullPayload
		if len(env.Payload) > 0 {
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return
			}
		}
		go c.handlePull(ctx, session, p)
	case MsgTypeFileCancel:
		var p FileCompletePayload
		if len(env.Payload) > 0 {
			_ = json.Unmarshal(env.Payload, &p)
		}
		c.abortIncoming(strings.TrimSpace(p.TransferID), errors.New("transfer canceled"))
	default:
	}
}

func (c *SlaveClient) handleFileBinary(session *SlaveSession, data []byte) {
	if c == nil {
		return
	}
	hdr, chunk, err := parseChunkBinaryFrame(data)
	if err != nil {
		return
	}
	id := strings.TrimSpace(hdr.TransferID)
	if id == "" {
		return
	}

	c.fileMu.Lock()
	t := c.incomingTransfers[id]
	c.fileMu.Unlock()
	if t == nil {
		return
	}

	if err := t.handleChunk(hdr, chunk); err != nil {
		ack := t.fail(err)
		c.fileMu.Lock()
		delete(c.incomingTransfers, id)
		c.fileMu.Unlock()
		_ = c.sendFileAck(context.Background(), session, ack)
		t.signalDone(ack)
	}
}

func (c *SlaveClient) handleIncomingOffer(ctx context.Context, session *SlaveSession, offer FileOfferPayload) {
	if c == nil {
		return
	}
	offer.TransferID = strings.TrimSpace(offer.TransferID)
	if offer.TransferID == "" {
		_ = c.sendFileReject(ctx, session, FileRejectPayload{TransferID: "", Reason: "transfer_id is required"})
		return
	}

	peer := strings.TrimSpace(c.masterID)
	if peer == "" {
		peer = "master"
	}
	t, accept, err := newIncomingFileTransfer(c.files, peer, offer)
	if err != nil {
		_ = c.sendFileReject(ctx, session, FileRejectPayload{TransferID: offer.TransferID, Reason: err.Error()})
		return
	}

	c.fileMu.Lock()
	if c.incomingTransfers == nil {
		c.incomingTransfers = make(map[string]*incomingFileTransfer)
	}
	// If a transfer already exists with the same id, drop the old one.
	if old := c.incomingTransfers[offer.TransferID]; old != nil {
		ack := old.fail(errors.New("replaced by new offer"))
		old.signalDone(ack)
	}
	c.incomingTransfers[offer.TransferID] = t
	c.fileMu.Unlock()

	env, err := NewEnvelope(MsgTypeFileAccept, "", accept)
	if err != nil {
		ack := t.fail(err)
		t.signalDone(ack)
		_ = c.sendFileAck(context.Background(), session, ack)
		return
	}
	msg, err := env.Marshal()
	if err != nil {
		ack := t.fail(err)
		t.signalDone(ack)
		_ = c.sendFileAck(context.Background(), session, ack)
		return
	}
	_ = session.WriteText(ctx, msg)
}

func (c *SlaveClient) handleIncomingComplete(session *SlaveSession, complete FileCompletePayload) {
	if c == nil {
		return
	}
	id := strings.TrimSpace(complete.TransferID)
	if id == "" {
		return
	}
	c.fileMu.Lock()
	t := c.incomingTransfers[id]
	delete(c.incomingTransfers, id)
	c.fileMu.Unlock()
	if t == nil {
		return
	}
	ack, _ := t.finalize(complete, map[string]any{
		"role":      "slave",
		"master_id": strings.TrimSpace(c.masterID),
	})
	_ = c.sendFileAck(context.Background(), session, ack)
	t.signalDone(ack)
}

func (c *SlaveClient) abortIncoming(transferID string, err error) {
	if c == nil {
		return
	}
	id := strings.TrimSpace(transferID)
	if id == "" {
		return
	}
	c.fileMu.Lock()
	t := c.incomingTransfers[id]
	delete(c.incomingTransfers, id)
	c.fileMu.Unlock()
	if t == nil {
		return
	}
	ack := t.fail(err)
	t.signalDone(ack)
}

func (c *SlaveClient) deliverFileOfferReply(reply fileOfferReply) {
	if c == nil {
		return
	}
	transferID := ""
	if reply.Accepted {
		transferID = reply.Accept.TransferID
	} else {
		transferID = reply.Reject.TransferID
	}
	id := strings.TrimSpace(transferID)
	if id == "" {
		return
	}
	c.fileMu.Lock()
	ch := c.pendingFileReplies[id]
	c.fileMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- reply:
	default:
	}
}

func (c *SlaveClient) deliverFileAck(ack FileAckPayload) {
	if c == nil {
		return
	}
	id := strings.TrimSpace(ack.TransferID)
	if id == "" {
		return
	}
	c.fileMu.Lock()
	ch := c.pendingFileAcks[id]
	c.fileMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- ack:
	default:
	}
}

func (c *SlaveClient) sendFileReject(ctx context.Context, session *SlaveSession, payload FileRejectPayload) error {
	env, err := NewEnvelope(MsgTypeFileReject, "", payload)
	if err != nil {
		return err
	}
	msg, err := env.Marshal()
	if err != nil {
		return err
	}
	return session.WriteText(ctx, msg)
}

func (c *SlaveClient) sendFileAck(ctx context.Context, session *SlaveSession, payload FileAckPayload) error {
	env, err := NewEnvelope(MsgTypeFileAck, "", payload)
	if err != nil {
		return err
	}
	msg, err := env.Marshal()
	if err != nil {
		return err
	}
	return session.WriteText(ctx, msg)
}

func (c *SlaveClient) handlePull(ctx context.Context, session *SlaveSession, pull FilePullPayload) {
	if c == nil || session == nil {
		return
	}
	transferID := strings.TrimSpace(pull.TransferID)
	if transferID == "" {
		_ = c.sendFileReject(ctx, session, FileRejectPayload{TransferID: "", Reason: "transfer_id is required"})
		return
	}
	remote := strings.TrimSpace(pull.RemotePath)
	if remote == "" {
		_ = c.sendFileReject(ctx, session, FileRejectPayload{TransferID: transferID, Reason: "remote_path is required"})
		return
	}

	abs, err := safeJoin(c.files.RootDir, remote)
	if err != nil {
		_ = c.sendFileReject(ctx, session, FileRejectPayload{TransferID: transferID, Reason: err.Error()})
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		_ = c.sendFileReject(ctx, session, FileRejectPayload{TransferID: transferID, Reason: err.Error()})
		return
	}
	if !info.Mode().IsRegular() {
		_ = c.sendFileReject(ctx, session, FileRejectPayload{TransferID: transferID, Reason: "remote_path must be a regular file"})
		return
	}
	size := info.Size()

	replyCh := make(chan fileOfferReply, 1)
	ackCh := make(chan FileAckPayload, 1)
	c.fileMu.Lock()
	c.pendingFileReplies[transferID] = replyCh
	c.pendingFileAcks[transferID] = ackCh
	c.fileMu.Unlock()
	defer func() {
		c.fileMu.Lock()
		delete(c.pendingFileReplies, transferID)
		delete(c.pendingFileAcks, transferID)
		c.fileMu.Unlock()
	}()

	offer := FileOfferPayload{
		TransferID: transferID,
		Direction:  "push",
		Filename:   filepath.Base(abs),
		SizeBytes:  size,
		Metadata: map[string]any{
			"remote_path": remote,
		},
	}
	offerEnv, err := NewEnvelope(MsgTypeFileOffer, "", offer)
	if err != nil {
		return
	}
	offerMsg, err := offerEnv.Marshal()
	if err != nil {
		return
	}
	if err := session.WriteText(ctx, offerMsg); err != nil {
		return
	}

	waitCtx := ctx
	cancel := func() {}
	if ctx == nil {
		waitCtx = context.Background()
	}
	// Give the master some time to accept.
	waitCtx, cancel = context.WithTimeout(waitCtx, 30*time.Second)
	defer cancel()

	var reply fileOfferReply
	select {
	case reply = <-replyCh:
	case <-waitCtx.Done():
		return
	}
	if !reply.Accepted {
		return
	}

	chunkSize := reply.Accept.ChunkSizeBytes
	if chunkSize <= 0 {
		chunkSize = c.files.ChunkSizeBytes
	}
	if chunkSize <= 0 {
		chunkSize = 256 << 10
	}

	f, err := os.Open(abs)
	if err != nil {
		return
	}
	defer f.Close()

	hasher := sha256.New()
	buf := make([]byte, chunkSize)
	var (
		seq    int64
		offset int64
	)
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			_, _ = hasher.Write(chunk)
			hdr := FileChunkHeader{
				TransferID: transferID,
				Seq:        seq,
				Offset:     offset,
				Len:        n,
			}
			headerRaw, err := json.Marshal(hdr)
			if err != nil {
				return
			}
			frame := make([]byte, 0, len(headerRaw)+1+len(chunk))
			frame = append(frame, headerRaw...)
			frame = append(frame, '\n')
			frame = append(frame, chunk...)
			if err := session.WriteBinary(ctx, frame); err != nil {
				return
			}
			seq++
			offset += int64(n)
		}
		if rerr == nil {
			continue
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		return
	}

	sum := hex.EncodeToString(hasher.Sum(nil))
	complete := FileCompletePayload{
		TransferID: transferID,
		SizeBytes:  size,
		SHA256:     sum,
	}
	completeEnv, err := NewEnvelope(MsgTypeFileComplete, "", complete)
	if err != nil {
		return
	}
	completeMsg, err := completeEnv.Marshal()
	if err != nil {
		return
	}
	_ = session.WriteText(ctx, completeMsg)

	// Best-effort wait for ack.
	select {
	case <-ackCh:
	case <-time.After(10 * time.Second):
	}
}
