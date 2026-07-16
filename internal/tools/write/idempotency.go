package write

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type idempotencyEntry struct {
	RequestHash string
	ResultJSON  []byte
	CreatedAt   time.Time
}

type idempotencyStore struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	entries    map[string]idempotencyEntry
}

func newIdempotencyStore(ttl time.Duration, maxEntries int) *idempotencyStore {
	return &idempotencyStore{
		ttl:        ttl,
		maxEntries: maxEntries,
		entries:    make(map[string]idempotencyEntry),
	}
}

func (s *idempotencyStore) replay(tool, key, requestHash string, out any) (bool, error) {
	if s == nil || key == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	entry, ok := s.entries[s.cacheKey(tool, key)]
	if !ok {
		return false, nil
	}
	if entry.RequestHash != requestHash {
		return false, fmt.Errorf("idempotency_conflict: idempotency_key was already used for a different %s request", tool)
	}
	if err := json.Unmarshal(entry.ResultJSON, out); err != nil {
		return false, err
	}
	return true, nil
}

func (s *idempotencyStore) remember(tool, key, requestHash string, out any) error {
	if s == nil || key == "" {
		return nil
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.pruneLocked(now)
	s.entries[s.cacheKey(tool, key)] = idempotencyEntry{
		RequestHash: requestHash,
		ResultJSON:  raw,
		CreatedAt:   now,
	}
	s.trimLocked()
	return nil
}

func (s *idempotencyStore) cacheKey(tool, key string) string {
	return tool + "\x00" + key
}

func (s *idempotencyStore) pruneLocked(now time.Time) {
	if s.ttl <= 0 {
		return
	}
	for key, entry := range s.entries {
		if now.Sub(entry.CreatedAt) > s.ttl {
			delete(s.entries, key)
		}
	}
}

func (s *idempotencyStore) trimLocked() {
	if s.maxEntries <= 0 || len(s.entries) <= s.maxEntries {
		return
	}
	for len(s.entries) > s.maxEntries {
		var oldestKey string
		var oldest time.Time
		first := true
		for key, entry := range s.entries {
			if first || entry.CreatedAt.Before(oldest) {
				oldestKey = key
				oldest = entry.CreatedAt
				first = false
			}
		}
		delete(s.entries, oldestKey)
	}
}

func requestHash(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
