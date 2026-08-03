package previewstore_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
