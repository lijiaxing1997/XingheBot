package cluster

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type incomingFileTransfer struct {
	cfg FilesConfig

	transferID string
	peerID     string
	filename   string

	expectedSize int64

	rootDir     string
	finalRel    string
	finalAbs    string
	manifestAbs string
	tmpAbs      string

	mu         sync.Mutex
	f          *os.File
	hasher     hash.Hash
	bytesSeen  int64
	nextOffset int64
	closed     bool

	startedAt time.Time
	done      chan FileAckPayload
}

type fileManifest struct {
	TransferID string         `json:"transfer_id"`
	PeerID     string         `json:"peer_id"`
	Filename   string         `json:"filename"`
	SizeBytes  int64          `json:"size_bytes"`
	SHA256     string         `json:"sha256"`
	SavedPath  string         `json:"saved_path"`
	ReceivedAt time.Time      `json:"received_at"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

func normalizeFilesConfig(cfg FilesConfig) FilesConfig {
	out := cfg
	if strings.TrimSpace(out.RootDir) == "" {
		out.RootDir = ".cluster/files"
	}
	if out.MaxFileBytes <= 0 {
		out.MaxFileBytes = 2 << 30
	}
	if out.MaxTotalBytes <= 0 {
		out.MaxTotalBytes = 20 << 30
	}
	if out.RetentionDays <= 0 {
		out.RetentionDays = 7
	}
	if out.ChunkSizeBytes <= 0 {
		out.ChunkSizeBytes = 256 << 10
	}
	if out.MaxInflightChunks <= 0 {
		out.MaxInflightChunks = 8
	}
	return out
}

func ensureFileRootDirs(root string) error {
	r := strings.TrimSpace(root)
	if r == "" {
		return errors.New("files root dir is empty")
	}
	dirs := []string{
		r,
		filepath.Join(r, "inbox"),
		filepath.Join(r, "outbox"),
		filepath.Join(r, "tmp"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func sanitizePathSegment(raw string, fallback string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		s = fallback
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		return fallback
	}
	return out
}

func sanitizeFilename(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "file"
	}
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, string(filepath.Separator), "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	return name
}

func newIncomingFileTransfer(cfg FilesConfig, peerID string, offer FileOfferPayload) (*incomingFileTransfer, FileAcceptPayload, error) {
	cfg = normalizeFilesConfig(cfg)
	if strings.TrimSpace(offer.TransferID) == "" {
		return nil, FileAcceptPayload{}, errors.New("transfer_id is required")
	}
	if offer.SizeBytes < 0 {
		return nil, FileAcceptPayload{}, errors.New("size_bytes must be >= 0")
	}
	if offer.SizeBytes > cfg.MaxFileBytes {
		return nil, FileAcceptPayload{}, fmt.Errorf("file too large (size=%d max=%d)", offer.SizeBytes, cfg.MaxFileBytes)
	}
	root := filepath.Clean(strings.TrimSpace(cfg.RootDir))
	if err := ensureFileRootDirs(root); err != nil {
		return nil, FileAcceptPayload{}, err
	}

	peer := sanitizePathSegment(peerID, "peer")
	filename := sanitizeFilename(offer.Filename)
	date := time.Now().UTC().Format("2006-01-02")
	finalRel := filepath.Join("inbox", peer, date, fmt.Sprintf("%s__%s", strings.TrimSpace(offer.TransferID), filename))
	finalAbs := filepath.Join(root, finalRel)
	if err := os.MkdirAll(filepath.Dir(finalAbs), 0o755); err != nil {
		return nil, FileAcceptPayload{}, err
	}

	tmpAbs := filepath.Join(root, "tmp", fmt.Sprintf("%s.partial", strings.TrimSpace(offer.TransferID)))
	manifestAbs := filepath.Join(root, "inbox", peer, date, fmt.Sprintf("%s.manifest.json", strings.TrimSpace(offer.TransferID)))

	f, err := os.OpenFile(tmpAbs, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, FileAcceptPayload{}, err
	}

	t := &incomingFileTransfer{
		cfg:          cfg,
		transferID:   strings.TrimSpace(offer.TransferID),
		peerID:       peer,
		filename:     filename,
		expectedSize: offer.SizeBytes,
		rootDir:      root,
		finalRel:     finalRel,
		finalAbs:     finalAbs,
		manifestAbs:  manifestAbs,
		tmpAbs:       tmpAbs,
		f:            f,
		hasher:       sha256.New(),
		startedAt:    time.Now().UTC(),
		done:         make(chan FileAckPayload, 1),
	}

	accept := FileAcceptPayload{
		TransferID:        t.transferID,
		ChunkSizeBytes:    cfg.ChunkSizeBytes,
		MaxInflightChunks: cfg.MaxInflightChunks,
		SaveHint:          finalRel,
	}
	return t, accept, nil
}

func (t *incomingFileTransfer) handleChunk(header FileChunkHeader, chunk []byte) error {
	if t == nil {
		return errors.New("nil transfer")
	}
	if strings.TrimSpace(header.TransferID) == "" || strings.TrimSpace(header.TransferID) != t.transferID {
		return errors.New("chunk transfer_id mismatch")
	}
	if header.Len != 0 && header.Len != len(chunk) {
		return fmt.Errorf("chunk length mismatch (header=%d got=%d)", header.Len, len(chunk))
	}
	if header.Offset != t.nextOffset {
		return fmt.Errorf("unexpected chunk offset (got=%d expected=%d)", header.Offset, t.nextOffset)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.f == nil {
		return errors.New("transfer is closed")
	}

	if _, err := t.f.Write(chunk); err != nil {
		return err
	}
	_, _ = t.hasher.Write(chunk)
	t.bytesSeen += int64(len(chunk))
	t.nextOffset += int64(len(chunk))
	return nil
}

func (t *incomingFileTransfer) finalize(complete FileCompletePayload, metadata map[string]any) (FileAckPayload, error) {
	if t == nil {
		return FileAckPayload{}, errors.New("nil transfer")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return FileAckPayload{}, errors.New("transfer already closed")
	}
	t.closed = true

	if t.f != nil {
		_ = t.f.Close()
		t.f = nil
	}

	if complete.SizeBytes > 0 && complete.SizeBytes != t.expectedSize {
		_ = os.Remove(t.tmpAbs)
		return FileAckPayload{
			TransferID: t.transferID,
			Status:     "failed",
			SizeBytes:  t.expectedSize,
			Error:      fmt.Sprintf("size mismatch (offer=%d complete=%d)", t.expectedSize, complete.SizeBytes),
		}, errors.New("size mismatch")
	}
	if t.bytesSeen != t.expectedSize {
		_ = os.Remove(t.tmpAbs)
		return FileAckPayload{
			TransferID:    t.transferID,
			Status:        "failed",
			SizeBytes:     t.expectedSize,
			BytesReceived: t.bytesSeen,
			Error:         fmt.Sprintf("incomplete transfer (received=%d expected=%d)", t.bytesSeen, t.expectedSize),
		}, errors.New("incomplete transfer")
	}

	sumHex := hex.EncodeToString(t.hasher.Sum(nil))
	want := strings.TrimSpace(complete.SHA256)
	if want != "" && !strings.EqualFold(want, sumHex) {
		_ = os.Remove(t.tmpAbs)
		return FileAckPayload{
			TransferID:    t.transferID,
			Status:        "failed",
			SizeBytes:     t.expectedSize,
			BytesReceived: t.bytesSeen,
			SHA256:        sumHex,
			Error:         fmt.Sprintf("sha256 mismatch (got=%s want=%s)", sumHex, want),
		}, errors.New("sha256 mismatch")
	}

	if err := os.Rename(t.tmpAbs, t.finalAbs); err != nil {
		_ = os.Remove(t.tmpAbs)
		return FileAckPayload{TransferID: t.transferID, Status: "failed", Error: err.Error()}, err
	}

	manifest := fileManifest{
		TransferID: t.transferID,
		PeerID:     t.peerID,
		Filename:   t.filename,
		SizeBytes:  t.expectedSize,
		SHA256:     sumHex,
		SavedPath:  t.finalRel,
		ReceivedAt: time.Now().UTC(),
		Metadata:   metadata,
	}
	_ = writeJSONAtomic(t.manifestAbs, manifest)

	ack := FileAckPayload{
		TransferID:    t.transferID,
		Status:        "completed",
		SizeBytes:     t.expectedSize,
		BytesReceived: t.bytesSeen,
		SavedPath:     t.finalRel,
		SHA256:        sumHex,
	}
	return ack, nil
}

func (t *incomingFileTransfer) fail(err error) FileAckPayload {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return FileAckPayload{TransferID: t.transferID, Status: "failed", Error: "already closed"}
	}
	t.closed = true
	if t.f != nil {
		_ = t.f.Close()
		t.f = nil
	}
	_ = os.Remove(t.tmpAbs)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return FileAckPayload{
		TransferID:    t.transferID,
		Status:        "failed",
		SizeBytes:     t.expectedSize,
		BytesReceived: t.bytesSeen,
		Error:         msg,
	}
}

func (t *incomingFileTransfer) signalDone(ack FileAckPayload) {
	if t == nil || t.done == nil {
		return
	}
	select {
	case t.done <- ack:
	default:
	}
}

func writeJSONAtomic(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := fmt.Sprintf("%s.tmp.%d", path, time.Now().UTC().UnixNano())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func safeJoin(root string, rel string) (string, error) {
	r := filepath.Clean(strings.TrimSpace(root))
	p := strings.TrimSpace(rel)
	if r == "" {
		return "", errors.New("root is empty")
	}
	if p == "" {
		return "", errors.New("path is empty")
	}
	if filepath.IsAbs(p) {
		return "", errors.New("path must be relative")
	}
	if strings.Contains(p, ":") {
		return "", errors.New("path must not contain ':'")
	}
	clean := filepath.Clean(p)
	if clean == "." {
		return "", errors.New("path is invalid")
	}
	if strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", errors.New("path must not escape root")
	}
	absRoot, err := filepath.Abs(r)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(absRoot, clean)
	absClean, err := filepath.Abs(abs)
	if err != nil {
		return "", err
	}
	sep := string(filepath.Separator)
	if !strings.HasPrefix(absClean, absRoot+sep) && absClean != absRoot {
		return "", errors.New("path must stay within root")
	}
	return absClean, nil
}

func parseChunkBinaryFrame(frame []byte) (FileChunkHeader, []byte, error) {
	i := bytesIndexByte(frame, '\n')
	if i <= 0 {
		return FileChunkHeader{}, nil, errors.New("invalid chunk frame (missing header)")
	}
	headerRaw := frame[:i]
	chunk := frame[i+1:]
	var hdr FileChunkHeader
	if err := json.Unmarshal(headerRaw, &hdr); err != nil {
		return FileChunkHeader{}, nil, fmt.Errorf("parse chunk header: %w", err)
	}
	hdr.TransferID = strings.TrimSpace(hdr.TransferID)
	return hdr, chunk, nil
}

func bytesIndexByte(b []byte, c byte) int {
	for i := 0; i < len(b); i++ {
		if b[i] == c {
			return i
		}
	}
	return -1
}
