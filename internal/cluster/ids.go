package cluster

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

func NewID(prefix string) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	p := strings.TrimSpace(prefix)
	if p == "" {
		return id
	}
	return p + "-" + id
}

