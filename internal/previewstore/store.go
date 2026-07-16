// Package previewstore holds the in-memory registry and HTTP handler for
// temporary, token-gated preview builds created by the create_preview tool
// (issue #345). It is deliberately separate from internal/tools/admin: the
// admin package owns *building* a preview (running Hugo into an isolated
// directory); this package owns *serving* it and enforcing its lifetime,
// so the same store can be shared between the tool-call code path and the
// plain-HTTP code path in internal/server without an import cycle.
package previewstore

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Entry is one active preview build.
type Entry struct {
	Dir         string
	Token       string
	ExpiresAt   time.Time
	BuildStatus string
}

// Store is an in-memory registry of active previews. It does not persist
// across process restarts — a restarted server simply has no previews,
// which is an acceptable MVP tradeoff for a short-TTL, disposable surface.
type Store struct {
	mu      sync.Mutex
	entries map[string]*Entry
}

func New() *Store {
	return &Store{entries: make(map[string]*Entry)}
}

// NewID returns a random, opaque, URL-safe identifier suitable for either a
// preview_id (non-secret, just needs to not be a raw PID) or a token
// (secret — the sole confidentiality boundary for preview content, so this
// always uses crypto/rand, never math/rand).
func NewID(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Put registers a new preview. Callers should generate id/token via NewID.
func (s *Store) Put(id string, entry *Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[id] = entry
}

// Get returns the entry for id if it exists, is not expired, and token
// matches (constant-time compare, since token is the confidentiality
// boundary for draft/unpublished content). An expired entry is removed and
// its directory deleted as a side effect, so expired previews are cleaned
// up lazily on next access rather than requiring a background sweeper.
func (s *Store) Get(id, token string) (*Entry, bool) {
	s.mu.Lock()
	entry, ok := s.entries[id]
	if !ok {
		s.mu.Unlock()
		return nil, false
	}
	if time.Now().After(entry.ExpiresAt) {
		delete(s.entries, id)
		s.mu.Unlock()
		_ = os.RemoveAll(entry.Dir)
		return nil, false
	}
	s.mu.Unlock()
	if subtle.ConstantTimeCompare([]byte(entry.Token), []byte(token)) != 1 {
		return nil, false
	}
	return entry, true
}

// Sweep removes every expired entry and deletes its directory. Called
// opportunistically from create_preview before registering a new entry, so
// storage doesn't accumulate indefinitely even if nobody ever re-visits an
// expired preview URL (which is what triggers cleanup in Get).
func (s *Store) Sweep() {
	now := time.Now()
	var expiredDirs []string
	s.mu.Lock()
	for id, entry := range s.entries {
		if now.After(entry.ExpiresAt) {
			expiredDirs = append(expiredDirs, entry.Dir)
			delete(s.entries, id)
		}
	}
	s.mu.Unlock()
	for _, dir := range expiredDirs {
		_ = os.RemoveAll(dir)
	}
}

// HTTPHandler serves preview content at /preview/{id}/{token}/{path...}.
// Every response carries X-Robots-Tag: noindex (acceptance criterion:
// preview URLs must be non-indexable). File serving goes through
// http.FileServer(http.Dir(...)) + http.StripPrefix so path traversal is
// handled by the standard library rather than manual path joining.
func (s *Store) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")

		rest := strings.TrimPrefix(r.URL.Path, "/preview/")
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			http.NotFound(w, r)
			return
		}
		id, token := parts[0], parts[1]

		entry, ok := s.Get(id, token)
		if !ok {
			http.Error(w, "preview not found or expired", http.StatusNotFound)
			return
		}

		prefix := "/preview/" + id + "/" + token
		http.StripPrefix(prefix, http.FileServer(http.Dir(entry.Dir))).ServeHTTP(w, r)
	})
}
