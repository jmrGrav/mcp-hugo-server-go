package write

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
)

// maxIdempotencyKeyLen and idempotencyKeyPattern bound a caller-supplied
// idempotency_key (#888). The key is currently just a lookup key into an
// in-memory, client-scoped store (#627), so an unvalidated value is inert
// today — this is defense in depth: a caller-controlled string with
// path-traversal shapes, embedded whitespace, Unicode, or emoji has no
// legitimate place in a mutation-tool contract and is cheap to reject now,
// before the key could ever reach a filesystem path, log correlation ID, or
// external system in some future change. The charset mirrors how other
// opaque IDs in this codebase are generated (e.g. previewstore.NewID's
// hex-only IDs): alphanumeric plus '-' and '_'.
const maxIdempotencyKeyLen = 128

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// validateIdempotencyKey enforces the defensive charset/length bound on a
// caller-supplied idempotency_key (#888). A blank (empty or all-whitespace)
// key is accepted and means "idempotency not requested" — matching the
// existing `strings.TrimSpace(in.IdempotencyKey) != ""` gating every tool
// already uses to decide whether to consult the store — so this can be
// called unconditionally at handler entry without changing behavior for
// callers that omit the key. Any non-blank key must match the safe format.
func validateIdempotencyKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	if len(key) > maxIdempotencyKeyLen {
		return fmt.Errorf("invalid_params: idempotency_key exceeds %d characters (got %d)", maxIdempotencyKeyLen, len(key))
	}
	if !idempotencyKeyPattern.MatchString(key) {
		return fmt.Errorf("invalid_params: idempotency_key must contain only alphanumeric characters, '-', or '_'")
	}
	return nil
}

// defaultIdempotencyTTL is used when a caller constructs the write package's
// Register() with a config.Config whose IdempotencyTTLSeconds is unset or
// non-positive (e.g. a hand-built Config in a test, bypassing
// config.Load's own clamping). Derived from config.DefaultIdempotencyTTLSeconds
// (#616), single-sourcing the value rather than duplicating it, so this
// package never silently constructs a zero/negative-TTL store, which would
// defeat idempotency replay protection entirely.
const defaultIdempotencyTTL = time.Duration(config.DefaultIdempotencyTTLSeconds) * time.Second

// idempotencyTTLFromConfig resolves the configured idempotency-key retention
// window, falling back to defaultIdempotencyTTL for non-positive values
// (#616). The TTL is deliberately a server-level setting only — it is never
// accepted as a per-call tool parameter, since a caller-supplied TTL could
// be used to shorten the window and evade duplicate-submission protection.
func idempotencyTTLFromConfig(cfg config.Config) time.Duration {
	if cfg.IdempotencyTTLSeconds <= 0 {
		return defaultIdempotencyTTL
	}
	return time.Duration(cfg.IdempotencyTTLSeconds) * time.Second
}

// formatTTLDescription renders a TTL for agent-facing tool descriptions
// (get_mutation_status, #616) in whole minutes when it divides evenly —
// matching the "15 minutes" phrasing this text used before the TTL became
// configurable — and falls back to Duration's own String() (e.g. "90s",
// "1h30m0s") for values that don't land on a whole minute.
func formatTTLDescription(d time.Duration) string {
	if d > 0 && d%time.Minute == 0 {
		mins := d / time.Minute
		if mins == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", mins)
	}
	return d.String()
}

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
	persistent *db.DB
}

func newIdempotencyStore(ttl time.Duration, maxEntries int, persistent ...*db.DB) *idempotencyStore {
	var journal *db.DB
	if len(persistent) > 0 {
		journal = persistent[0]
	}
	return &idempotencyStore{
		ttl:        ttl,
		maxEntries: maxEntries,
		entries:    make(map[string]idempotencyEntry),
		persistent: journal,
	}
}

func (s *idempotencyStore) replay(callerKey, tool, key, requestHash string, out any) (bool, error) {
	if s == nil || key == "" {
		return false, nil
	}
	if s.persistent != nil {
		entry, err := s.persistent.LookupMutation(callerKey, tool, key, s.ttl)
		if err != nil {
			return false, err
		}
		if entry != nil {
			if entry.RequestHash != requestHash {
				return false, fmt.Errorf("idempotency_conflict: idempotency_key was already used for a different %s request", tool)
			}
			return true, json.Unmarshal(entry.ResultJSON, out)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	entry, ok := s.entries[s.cacheKey(callerKey, tool, key)]
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

func (s *idempotencyStore) remember(callerKey, tool, key, requestHash string, out any) error {
	if s == nil || key == "" {
		return nil
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if s.persistent != nil {
		if err := s.persistent.RememberMutation(db.MutationJournalEntry{CallerKey: callerKey, Tool: tool, Key: key, RequestHash: requestHash, ResultJSON: raw}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.pruneLocked(now)
	s.entries[s.cacheKey(callerKey, tool, key)] = idempotencyEntry{
		RequestHash: requestHash,
		ResultJSON:  raw,
		CreatedAt:   now,
	}
	s.trimLocked()
	return nil
}

// lookup returns the previously remembered result for callerKey+tool+key, if
// any, without requiring the caller to resupply (or hash-match) the original
// mutation payload — unlike replay, which is invoked from inside the
// mutating tool itself as part of re-attempting the same request. lookup
// backs get_mutation_status (#586): a read-only way to ask "did my last
// call actually land" after a timeout/ambiguous response, without knowing
// or resending the original arguments. Only ever contains successful
// results (remember is called on the success path only) — a miss here is
// not proof of failure, only "no confirmed success on record for this key,"
// which also covers still-in-flight, genuinely failed, expired (TTL), or
// never-attempted-with-this-key.
func (s *idempotencyStore) lookup(callerKey, tool, key string) (json.RawMessage, bool, error) {
	if s == nil || key == "" {
		return nil, false, nil
	}
	if s.persistent != nil {
		entry, err := s.persistent.LookupMutation(callerKey, tool, key, s.ttl)
		if err != nil {
			return nil, false, err
		}
		if entry != nil {
			return json.RawMessage(entry.ResultJSON), true, nil
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	entry, ok := s.entries[s.cacheKey(callerKey, tool, key)]
	if !ok {
		return nil, false, nil
	}
	return json.RawMessage(entry.ResultJSON), true, nil
}

// cacheKey namespaces every entry by callerKey (the requesting bearer
// token's hash, see oauth.CtxTokenID) ahead of tool+key, so idempotency-key
// state can never be read or replayed across two different callers (#627) —
// previously get_mutation_status's lookup path had no caller dimension at
// all, so any caller who guessed or was told another caller's tool+key could
// read that caller's mutation result.
func (s *idempotencyStore) cacheKey(callerKey, tool, key string) string {
	return callerKey + "\x00" + tool + "\x00" + key
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
