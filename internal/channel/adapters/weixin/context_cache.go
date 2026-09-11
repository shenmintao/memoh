package weixin

import (
	"strings"
	"sync"
	"time"
)

// contextTokenCache stores the latest context_token per target user.
// Tokens follow Tencent's per-account persistence model. A zero TTL retains
// them until replaced; the platform decides whether a saved token remains valid.
type contextTokenCache struct {
	mu    sync.RWMutex
	items map[string]contextTokenEntry
	ttl   time.Duration
}

type contextTokenEntry struct {
	Token     string
	CreatedAt time.Time
}

func newContextTokenCache(ttl time.Duration) *contextTokenCache {
	return &contextTokenCache{
		items: make(map[string]contextTokenEntry),
		ttl:   ttl,
	}
}

func (c *contextTokenCache) Put(target string, token string) {
	key := strings.TrimSpace(target)
	if key == "" || strings.TrimSpace(token) == "" {
		return
	}
	c.mu.Lock()
	c.items[key] = contextTokenEntry{
		Token:     token,
		CreatedAt: time.Now().UTC(),
	}
	c.gcLocked()
	c.mu.Unlock()
}

func (c *contextTokenCache) Get(target string) (string, bool) {
	key := strings.TrimSpace(target)
	if key == "" {
		return "", false
	}
	c.mu.RLock()
	entry, ok := c.items[key]
	c.mu.RUnlock()
	if !ok {
		return "", false
	}
	if c.ttl > 0 && time.Since(entry.CreatedAt) > c.ttl {
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		return "", false
	}
	return entry.Token, true
}

func (c *contextTokenCache) gcLocked() {
	if len(c.items) < 512 {
		return
	}
	now := time.Now().UTC()
	for key, entry := range c.items {
		if c.ttl > 0 && now.Sub(entry.CreatedAt) > c.ttl {
			delete(c.items, key)
		}
	}
}
