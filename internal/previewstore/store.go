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
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
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
	// Recovered marks metadata restored after restart. No bearer/session secret
	// was restored, so browser access requires creating a new preview.
	Recovered bool
	// sessionActivated records that the session cookie minted from this
	// entry's single-use Token has since been used to fetch actual preview
	// content (a successful GetBySession), not merely exchanged on the
	// redirect. It is the pivot for single-use enforcement (#871): the entry
	// Token is honoured for re-exchange up until activation, then refused.
	// Mutated only under Store.mu (like SessionToken, unlike the immutable
	// Token) — never read or written without the lock held.
	sessionActivated bool
}

type Snapshot struct {
	ID           string
	ExpiresAt    time.Time
	BuildStatus  string
	CreatedAt    time.Time
	Owner        string
	AccessStatus string
}

// Store keeps active preview secrets in memory and may persist only non-secret
// lease metadata. Entry and session tokens are deliberately restart-volatile.
type Store struct {
	mu      sync.Mutex
	entries map[string]*Entry
	stateDB *db.DB
	root    string
}

type ReconcileReport struct {
	Restored       int
	Expired        int
	Missing        int
	Unsafe         int
	OrphansRemoved int
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

// NewDir allocates an isolated preview directory under the store's managed
// root. Persistent stores use a dedicated root so restart reconciliation can
// never sweep another process's unrelated os.TempDir entries.
func (s *Store) NewDir() (string, error) {
	root := s.root
	if root == "" {
		root = os.TempDir()
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(root, "mcp-preview-*")
}

func (s *Store) ManagedRoot() string {
	if s.root != "" {
		return s.root
	}
	return os.TempDir()
}

// NewPersistent restores non-secret lease metadata from SQLite and reconciles
// it with the preview root. Existing bearer/cookie credentials are never
// restored, so old preview URLs are invalid after a restart even while their
// owner can still list and revoke the lease.
func NewPersistent(stateDB *db.DB, root string) (*Store, ReconcileReport, error) {
	s := New()
	if stateDB == nil {
		return s, ReconcileReport{}, nil
	}
	root = filepath.Clean(root)
	if root == "" || !filepath.IsAbs(root) {
		return nil, ReconcileReport{}, fmt.Errorf("preview store: root must be absolute")
	}
	s.stateDB = stateDB
	s.root = root
	report, err := s.reconcile()
	if err != nil {
		return nil, report, err
	}
	return s, report, nil
}

func safeDirName(name string) bool {
	return name == filepath.Base(name) && strings.HasPrefix(name, "mcp-preview-") && name != "mcp-preview-"
}

func (s *Store) persistedLease(id string, entry *Entry) (db.PreviewLease, error) {
	if s.stateDB == nil {
		return db.PreviewLease{}, nil
	}
	if entry == nil || strings.TrimSpace(id) == "" {
		return db.PreviewLease{}, fmt.Errorf("preview store: nil entry or empty id")
	}
	dir, err := filepath.Abs(entry.Dir)
	if err != nil {
		return db.PreviewLease{}, err
	}
	root := filepath.Clean(s.root)
	if filepath.Dir(dir) != root || !safeDirName(filepath.Base(dir)) {
		return db.PreviewLease{}, fmt.Errorf("preview store: directory is outside the managed preview root")
	}
	return db.PreviewLease{
		ID: id, Owner: entry.Owner, DirName: filepath.Base(dir),
		BuildStatus: entry.BuildStatus, State: "active",
		CreatedAt: entry.CreatedAt, ExpiresAt: entry.ExpiresAt,
	}, nil
}

func (s *Store) reconcile() (ReconcileReport, error) {
	var report ReconcileReport
	leases, err := s.stateDB.ListPreviewLeases()
	if err != nil {
		return report, fmt.Errorf("preview store: list leases: %w", err)
	}
	now := time.Now()
	active := make(map[string]struct{})
	for _, lease := range leases {
		if !safeDirName(lease.DirName) || strings.TrimSpace(lease.ID) == "" {
			report.Unsafe++
			if err := s.stateDB.DeletePreviewLease(lease.ID); err != nil {
				return report, err
			}
			continue
		}
		dir := filepath.Join(s.root, lease.DirName)
		if !lease.ExpiresAt.After(now) {
			report.Expired++
			if err := s.stateDB.DeletePreviewLease(lease.ID); err != nil {
				return report, err
			}
			_ = os.RemoveAll(dir)
			continue
		}
		info, statErr := os.Lstat(dir)
		if statErr != nil || !info.IsDir() {
			report.Missing++
			if err := s.stateDB.DeletePreviewLease(lease.ID); err != nil {
				return report, err
			}
			continue
		}
		s.entries[lease.ID] = &Entry{
			Dir: dir, ExpiresAt: lease.ExpiresAt, BuildStatus: lease.BuildStatus,
			CreatedAt: lease.CreatedAt, Owner: lease.Owner, Recovered: true,
		}
		active[lease.DirName] = struct{}{}
		report.Restored++
	}
	entries, err := os.ReadDir(s.root)
	if err != nil && !os.IsNotExist(err) {
		return report, fmt.Errorf("preview store: scan root: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !safeDirName(entry.Name()) {
			continue
		}
		if _, ok := active[entry.Name()]; ok {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.root, entry.Name())); err != nil {
			return report, fmt.Errorf("preview store: remove orphan %q: %w", entry.Name(), err)
		}
		report.OrphansRemoved++
	}
	return report, nil
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
func (s *Store) Put(id string, entry *Entry) error {
	if entry != nil && entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	if s.stateDB != nil {
		lease, err := s.persistedLease(id, entry)
		if err != nil {
			return err
		}
		if err := s.stateDB.PutPreviewLease(lease); err != nil {
			return fmt.Errorf("preview store: persist lease: %w", err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[id] = entry
	return nil
}

func (s *Store) deleteLease(id string) error {
	if s.stateDB == nil {
		return nil
	}
	return s.stateDB.DeletePreviewLease(id)
}

func (s *Store) removeExpiredLocked(id string, entry *Entry) bool {
	if err := s.deleteLease(id); err != nil {
		slog.Error("preview lease expiry persistence failed", "preview_id", id, "error", err)
		return false
	}
	delete(s.entries, id)
	return true
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
		removed := s.removeExpiredLocked(id, entry)
		s.mu.Unlock()
		if removed {
			_ = os.RemoveAll(entry.Dir)
		}
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
		removed := s.removeExpiredLocked(id, entry)
		s.mu.Unlock()
		if removed {
			_ = os.RemoveAll(entry.Dir)
		}
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
		removed := s.removeExpiredLocked(id, entry)
		s.mu.Unlock()
		if removed {
			_ = os.RemoveAll(entry.Dir)
		}
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
	// A session cookie has now successfully fetched real content: the
	// entry->session handoff is provably complete, so the single-use entry
	// Token may be retired (see EstablishSession). Re-lock to flip the flag —
	// sessionActivated is shared mutable state guarded by s.mu. The entry
	// pointer may have been deleted from the map by a concurrent expiry/revoke
	// between unlock and here; writing the field on the (still-live) pointer is
	// harmless in that case.
	s.mu.Lock()
	entry.sessionActivated = true
	s.mu.Unlock()
	return entry, true
}

// EstablishSession validates the entry URL token and returns the corresponding
// preview entry plus a clean-path session cookie value.
//
// Single-use semantics (#871): the entry Token is not retired on the first
// exchange — it stays valid for *re-exchange* only until the session it minted
// is confirmed in active use (sessionActivated, flipped by GetBySession on the
// first content fetch). This deliberately keeps the legitimate re-exchange
// cases working, all of which happen *before* any content has been fetched:
//   - page reload / network retry of the entry request before the redirect's
//     Set-Cookie ever reached the browser (so the client has no cookie yet and
//     must re-exchange the token),
//   - opening the same entry URL in a second tab before the first completes.
//
// Each of those re-exchanges returns the *same* SessionToken, so they are
// idempotent. Once the browser has followed the redirect and fetched real
// content with the cookie, the token is no longer needed and any further
// exchange attempt is refused — a captured/leaked token can no longer mint a
// fresh, independent session. The security value is honest: this converts
// *silent* reuse of a leaked entry token into a *detectable* lockout (a
// legitimate user who is unexpectedly refused is a signal the link leaked); it
// does not protect against a token exfiltrated before the real flow completes.
func (s *Store) EstablishSession(id, token string) (*Entry, string, bool) {
	s.mu.Lock()
	entry, ok := s.entries[id]
	if !ok {
		s.mu.Unlock()
		return nil, "", false
	}
	if time.Now().After(entry.ExpiresAt) {
		removed := s.removeExpiredLocked(id, entry)
		s.mu.Unlock()
		if removed {
			_ = os.RemoveAll(entry.Dir)
		}
		return nil, "", false
	}
	if subtle.ConstantTimeCompare([]byte(entry.Token), []byte(token)) != 1 {
		s.mu.Unlock()
		return nil, "", false
	}
	// Token is valid, but if its session is already in active use the token is
	// spent: refuse to mint or hand back a session from it again.
	if entry.SessionToken != "" && entry.sessionActivated {
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

// ResetSessionForProbe clears the cookie session used by create_preview's
// optional external reachability probe. create_preview calls this only before
// returning the entry URL to its caller, so the real browser can subsequently
// exchange the still-secret entry token for a fresh session. It deliberately
// does not change the entry token, expiry, owner, or built files.
func (s *Store) ResetSessionForProbe(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[id]
	if !ok || entry == nil {
		return false
	}
	entry.SessionToken = ""
	entry.sessionActivated = false
	return true
}

// Sweep removes every expired entry and deletes its directory. Called
// opportunistically from create_preview before registering a new entry, so
// storage doesn't accumulate indefinitely even if nobody ever re-visits an
// expired preview URL (which is what triggers cleanup in Get).
func (s *Store) Sweep() {
	if err := s.SweepPersistent(); err != nil {
		slog.Error("preview lease sweep failed", "error", err)
	}
}

func (s *Store) SweepPersistent() error {
	now := time.Now()
	var expiredDirs []string
	s.mu.Lock()
	for id, entry := range s.entries {
		if now.After(entry.ExpiresAt) {
			if err := s.deleteLease(id); err != nil {
				s.mu.Unlock()
				return fmt.Errorf("preview store: expire %q: %w", id, err)
			}
			expiredDirs = append(expiredDirs, entry.Dir)
			delete(s.entries, id)
		}
	}
	s.mu.Unlock()
	for _, dir := range expiredDirs {
		_ = os.RemoveAll(dir)
	}
	return nil
}

func (s *Store) List() []Snapshot {
	now := time.Now()
	var expiredDirs []string
	s.mu.Lock()
	var out []Snapshot
	for id, entry := range s.entries {
		if now.After(entry.ExpiresAt) {
			if err := s.deleteLease(id); err == nil {
				expiredDirs = append(expiredDirs, entry.Dir)
				delete(s.entries, id)
			} else {
				slog.Error("preview lease list expiry failed", "preview_id", id, "error", err)
			}
			continue
		}
		out = append(out, Snapshot{
			ID:           id,
			ExpiresAt:    entry.ExpiresAt,
			BuildStatus:  entry.BuildStatus,
			CreatedAt:    entry.CreatedAt,
			Owner:        entry.Owner,
			AccessStatus: previewAccessStatus(entry),
		})
	}
	s.mu.Unlock()
	for _, dir := range expiredDirs {
		_ = os.RemoveAll(dir)
	}
	return out
}

func (s *Store) ListOwned(owner string) []Snapshot {
	if owner == "" {
		return s.List()
	}
	now := time.Now()
	var expiredDirs []string
	s.mu.Lock()
	var out []Snapshot
	for id, entry := range s.entries {
		if now.After(entry.ExpiresAt) {
			if err := s.deleteLease(id); err == nil {
				expiredDirs = append(expiredDirs, entry.Dir)
				delete(s.entries, id)
			} else {
				slog.Error("preview lease list expiry failed", "preview_id", id, "error", err)
			}
			continue
		}
		if entry.Owner != owner {
			continue
		}
		out = append(out, Snapshot{
			ID:           id,
			ExpiresAt:    entry.ExpiresAt,
			BuildStatus:  entry.BuildStatus,
			CreatedAt:    entry.CreatedAt,
			Owner:        entry.Owner,
			AccessStatus: previewAccessStatus(entry),
		})
	}
	s.mu.Unlock()
	for _, dir := range expiredDirs {
		_ = os.RemoveAll(dir)
	}
	return out
}

func previewAccessStatus(entry *Entry) string {
	if entry != nil && entry.Recovered {
		return "restart_invalidated"
	}
	return "active"
}

// CountByOwner returns how many non-expired previews are attributed to owner.
// Used by create_preview to enforce a per-caller active-preview cap (#871).
// Callers should Sweep() first so expired entries don't inflate the count.
func (s *Store) CountByOwner(owner string) int {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, entry := range s.entries {
		if now.After(entry.ExpiresAt) {
			continue
		}
		if entry.Owner == owner {
			n++
		}
	}
	return n
}

// DiskUsageBytes returns the total on-disk size of every non-expired preview's
// build directory. Used by create_preview to enforce a global preview-disk cap
// (#871). The walk is best-effort: entries can be expired/revoked and their
// temp dirs removed concurrently, so per-file/per-dir errors (including a whole
// directory vanishing mid-walk) are ignored rather than failing the caller —
// an undercount is the safe direction for a create that is about to add more.
func (s *Store) DiskUsageBytes() int64 {
	now := time.Now()
	s.mu.Lock()
	dirs := make([]string, 0, len(s.entries))
	for _, entry := range s.entries {
		if now.After(entry.ExpiresAt) {
			continue
		}
		dirs = append(dirs, entry.Dir)
	}
	s.mu.Unlock()

	var total int64
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip vanished/unreadable entries, keep walking
			}
			if !info.IsDir() {
				total += info.Size()
			}
			return nil
		})
	}
	return total
}

// ActiveDirs returns the on-disk directories of every currently-tracked,
// non-expired preview, as a set. It is used by get_storage_health (#861) to
// distinguish a live preview's directory from expired/orphaned mcp-preview-*
// residue left on disk (including directories without durable lease metadata).
// Unlike List/Sweep this is a pure
// read: it never deletes an expired entry or its directory, so an advisory
// health check can't itself mutate the state it is reporting on.
func (s *Store) ActiveDirs() map[string]struct{} {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	dirs := make(map[string]struct{}, len(s.entries))
	for _, entry := range s.entries {
		if entry == nil || entry.Dir == "" {
			continue
		}
		if now.After(entry.ExpiresAt) {
			continue
		}
		dirs[entry.Dir] = struct{}{}
	}
	return dirs
}

func (s *Store) Revoke(id string) bool {
	ok, err := s.RevokeOwnedPersistent(id, "")
	if err != nil {
		slog.Error("preview lease revoke failed", "preview_id", id, "error", err)
		return false
	}
	return ok
}

func (s *Store) RevokeOwnedPersistent(id, owner string) (bool, error) {
	s.mu.Lock()
	entry, ok := s.entries[id]
	if ok && owner != "" && entry.Owner != owner {
		ok = false
	}
	if ok {
		if err := s.deleteLease(id); err != nil {
			s.mu.Unlock()
			return false, fmt.Errorf("preview store: revoke lease: %w", err)
		}
		delete(s.entries, id)
	}
	s.mu.Unlock()
	if !ok {
		return false, nil
	}
	_ = os.RemoveAll(entry.Dir)
	return true, nil
}

func (s *Store) RevokeOwned(id, owner string) bool {
	ok, err := s.RevokeOwnedPersistent(id, owner)
	if err != nil {
		slog.Error("preview lease revoke failed", "preview_id", id, "error", err)
		return false
	}
	return ok
}

func (s *Store) RevokeAll() int {
	count, err := s.RevokeAllOwnedPersistent("")
	if err != nil {
		slog.Error("preview lease bulk revoke failed", "error", err)
		return 0
	}
	return count
}

func (s *Store) RevokeAllOwnedPersistent(owner string) (int, error) {
	s.mu.Lock()
	entries := make(map[string]*Entry)
	for id, entry := range s.entries {
		if owner != "" && (entry == nil || entry.Owner != owner) {
			continue
		}
		entries[id] = entry
	}
	if err := func() error {
		if s.stateDB == nil {
			return nil
		}
		return s.stateDB.DeletePreviewLeasesByOwner(owner)
	}(); err != nil {
		s.mu.Unlock()
		return 0, fmt.Errorf("preview store: bulk revoke leases: %w", err)
	}
	for id := range entries {
		delete(s.entries, id)
	}
	s.mu.Unlock()
	count := 0
	for _, entry := range entries {
		count++
		_ = os.RemoveAll(entry.Dir)
	}
	return count, nil
}

func (s *Store) RevokeAllOwned(owner string) int {
	count, err := s.RevokeAllOwnedPersistent(owner)
	if err != nil {
		slog.Error("preview lease bulk revoke failed", "owner", owner, "error", err)
		return 0
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
	directoryRoute := strings.HasSuffix(assetPath, "/")
	cleaned := path.Clean(assetPath)
	if directoryRoute && cleaned != "." {
		cleaned += "/"
	}
	return "/preview/" + id + "/" + cleaned
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
		var sessionEntry *Entry
		var sessionCookie *http.Cookie
		if c, err := r.Cookie(CookieName(id)); err == nil && strings.TrimSpace(c.Value) != "" {
			if entry, ok := s.GetBySession(id, c.Value); ok {
				sessionEntry = entry
				sessionCookie = c
			}
		}

		if sessionEntry != nil {
			// A clean nested path such as /preview/{id}/en/posts/... must be
			// served verbatim. Only treat the first segment as an entry token
			// when it exactly matches this preview's token; the previous
			// "try every segment as a token" flow dropped `en`/`posts` from
			// valid cookie-backed routes and could trigger an ingress homepage
			// fallback (#978).
			if len(parts) >= 2 && parts[1] != "" && subtle.ConstantTimeCompare([]byte(sessionEntry.Token), []byte(parts[1])) == 1 {
				target := CleanPath(id, "")
				if len(parts) == 3 && parts[2] != "" {
					target = CleanPath(id, parts[2])
				}
				http.Redirect(w, r, target, http.StatusFound)
				return
			}
		} else if len(parts) >= 2 && parts[1] != "" {
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

		if sessionEntry == nil || sessionCookie == nil {
			http.Error(w, "preview not found or expired", http.StatusNotFound)
			return
		}

		prefix := "/preview/" + id
		http.StripPrefix(prefix, http.FileServer(http.Dir(sessionEntry.Dir))).ServeHTTP(w, r)
	})
}
