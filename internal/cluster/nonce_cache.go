package cluster

import (
	"strings"
	"sync"
	"time"
)

type NonceCache struct {
	mu      sync.Mutex
	entries map[string]time.Time
	ttl     time.Duration
	max     int

	lastSweep time.Time
}

func NewNonceCache(ttl time.Duration, max int) *NonceCache {
	if ttl <= 0 {
		ttl = DefaultNonceTTL
	}
	if max <= 0 {
		max = DefaultNonceMax
	}
	return &NonceCache{
		entries: make(map[string]time.Time),
		ttl:     ttl,
		max:     max,
	}
}

// Use records the nonce if it has not been seen recently.
// Returns true if accepted, false if the nonce was already used.
func (c *NonceCache) Use(nonce string, now time.Time) bool {
	if c == nil {
		return true
	}
	n := strings.TrimSpace(nonce)
	if n == "" {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.entries == nil {
		c.entries = make(map[string]time.Time)
	}

	if c.ttl <= 0 {
		c.ttl = DefaultNonceTTL
	}
	if c.max <= 0 {
		c.max = DefaultNonceMax
	}

	// Opportunistic sweep.
	if c.lastSweep.IsZero() || now.Sub(c.lastSweep) > c.ttl/2 {
		for k, exp := range c.entries {
			if !exp.IsZero() && now.After(exp) {
				delete(c.entries, k)
			}
		}
		c.lastSweep = now
	}

	if exp, ok := c.entries[n]; ok && (exp.IsZero() || now.Before(exp)) {
		return false
	}

	// If the map grows too large, drop expired entries; if still too large, drop everything.
	if len(c.entries) >= c.max {
		for k, exp := range c.entries {
			if !exp.IsZero() && now.After(exp) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= c.max {
			c.entries = make(map[string]time.Time)
		}
	}

	c.entries[n] = now.Add(c.ttl)
	return true
}

