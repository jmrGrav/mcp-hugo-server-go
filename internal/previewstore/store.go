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
	"path"
	"strings"
	"sync"
	"time"
)

// Entry is one active preview build.
type Entry struct {
	Dir          string
	Token        string
	SessionToken string
	ExpiresAt    time.Time
	BuildStatus  string
	CreatedAt    time.Time
	Owner        string
}

type Snapshot struct {
	ID          string
	ExpiresAt   time.Time
	BuildStatus string
	CreatedAt   time.Time
	Owner       string
}

// Store is an in-memory registry of active previews. It does not persist
// across process restarts — a restarted server simply has no previews,
// which is an acceptable MVP tradeoff for a short-TTL, disposable surface.
type Store struct {
	mu      sync.Mutex
	entries map[string]*Entry
}

type LookupStatus string

const (
	LookupActive  LookupStatus = "active"
	LookupMissing LookupStatus = "missing"
	LookupExpired LookupStatus = "expired"
)

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
	if entry != nil && entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
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
	return s.GetByToken(id, token)
}

// Lookup returns the preview entry by id without validating any bearer or
// session token, along with a stable status distinguishing a missing preview
// from one that existed but has expired and was just cleaned up.
func (s *Store) Lookup(id string) (*Entry, LookupStatus) {
	s.mu.Lock()
	entry, ok := s.entries[id]
	if !ok {
		s.mu.Unlock()
		return nil, LookupMissing
	}
	if time.Now().After(entry.ExpiresAt) {
		delete(s.entries, id)
		s.mu.Unlock()
		_ = os.RemoveAll(entry.Dir)
		return nil, LookupExpired
	}
	s.mu.Unlock()
	return entry, LookupActive
}

// GetByToken returns the preview for id if token matches. Expired previews
// are deleted lazily on access.
func (s *Store) GetByToken(id, token string) (*Entry, bool) {
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

// GetBySession returns the preview for id if the session cookie matches.
func (s *Store) GetBySession(id, sessionToken string) (*Entry, bool) {
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
	// SessionToken is mutated by EstablishSession under the lock (unlike
	// Token, which is immutable after Put) — snapshot it here before
	// unlocking so the compare below never races EstablishSession's write.
	storedSessionToken := entry.SessionToken
	s.mu.Unlock()
	if subtle.ConstantTimeCompare([]byte(storedSessionToken), []byte(sessionToken)) != 1 {
		return nil, false
	}
	return entry, true
}

// EstablishSession validates the entry URL token and returns the corresponding
// preview entry plus a clean-path session cookie value.
func (s *Store) EstablishSession(id, token string) (*Entry, string, bool) {
	s.mu.Lock()
	entry, ok := s.entries[id]
	if !ok {
		s.mu.Unlock()
		return nil, "", false
	}
	if time.Now().After(entry.ExpiresAt) {
		delete(s.entries, id)
		s.mu.Unlock()
		_ = os.RemoveAll(entry.Dir)
		return nil, "", false
	}
	if subtle.ConstantTimeCompare([]byte(entry.Token), []byte(token)) != 1 {
		s.mu.Unlock()
		return nil, "", false
	}
	if entry.SessionToken == "" {
		sessionToken, err := NewID(24)
		if err != nil {
			s.mu.Unlock()
			return nil, "", false
		}
		entry.SessionToken = sessionToken
	}
	sessionToken := entry.SessionToken
	s.mu.Unlock()
	return entry, sessionToken, true
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

func (s *Store) List() []Snapshot {
	now := time.Now()
	var expiredDirs []string
	s.mu.Lock()
	var out []Snapshot
	for id, entry := range s.entries {
		if now.After(entry.ExpiresAt) {
			expiredDirs = append(expiredDirs, entry.Dir)
			delete(s.entries, id)
			continue
		}
		out = append(out, Snapshot{
			ID:          id,
			ExpiresAt:   entry.ExpiresAt,
			BuildStatus: entry.BuildStatus,
			CreatedAt:   entry.CreatedAt,
			Owner:       entry.Owner,
		})
	}
	s.mu.Unlock()
	for _, dir := range expiredDirs {
		_ = os.RemoveAll(dir)
	}
	return out
}

func (s *Store) Revoke(id string) bool {
	s.mu.Lock()
	entry, ok := s.entries[id]
	if ok {
		delete(s.entries, id)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	_ = os.RemoveAll(entry.Dir)
	return true
}

func (s *Store) RevokeAll() int {
	s.mu.Lock()
	entries := s.entries
	s.entries = make(map[string]*Entry)
	s.mu.Unlock()
	count := 0
	for _, entry := range entries {
		count++
		_ = os.RemoveAll(entry.Dir)
	}
	return count
}

func CookieName(id string) string {
	return "mcp_preview_" + id
}

func CleanPath(id, assetPath string) string {
	id = strings.TrimSpace(id)
	assetPath = strings.TrimPrefix(strings.TrimSpace(assetPath), "/")
	if assetPath == "" {
		return "/preview/" + id + "/"
	}
	return "/preview/" + id + "/" + path.Clean(assetPath)
}

// HTTPHandler serves preview content at either the signed entry URL
// /preview/{id}/{token}/{path...} or the clean session URL /preview/{id}/{path...}.
// The entry URL exists only to establish an HttpOnly cookie-backed session,
// then redirect the browser to the clean URL so subsequent navigated asset/link
// requests no longer carry the bearer secret in every path.
func (s *Store) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		w.Header().Set("Cache-Control", "private, no-store, max-age=0")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")

		rest := strings.TrimPrefix(r.URL.Path, "/preview/")
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) == 0 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}
		id := parts[0]
		if len(parts) >= 2 && parts[1] != "" {
			if entry, sessionToken, ok := s.EstablishSession(id, parts[1]); ok {
				http.SetCookie(w, &http.Cookie{
					Name:     CookieName(id),
					Value:    sessionToken,
					Path:     CleanPath(id, ""),
					HttpOnly: true,
					Secure:   true,
					SameSite: http.SameSiteStrictMode,
					Expires:  entry.ExpiresAt,
				})
				target := CleanPath(id, "")
				if len(parts) == 3 && parts[2] != "" {
					target = CleanPath(id, parts[2])
				}
				http.Redirect(w, r, target, http.StatusFound)
				return
			}
		}

		cookie, err := r.Cookie(CookieName(id))
		if err != nil || strings.TrimSpace(cookie.Value) == "" {
			http.Error(w, "preview not found or expired", http.StatusNotFound)
			return
		}
		entry, ok := s.GetBySession(id, cookie.Value)
		if !ok {
			http.Error(w, "preview not found or expired", http.StatusNotFound)
			return
		}

		prefix := "/preview/" + id
		http.StripPrefix(prefix, http.FileServer(http.Dir(entry.Dir))).ServeHTTP(w, r)
	})
}
