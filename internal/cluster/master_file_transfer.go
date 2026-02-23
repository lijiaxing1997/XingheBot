package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type fileOfferReply struct {
	Accepted bool
	Accept   FileAcceptPayload
	Reject   FileRejectPayload
}

func xferKey(slaveID string, transferID string) string {
	s := strings.TrimSpace(slaveID)
	t := strings.TrimSpace(transferID)
	return s + "/" + t
}

func (g *MasterGateway) handleFileEnvelope(slaveID string, session *SlaveSession, env Envelope) {
	if g == nil {
		return
	}
	switch env.Type {
	case MsgTypeFileAccept:
		var p FileAcceptPayload
		if len(env.Payload) > 0 {
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return
			}
		}
		g.deliverFileOfferReply(slaveID, fileOfferReply{Accepted: true, Accept: p})
	case MsgTypeFileReject:
		var p FileRejectPayload
		if len(env.Payload) > 0 {
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return
			}
		}
		g.deliverFileOfferReply(slaveID, fileOfferReply{Accepted: false, Reject: p})
	case MsgTypeFileOffer:
		var p FileOfferPayload
		if len(env.Payload) > 0 {
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return
			}
		}
		key := xferKey(slaveID, p.TransferID)
		g.fileMu.Lock()
		ch := g.pendingFileOffers[key]
		g.fileMu.Unlock()
		if ch == nil {
			// No one is waiting. Reject by default.
			_ = g.sendFileReject(context.Background(), session, FileRejectPayload{
				TransferID: strings.TrimSpace(p.TransferID),
				Reason:     "no pending receiver",
			})
			return
		}
		select {
		case ch <- p:
		default:
		}
	case MsgTypeFileAck:
		var p FileAckPayload
		if len(env.Payload) > 0 {
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return
			}
		}
		g.deliverFileAck(slaveID, p)
	case MsgTypeFileComplete:
		var p FileCompletePayload
		if len(env.Payload) > 0 {
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return
			}
		}
		g.handleFileComplete(slaveID, session, p)
	case MsgTypeFileCancel:
		// Optional for MVP.
		var p FileCompletePayload
		if len(env.Payload) > 0 {
			_ = json.Unmarshal(env.Payload, &p)
		}
		g.abortOneFileTransfer(slaveID, strings.TrimSpace(p.TransferID), errors.New("transfer canceled"))
	default:
	}
}

func (g *MasterGateway) deliverFileOfferReply(slaveID string, reply fileOfferReply) {
	if g == nil {
		return
	}
	transferID := ""
	if reply.Accepted {
		transferID = reply.Accept.TransferID
	} else {
		transferID = reply.Reject.TransferID
	}
	key := xferKey(slaveID, transferID)
	g.fileMu.Lock()
	ch := g.pendingFileReplies[key]
	g.fileMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- reply:
	default:
	}
}

func (g *MasterGateway) deliverFileAck(slaveID string, ack FileAckPayload) {
	if g == nil {
		return
	}
	key := xferKey(slaveID, ack.TransferID)
	g.fileMu.Lock()
	ch := g.pendingFileAcks[key]
	g.fileMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- ack:
	default:
	}
}

func (g *MasterGateway) handleFileBinary(slaveID string, session *SlaveSession, data []byte) {
	if g == nil {
		return
	}
	hdr, chunk, err := parseChunkBinaryFrame(data)
	if err != nil {
		return
	}
	key := xferKey(slaveID, hdr.TransferID)
	g.fileMu.Lock()
	t := g.incomingFileTransfers[key]
	g.fileMu.Unlock()
	if t == nil {
		return
	}
	if err := t.handleChunk(hdr, chunk); err != nil {
		ack := t.fail(err)
		g.fileMu.Lock()
		delete(g.incomingFileTransfers, key)
		g.fileMu.Unlock()
		_ = g.sendFileAck(context.Background(), session, ack)
		t.signalDone(ack)
	}
}

func (g *MasterGateway) handleFileComplete(slaveID string, session *SlaveSession, complete FileCompletePayload) {
	if g == nil {
		return
	}
	key := xferKey(slaveID, complete.TransferID)
	g.fileMu.Lock()
	t := g.incomingFileTransfers[key]
	g.fileMu.Unlock()
	if t == nil {
		return
	}

	ack, err := t.finalize(complete, map[string]any{
		"role":     "master",
		"slave_id": strings.TrimSpace(slaveID),
	})
	g.fileMu.Lock()
	delete(g.incomingFileTransfers, key)
	g.fileMu.Unlock()

	_ = g.sendFileAck(context.Background(), session, ack)
	t.signalDone(ack)
	_ = err
}

func (g *MasterGateway) sendFileReject(ctx context.Context, session *SlaveSession, payload FileRejectPayload) error {
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

func (g *MasterGateway) sendFileAck(ctx context.Context, session *SlaveSession, payload FileAckPayload) error {
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

func (g *MasterGateway) abortFileTransfers(slaveID string, err error) {
	if g == nil {
		return
	}
	prefix := strings.TrimSpace(slaveID) + "/"
	g.fileMu.Lock()
	keys := make([]string, 0, len(g.incomingFileTransfers))
	for k := range g.incomingFileTransfers {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		t := g.incomingFileTransfers[k]
		delete(g.incomingFileTransfers, k)
		if t != nil {
			ack := t.fail(err)
			t.signalDone(ack)
		}
	}
	g.fileMu.Unlock()
}

func (g *MasterGateway) abortOneFileTransfer(slaveID string, transferID string, err error) {
	if g == nil {
		return
	}
	key := xferKey(slaveID, transferID)
	g.fileMu.Lock()
	t := g.incomingFileTransfers[key]
	delete(g.incomingFileTransfers, key)
	g.fileMu.Unlock()
	if t == nil {
		return
	}
	ack := t.fail(err)
	t.signalDone(ack)
}

type FilePutResult struct {
	TransferID string
	RemotePath string
	SizeBytes  int64
	SHA256     string
	Ack        FileAckPayload
}

func (g *MasterGateway) SendFilePut(ctx context.Context, slaveID string, localPath string, remoteName string, timeout time.Duration) (FilePutResult, error) {
	if g == nil {
		return FilePutResult{}, errors.New("gateway is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	id := strings.TrimSpace(slaveID)
	if id == "" {
		return FilePutResult{}, errors.New("slave is required")
	}
	path := strings.TrimSpace(localPath)
	if path == "" {
		return FilePutResult{}, errors.New("local_path is required")
	}

	info, err := os.Stat(path)
	if err != nil {
		return FilePutResult{}, err
	}
	if !info.Mode().IsRegular() {
		return FilePutResult{}, errors.New("local_path must be a regular file")
	}
	size := info.Size()

	rec, ok := g.registry.Get(id)
	if !ok || rec.Session == nil {
		return FilePutResult{}, fmt.Errorf("slave offline: %s", id)
	}

	filename := strings.TrimSpace(remoteName)
	if filename == "" {
		filename = filepath.Base(path)
	}
	filename = sanitizeFilename(filename)
	transferID := NewID("xfer")
	key := xferKey(id, transferID)
	baseResult := FilePutResult{TransferID: transferID}

	replyCh := make(chan fileOfferReply, 1)
	ackCh := make(chan FileAckPayload, 1)
	g.fileMu.Lock()
	g.pendingFileReplies[key] = replyCh
	g.pendingFileAcks[key] = ackCh
	g.fileMu.Unlock()
	defer func() {
		g.fileMu.Lock()
		delete(g.pendingFileReplies, key)
		delete(g.pendingFileAcks, key)
		g.fileMu.Unlock()
	}()

	offer := FileOfferPayload{
		TransferID: transferID,
		Direction:  "push",
		Filename:   filename,
		SizeBytes:  size,
		Metadata: map[string]any{
			"local_path": path,
		},
	}
	env, err := NewEnvelope(MsgTypeFileOffer, "", offer)
	if err != nil {
		return baseResult, err
	}
	msg, err := env.Marshal()
	if err != nil {
		return baseResult, err
	}
	if err := rec.Session.WriteText(ctx, msg); err != nil {
		return baseResult, err
	}

	waitCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	var reply fileOfferReply
	select {
	case reply = <-replyCh:
	case <-waitCtx.Done():
		return baseResult, waitCtx.Err()
	}
	if !reply.Accepted {
		reason := strings.TrimSpace(reply.Reject.Reason)
		if reason == "" {
			reason = "rejected"
		}
		return baseResult, errors.New(reason)
	}

	chunkSize := reply.Accept.ChunkSizeBytes
	if chunkSize <= 0 {
		chunkSize = g.files.ChunkSizeBytes
	}
	if chunkSize <= 0 {
		chunkSize = 256 << 10
	}

	f, err := os.Open(path)
	if err != nil {
		return baseResult, err
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
				return baseResult, err
			}
			frame := make([]byte, 0, len(headerRaw)+1+len(chunk))
			frame = append(frame, headerRaw...)
			frame = append(frame, '\n')
			frame = append(frame, chunk...)
			if err := rec.Session.WriteBinary(waitCtx, frame); err != nil {
				return baseResult, err
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
		return baseResult, rerr
	}

	sha := hex.EncodeToString(hasher.Sum(nil))
	complete := FileCompletePayload{
		TransferID: transferID,
		SizeBytes:  size,
		SHA256:     sha,
	}
	completeEnv, err := NewEnvelope(MsgTypeFileComplete, "", complete)
	if err != nil {
		return baseResult, err
	}
	completeMsg, err := completeEnv.Marshal()
	if err != nil {
		return baseResult, err
	}
	if err := rec.Session.WriteText(waitCtx, completeMsg); err != nil {
		return baseResult, err
	}

	var ack FileAckPayload
	select {
	case ack = <-ackCh:
	case <-waitCtx.Done():
		return baseResult, waitCtx.Err()
	}

	status := strings.ToLower(strings.TrimSpace(ack.Status))
	if status != "completed" {
		reason := strings.TrimSpace(ack.Error)
		if reason == "" {
			reason = "transfer failed"
		}
		return FilePutResult{
			TransferID: transferID,
			RemotePath: strings.TrimSpace(ack.SavedPath),
			SizeBytes:  size,
			SHA256:     sha,
			Ack:        ack,
		}, errors.New(reason)
	}
	return FilePutResult{
		TransferID: transferID,
		RemotePath: strings.TrimSpace(ack.SavedPath),
		SizeBytes:  size,
		SHA256:     sha,
		Ack:        ack,
	}, nil
}

type FileGetResult struct {
	TransferID string
	LocalPath  string
	SizeBytes  int64
	SHA256     string
	Ack        FileAckPayload
}

func (g *MasterGateway) SendFileGet(ctx context.Context, slaveID string, remotePath string, timeout time.Duration) (FileGetResult, error) {
	if g == nil {
		return FileGetResult{}, errors.New("gateway is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	id := strings.TrimSpace(slaveID)
	if id == "" {
		return FileGetResult{}, errors.New("slave is required")
	}
	rp := strings.TrimSpace(remotePath)
	if rp == "" {
		return FileGetResult{}, errors.New("remote_path is required")
	}

	rec, ok := g.registry.Get(id)
	if !ok || rec.Session == nil {
		return FileGetResult{}, fmt.Errorf("slave offline: %s", id)
	}

	transferID := NewID("xfer")
	key := xferKey(id, transferID)
	offerCh := make(chan FileOfferPayload, 1)
	replyCh := make(chan fileOfferReply, 1)
	g.fileMu.Lock()
	g.pendingFileOffers[key] = offerCh
	g.pendingFileReplies[key] = replyCh
	g.fileMu.Unlock()
	defer func() {
		g.fileMu.Lock()
		delete(g.pendingFileOffers, key)
		delete(g.pendingFileReplies, key)
		g.fileMu.Unlock()
	}()

	pull := FilePullPayload{
		TransferID: transferID,
		RemotePath: rp,
	}
	env, err := NewEnvelope(MsgTypeFilePull, "", pull)
	if err != nil {
		return FileGetResult{TransferID: transferID}, err
	}
	msg, err := env.Marshal()
	if err != nil {
		return FileGetResult{TransferID: transferID}, err
	}
	if err := rec.Session.WriteText(ctx, msg); err != nil {
		return FileGetResult{TransferID: transferID}, err
	}

	waitCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	var offer FileOfferPayload
	for {
		select {
		case offer = <-offerCh:
			goto gotOffer
		case reply := <-replyCh:
			if reply.Accepted {
				continue
			}
			reason := strings.TrimSpace(reply.Reject.Reason)
			if reason == "" {
				reason = "rejected"
			}
			return FileGetResult{TransferID: transferID}, errors.New(reason)
		case <-waitCtx.Done():
			return FileGetResult{TransferID: transferID}, waitCtx.Err()
		}
	}

gotOffer:
	offer.TransferID = strings.TrimSpace(offer.TransferID)
	if offer.TransferID == "" {
		offer.TransferID = transferID
	}
	if offer.TransferID != transferID {
		return FileGetResult{TransferID: transferID}, fmt.Errorf("unexpected offer transfer_id=%s (expected %s)", offer.TransferID, transferID)
	}

	t, accept, err := newIncomingFileTransfer(g.files, id, offer)
	if err != nil {
		_ = g.sendFileReject(context.Background(), rec.Session, FileRejectPayload{TransferID: transferID, Reason: err.Error()})
		return FileGetResult{TransferID: transferID}, err
	}

	g.fileMu.Lock()
	g.incomingFileTransfers[key] = t
	g.fileMu.Unlock()
	defer func() {
		g.fileMu.Lock()
		delete(g.incomingFileTransfers, key)
		g.fileMu.Unlock()
	}()

	acceptEnv, err := NewEnvelope(MsgTypeFileAccept, "", accept)
	if err != nil {
		return FileGetResult{TransferID: transferID}, err
	}
	acceptMsg, err := acceptEnv.Marshal()
	if err != nil {
		return FileGetResult{TransferID: transferID}, err
	}
	if err := rec.Session.WriteText(waitCtx, acceptMsg); err != nil {
		return FileGetResult{TransferID: transferID}, err
	}

	var ack FileAckPayload
	select {
	case ack = <-t.done:
	case <-waitCtx.Done():
		return FileGetResult{TransferID: transferID}, waitCtx.Err()
	}

	status := strings.ToLower(strings.TrimSpace(ack.Status))
	if status != "completed" {
		reason := strings.TrimSpace(ack.Error)
		if reason == "" {
			reason = "transfer failed"
		}
		return FileGetResult{
			TransferID: transferID,
			SizeBytes:  ack.SizeBytes,
			SHA256:     strings.TrimSpace(ack.SHA256),
			Ack:        ack,
		}, errors.New(reason)
	}
	if strings.TrimSpace(ack.SavedPath) == "" {
		return FileGetResult{TransferID: transferID, Ack: ack}, errors.New("missing saved_path in file ack")
	}

	localPath := filepath.Join(filepath.Clean(g.files.RootDir), ack.SavedPath)
	return FileGetResult{
		TransferID: transferID,
		LocalPath:  localPath,
		SizeBytes:  ack.SizeBytes,
		SHA256:     strings.TrimSpace(ack.SHA256),
		Ack:        ack,
	}, nil
}
