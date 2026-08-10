package previewstore_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/previewstore"
)

func writePreviewFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestStoreServesValidPreview(t *testing.T) {
	dir := t.TempDir()
	writePreviewFile(t, dir, "index.html", "<html>hello preview</html>")

	s := previewstore.New()
	s.Put("abc123", &previewstore.Entry{
		Dir:         dir,
		Token:       "secret-token",
		ExpiresAt:   time.Now().Add(time.Hour),
		BuildStatus: "passed",
	})

	req := httptest.NewRequest(http.MethodGet, "/preview/abc123/secret-token/", nil)
	rec := httptest.NewRecorder()
	s.HTTPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("entry URL status = %d, want redirect", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/preview/abc123/" {
		t.Fatalf("redirect Location = %q, want /preview/abc123/", got)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v, want exactly one session cookie", cookies)
	}

	req = httptest.NewRequest(http.MethodGet, "/preview/abc123/", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	s.HTTPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("clean URL status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello preview") {
		t.Fatalf("body = %q, missing expected content", rec.Body.String())
	}
	if got := rec.Header().Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
		t.Fatalf("X-Robots-Tag = %q, want noindex", got)
	}
}

// TestStoreServesSecurityHeaders is a regression test for #831: the preview
// URL's token is the sole access control, so responses must not be
// cacheable by shared/intermediate caches and must resist being framed or
// leaking the URL via Referer, in addition to the pre-existing
// X-Robots-Tag noindex check above.
func TestStoreServesSecurityHeaders(t *testing.T) {
	dir := t.TempDir()
	writePreviewFile(t, dir, "index.html", "<html>hello preview</html>")

	s := previewstore.New()
	s.Put("abc123", &previewstore.Entry{
		Dir:         dir,
		Token:       "secret-token",
		ExpiresAt:   time.Now().Add(time.Hour),
		BuildStatus: "passed",
	})

	req := httptest.NewRequest(http.MethodGet, "/preview/abc123/secret-token/", nil)
	rec := httptest.NewRecorder()
	s.HTTPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want redirect", rec.Code)
	}
	cases := map[string]string{
		"Cache-Control":           "no-store",
		"Referrer-Policy":         "no-referrer",
		"X-Frame-Options":         "DENY",
		"Content-Security-Policy": "frame-ancestors 'none'",
	}
	for header, want := range cases {
		if got := rec.Header().Get(header); !strings.Contains(got, want) {
			t.Fatalf("%s = %q, want to contain %q", header, got, want)
		}
	}
}

func TestStoreRejectsWrongToken(t *testing.T) {
	dir := t.TempDir()
	writePreviewFile(t, dir, "index.html", "secret content")

	s := previewstore.New()
	s.Put("abc123", &previewstore.Entry{
		Dir:       dir,
		Token:     "correct-token",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	req := httptest.NewRequest(http.MethodGet, "/preview/abc123/wrong-token/index.html", nil)
	rec := httptest.NewRecorder()
	s.HTTPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for wrong token", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret content") {
		t.Fatalf("wrong token must not leak preview content: %s", rec.Body.String())
	}
}

func TestStoreRejectsExpiredPreviewAndCleansUpDir(t *testing.T) {
	dir := t.TempDir()
	writePreviewFile(t, dir, "index.html", "expired content")

	s := previewstore.New()
	s.Put("abc123", &previewstore.Entry{
		Dir:       dir,
		Token:     "tok",
		ExpiresAt: time.Now().Add(-time.Minute), // already expired
	})

	req := httptest.NewRequest(http.MethodGet, "/preview/abc123/tok/index.html", nil)
	rec := httptest.NewRecorder()
	s.HTTPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for expired preview", rec.Code)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expired preview directory should have been removed, stat err = %v", err)
	}
}

func TestSweepRemovesOnlyExpiredEntries(t *testing.T) {
	freshDir := t.TempDir()
	expiredDir := t.TempDir()
	writePreviewFile(t, freshDir, "index.html", "fresh")
	writePreviewFile(t, expiredDir, "index.html", "expired")

	s := previewstore.New()
	s.Put("fresh", &previewstore.Entry{Dir: freshDir, Token: "t1", ExpiresAt: time.Now().Add(time.Hour)})
	s.Put("expired", &previewstore.Entry{Dir: expiredDir, Token: "t2", ExpiresAt: time.Now().Add(-time.Hour)})

	s.Sweep()

	if _, ok := s.Get("fresh", "t1"); !ok {
		t.Fatal("Sweep should not remove a still-fresh entry")
	}
	if _, err := os.Stat(expiredDir); !os.IsNotExist(err) {
		t.Fatalf("Sweep should have removed the expired entry's directory, stat err = %v", err)
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Fatalf("Sweep must not touch the fresh entry's directory: %v", err)
	}
}

func TestHTTPHandlerRejectsPathTraversalAttempt(t *testing.T) {
	dir := t.TempDir()
	writePreviewFile(t, dir, "index.html", "preview content")
	// A file outside the preview dir that a traversal attempt might target.
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("outside secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	s := previewstore.New()
	s.Put("abc123", &previewstore.Entry{Dir: dir, Token: "tok", ExpiresAt: time.Now().Add(time.Hour)})

	req := httptest.NewRequest(http.MethodGet, "/preview/abc123/tok/../../../../etc/passwd", nil)
	rec := httptest.NewRecorder()
	s.HTTPHandler().ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), "root:") {
		t.Fatalf("path traversal was not blocked: %s", rec.Body.String())
	}
}

func TestNewIDReturnsDistinctValues(t *testing.T) {
	a, err := previewstore.NewID(16)
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	b, err := previewstore.NewID(16)
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	if a == b {
		t.Fatal("NewID() returned the same value twice — not random")
	}
	if len(a) != 32 { // 16 bytes hex-encoded = 32 chars
		t.Fatalf("NewID(16) length = %d, want 32", len(a))
	}
}

func TestStoreCleanPathRequiresCookieBackedSession(t *testing.T) {
	dir := t.TempDir()
	writePreviewFile(t, dir, "index.html", "session content")

	s := previewstore.New()
	s.Put("abc123", &previewstore.Entry{
		Dir:       dir,
		Token:     "secret-token",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	req := httptest.NewRequest(http.MethodGet, "/preview/abc123/", nil)
	rec := httptest.NewRecorder()
	s.HTTPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 without session cookie", rec.Code)
	}
}

func TestStoreRevokeRemovesDirectoryAndEntry(t *testing.T) {
	dir := t.TempDir()
	writePreviewFile(t, dir, "index.html", "preview content")

	s := previewstore.New()
	s.Put("abc123", &previewstore.Entry{
		Dir:       dir,
		Token:     "tok",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	if !s.Revoke("abc123") {
		t.Fatal("Revoke() = false, want true")
	}
	if _, ok := s.GetByToken("abc123", "tok"); ok {
		t.Fatal("revoked preview should no longer be fetchable by token")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("revoked preview directory should be removed, stat err = %v", err)
	}
}

// TestEntryTokenSingleUseAfterContentFetch is the core #871 regression: once
// the session minted from an entry token has been used to fetch actual content
// (a successful GetBySession), the entry token can no longer mint a new
// session — a captured/leaked token is inert against the completed flow — while
// the legitimate session cookie keeps working.
func TestEntryTokenSingleUseAfterContentFetch(t *testing.T) {
	dir := t.TempDir()
	writePreviewFile(t, dir, "index.html", "session content")

	s := previewstore.New()
	s.Put("abc123", &previewstore.Entry{
		Dir:       dir,
		Token:     "entry-token",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	// First exchange mints a session.
	_, sessionToken, ok := s.EstablishSession("abc123", "entry-token")
	if !ok {
		t.Fatal("first EstablishSession should succeed")
	}
	// The session cookie fetches real content — this completes the handoff.
	if _, ok := s.GetBySession("abc123", sessionToken); !ok {
		t.Fatal("GetBySession with the minted session token should succeed")
	}

	// A leaked entry token must now be refused: it can no longer mint a session.
	if _, _, ok := s.EstablishSession("abc123", "entry-token"); ok {
		t.Fatal("entry token must be single-use: after the session is in active use it must not mint another session")
	}
	// The legitimate session cookie still works.
	if _, ok := s.GetBySession("abc123", sessionToken); !ok {
		t.Fatal("the already-established session must keep working after the entry token is retired")
	}
}

// TestEntryTokenReExchangeBeforeActivation proves the single-use rule does not
// break legitimate re-exchange: a page reload / network retry / second tab that
// re-hits the entry URL *before* any content has been fetched must still get a
// working session, and the same one, so the flow is idempotent.
func TestEntryTokenReExchangeBeforeActivation(t *testing.T) {
	dir := t.TempDir()
	writePreviewFile(t, dir, "index.html", "session content")

	s := previewstore.New()
	s.Put("abc123", &previewstore.Entry{
		Dir:       dir,
		Token:     "entry-token",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	_, first, ok := s.EstablishSession("abc123", "entry-token")
	if !ok {
		t.Fatal("first EstablishSession should succeed")
	}
	// No GetBySession in between (cookie never reached the browser / redirect
	// lost): re-exchange must still succeed and yield the same session token.
	_, second, ok := s.EstablishSession("abc123", "entry-token")
	if !ok {
		t.Fatal("re-exchange before activation must still succeed (reload/retry/second-tab)")
	}
	if first != second {
		t.Fatalf("re-exchange returned a different session token (%q vs %q); must be idempotent", first, second)
	}
}

// TestHTTPHandlerLeakedEntryTokenInertButCookieRedirects exercises the full
// HTTP flow for #871: after the redirect+content-fetch completes, the same
// entry URL is (a) inert for a token-only attacker with no cookie (404) but
// (b) gracefully redirected to the clean URL for the original browser that
// still holds the session cookie.
func TestHTTPHandlerLeakedEntryTokenInertButCookieRedirects(t *testing.T) {
	dir := t.TempDir()
	writePreviewFile(t, dir, "index.html", "session content")

	s := previewstore.New()
	s.Put("abc123", &previewstore.Entry{
		Dir:       dir,
		Token:     "secret-token",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	h := s.HTTPHandler()

	// Entry URL -> redirect + Set-Cookie.
	req := httptest.NewRequest(http.MethodGet, "/preview/abc123/secret-token/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("entry URL status = %d, want redirect", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v, want one session cookie", cookies)
	}

	// Clean URL with cookie -> serves content, activating the session.
	req = httptest.NewRequest(http.MethodGet, "/preview/abc123/", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("clean URL status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Attacker replays the entry URL with the token but NO cookie -> 404.
	req = httptest.NewRequest(http.MethodGet, "/preview/abc123/secret-token/", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("replayed entry token with no cookie status = %d, want 404 (single-use spent)", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("a spent entry token must not mint a fresh session cookie")
	}

	// Original browser re-clicks the entry URL, still holding the cookie ->
	// graceful redirect to the clean URL (no 404 on the token-in-path).
	req = httptest.NewRequest(http.MethodGet, "/preview/abc123/secret-token/", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("re-click with valid cookie status = %d, want graceful 302 to clean URL", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/preview/abc123/" {
		t.Fatalf("re-click redirect Location = %q, want /preview/abc123/", got)
	}
}

// TestStoreGetBySessionDoesNotRaceEstablishSession reproduces a data race:
// GetBySession used to read entry.SessionToken after releasing the mutex,
// while EstablishSession writes entry.SessionToken under the lock the first
// time a client exchanges the entry token for a session cookie. Unlike
// Token (immutable after Put, safe to read post-unlock), SessionToken is
// mutated in place, so an unsynchronized read races the write under
// `go test -race`. Run with `go test -race` to catch a regression here —
// this test intentionally hammers the same entry from many goroutines to
// make the race observable.
func TestStoreGetBySessionDoesNotRaceEstablishSession(t *testing.T) {
	dir := t.TempDir()
	writePreviewFile(t, dir, "index.html", "session content")

	s := previewstore.New()
	s.Put("race-entry", &previewstore.Entry{
		Dir:       dir,
		Token:     "entry-token",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.EstablishSession("race-entry", "entry-token")
		}()
		go func() {
			defer wg.Done()
			s.GetBySession("race-entry", "whatever-the-client-currently-has")
		}()
	}
	wg.Wait()
}

func TestStoreLookupDistinguishesMissingFromExpired(t *testing.T) {
	s := previewstore.New()
	if entry, status := s.Lookup("missing"); entry != nil || status != previewstore.LookupMissing {
		t.Fatalf("Lookup(missing) = (%v, %v), want (nil, %v)", entry, status, previewstore.LookupMissing)
	}

	dir := t.TempDir()
	writePreviewFile(t, dir, "index.html", "expired")
	s.Put("expired", &previewstore.Entry{
		Dir:       dir,
		Token:     "tok",
		ExpiresAt: time.Now().Add(-time.Minute),
	})
	if entry, status := s.Lookup("expired"); entry != nil || status != previewstore.LookupExpired {
		t.Fatalf("Lookup(expired) = (%v, %v), want (nil, %v)", entry, status, previewstore.LookupExpired)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expired preview directory should have been removed, stat err = %v", err)
	}
}

// TestStoreGetBySessionConcurrentWithEstablishNoRace exercises the reachable
// interleaving where an unauthenticated caller (preview ids are not secret)
// sends a bogus session cookie to GetBySession at the same time a legitimate
// first-open EstablishSession lazily writes entry.SessionToken. Reading that
// field outside the lock would be a data race; this must stay clean under
// `go test -race`. Regression for the v1.7.7 preview-session change.
func TestStoreGetBySessionConcurrentWithEstablishNoRace(t *testing.T) {
	s := previewstore.New()
	const n = 500
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("id-%d", i)
		s.Put(id, &previewstore.Entry{
			Dir:       t.TempDir(),
			Token:     "entry-token",
			ExpiresAt: time.Now().Add(time.Hour),
		})
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("id-%d", i)
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.EstablishSession(id, "entry-token") // writes SessionToken (first open)
		}()
		go func() {
			defer wg.Done()
			s.GetBySession(id, "bogus-cookie-value") // reads SessionToken
		}()
	}
	wg.Wait()
}

// TestStoreSessionActivationDoesNotRace hammers the new #871 shared field
// sessionActivated: GetBySession here uses the *real* session token so its
// activation write actually fires and races EstablishSession's read of the
// same field under the mutex. The previous race test used a non-matching
// token, so the activation write never executed. Run under `go test -race`.
func TestStoreSessionActivationDoesNotRace(t *testing.T) {
	dir := t.TempDir()
	writePreviewFile(t, dir, "index.html", "session content")

	s := previewstore.New()
	s.Put("race-entry", &previewstore.Entry{
		Dir:       dir,
		Token:     "entry-token",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	// Mint the real session token up front so both goroutines can act on it.
	_, sessionToken, ok := s.EstablishSession("race-entry", "entry-token")
	if !ok {
		t.Fatal("EstablishSession setup should succeed")
	}

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.EstablishSession("race-entry", "entry-token")
		}()
		go func() {
			defer wg.Done()
			s.GetBySession("race-entry", sessionToken) // fires the activation write
		}()
	}
	wg.Wait()
}

// TestStoreCountByOwnerAndDiskUsage covers the #871 cap-enforcement helpers:
// CountByOwner ignores other owners and expired entries; DiskUsageBytes sums
// only live entries' directory sizes.
func TestStoreCountByOwnerAndDiskUsage(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	dirExpired := t.TempDir()
	writePreviewFile(t, dirA, "index.html", "aaaaaaaaaa") // 10 bytes
	writePreviewFile(t, dirB, "index.html", "bbbbb")      // 5 bytes
	writePreviewFile(t, dirExpired, "index.html", "zzzzzzzzzzzzzzzzzzzz")

	s := previewstore.New()
	s.Put("a1", &previewstore.Entry{Dir: dirA, Token: "t", ExpiresAt: time.Now().Add(time.Hour), Owner: "alice"})
	s.Put("b1", &previewstore.Entry{Dir: dirB, Token: "t", ExpiresAt: time.Now().Add(time.Hour), Owner: "bob"})
	s.Put("x1", &previewstore.Entry{Dir: dirExpired, Token: "t", ExpiresAt: time.Now().Add(-time.Hour), Owner: "alice"})

	if got := s.CountByOwner("alice"); got != 1 {
		t.Fatalf("CountByOwner(alice) = %d, want 1 (expired entry must not count)", got)
	}
	if got := s.CountByOwner("bob"); got != 1 {
		t.Fatalf("CountByOwner(bob) = %d, want 1", got)
	}
	if got := s.CountByOwner("nobody"); got != 0 {
		t.Fatalf("CountByOwner(nobody) = %d, want 0", got)
	}
	if got := s.DiskUsageBytes(); got != 15 {
		t.Fatalf("DiskUsageBytes = %d, want 15 (10+5, expired excluded)", got)
	}
}

func TestStoreListAndListOwnedFilterAndCleanupExpired(t *testing.T) {
	freshAlice := t.TempDir()
	freshBob := t.TempDir()
	expired := t.TempDir()
	writePreviewFile(t, freshAlice, "index.html", "alice")
	writePreviewFile(t, freshBob, "index.html", "bob")
	writePreviewFile(t, expired, "index.html", "expired")

	s := previewstore.New()
	s.Put("alice-1", &previewstore.Entry{Dir: freshAlice, Token: "a", ExpiresAt: time.Now().Add(time.Hour), BuildStatus: "passed", Owner: "alice"})
	s.Put("bob-1", &previewstore.Entry{Dir: freshBob, Token: "b", ExpiresAt: time.Now().Add(time.Hour), BuildStatus: "failed", Owner: "bob"})
	s.Put("old-1", &previewstore.Entry{Dir: expired, Token: "x", ExpiresAt: time.Now().Add(-time.Minute), Owner: "alice"})

	all := s.List()
	if len(all) != 2 {
		t.Fatalf("List() len = %d, want 2 live entries", len(all))
	}
	gotIDs := []string{all[0].ID, all[1].ID}
	sort.Strings(gotIDs)
	if strings.Join(gotIDs, ",") != "alice-1,bob-1" {
		t.Fatalf("List() ids = %v, want [alice-1 bob-1]", gotIDs)
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Fatalf("List() should cleanup expired dir, stat err = %v", err)
	}

	owned := s.ListOwned("alice")
	if len(owned) != 1 || owned[0].ID != "alice-1" || owned[0].Owner != "alice" {
		t.Fatalf("ListOwned(alice) = %#v, want only alice-1", owned)
	}
	owned = s.ListOwned("nobody")
	if len(owned) != 0 {
		t.Fatalf("ListOwned(nobody) = %#v, want empty", owned)
	}
	allViaOwned := s.ListOwned("")
	if len(allViaOwned) != len(all) {
		t.Fatalf("ListOwned(\"\") len = %d, want %d like List()", len(allViaOwned), len(all))
	}
}

func TestStoreActiveDirsIsPureReadAndIgnoresExpiredNilAndEmpty(t *testing.T) {
	liveDir := t.TempDir()
	expiredDir := t.TempDir()
	writePreviewFile(t, liveDir, "index.html", "live")
	writePreviewFile(t, expiredDir, "index.html", "expired")

	s := previewstore.New()
	s.Put("live", &previewstore.Entry{Dir: liveDir, Token: "a", ExpiresAt: time.Now().Add(time.Hour)})
	s.Put("expired", &previewstore.Entry{Dir: expiredDir, Token: "b", ExpiresAt: time.Now().Add(-time.Minute)})
	s.Put("empty", &previewstore.Entry{Dir: "", Token: "c", ExpiresAt: time.Now().Add(time.Hour)})
	s.Put("nil-dir", nil)

	dirs := s.ActiveDirs()
	if len(dirs) != 1 {
		t.Fatalf("ActiveDirs() len = %d, want 1 live dir", len(dirs))
	}
	if _, ok := dirs[liveDir]; !ok {
		t.Fatalf("ActiveDirs() missing live dir %q", liveDir)
	}
	if _, ok := dirs[expiredDir]; ok {
		t.Fatalf("ActiveDirs() must not include expired dir %q", expiredDir)
	}
	if _, err := os.Stat(expiredDir); err != nil {
		t.Fatalf("ActiveDirs() must be a pure read and keep expired dir on disk, stat err = %v", err)
	}
}

func TestStoreRevokeOwnedRevokeAllAndRevokeAllOwned(t *testing.T) {
	aliceDir := t.TempDir()
	bobDir := t.TempDir()
	aliceDir2 := t.TempDir()
	writePreviewFile(t, aliceDir, "index.html", "alice-1")
	writePreviewFile(t, bobDir, "index.html", "bob-1")
	writePreviewFile(t, aliceDir2, "index.html", "alice-2")

	s := previewstore.New()
	s.Put("alice-1", &previewstore.Entry{Dir: aliceDir, Token: "a", ExpiresAt: time.Now().Add(time.Hour), Owner: "alice"})
	s.Put("bob-1", &previewstore.Entry{Dir: bobDir, Token: "b", ExpiresAt: time.Now().Add(time.Hour), Owner: "bob"})
	s.Put("alice-2", &previewstore.Entry{Dir: aliceDir2, Token: "c", ExpiresAt: time.Now().Add(time.Hour), Owner: "alice"})

	if s.RevokeOwned("bob-1", "alice") {
		t.Fatal("RevokeOwned() must reject owner mismatch")
	}
	if _, ok := s.GetByToken("bob-1", "b"); !ok {
		t.Fatal("owner-mismatch revoke must not delete the entry")
	}
	if !s.RevokeOwned("alice-1", "alice") {
		t.Fatal("RevokeOwned() must delete matching owner entry")
	}
	if _, err := os.Stat(aliceDir); !os.IsNotExist(err) {
		t.Fatalf("RevokeOwned() should remove alice dir, stat err = %v", err)
	}

	if got := s.RevokeAllOwned("alice"); got != 1 {
		t.Fatalf("RevokeAllOwned(alice) = %d, want 1", got)
	}
	if _, err := os.Stat(aliceDir2); !os.IsNotExist(err) {
		t.Fatalf("RevokeAllOwned(alice) should remove second alice dir, stat err = %v", err)
	}
	if _, err := os.Stat(bobDir); err != nil {
		t.Fatalf("RevokeAllOwned(alice) must not remove bob dir, stat err = %v", err)
	}

	if got := s.RevokeAll(); got != 1 {
		t.Fatalf("RevokeAll() = %d, want 1 remaining bob entry", got)
	}
	if _, err := os.Stat(bobDir); !os.IsNotExist(err) {
		t.Fatalf("RevokeAll() should remove bob dir, stat err = %v", err)
	}

	dir3 := t.TempDir()
	writePreviewFile(t, dir3, "index.html", "bob-2")
	s.Put("bob-2", &previewstore.Entry{Dir: dir3, Token: "d", ExpiresAt: time.Now().Add(time.Hour), Owner: "bob"})
	if got := s.RevokeAllOwned(""); got != 1 {
		t.Fatalf("RevokeAllOwned(\"\") = %d, want RevokeAll() behavior over the last entry", got)
	}
	if _, err := os.Stat(dir3); !os.IsNotExist(err) {
		t.Fatalf("RevokeAllOwned(\"\") should remove the remaining dir, stat err = %v", err)
	}
}
