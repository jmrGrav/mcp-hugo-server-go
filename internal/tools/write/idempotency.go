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

// lookup returns the previously remembered result for tool+key, if any,
// without requiring the caller to resupply (or hash-match) the original
// mutation payload — unlike replay, which is invoked from inside the
// mutating tool itself as part of re-attempting the same request. lookup
// backs get_mutation_status (#586): a read-only way to ask "did my last
// call actually land" after a timeout/ambiguous response, without knowing
// or resending the original arguments. Only ever contains successful
// results (remember is called on the success path only) — a miss here is
// not proof of failure, only "no confirmed success on record for this key,"
// which also covers still-in-flight, genuinely failed, expired (TTL), or
// never-attempted-with-this-key.
func (s *idempotencyStore) lookup(tool, key string) (json.RawMessage, bool) {
	if s == nil || key == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	entry, ok := s.entries[s.cacheKey(tool, key)]
	if !ok {
		return nil, false
	}
	return json.RawMessage(entry.ResultJSON), true
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
