package admin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
)

func writeStaleContentFixture(t *testing.T, contentRoot, slug string, age time.Duration) {
	t.Helper()
	full := filepath.Join(contentRoot, slug, "index.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", full, err)
	}
	if err := os.WriteFile(full, []byte("---\ntitle: Test\n---\nBody.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", full, err)
	}
	if age > 0 {
		old := time.Now().Add(-age)
		if err := os.Chtimes(full, old, old); err != nil {
			t.Fatalf("Chtimes(%q): %v", full, err)
		}
	}
}

// TestCheckStaleTestContentDisabledByDefault confirms threshold<=0 (the
// zero value, i.e. an operator who never opted in) disables the check
// entirely — #608 is off by default, never a surprise behavior change on
// upgrade.
func TestCheckStaleTestContentDisabledByDefault(t *testing.T) {
	contentRoot := t.TempDir()
	writeStaleContentFixture(t, contentRoot, "posts/mcp-audit-old", 48*time.Hour)
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	if err := admin.CheckStaleTestContent(srcIdx, 0); err != nil {
		t.Errorf("CheckStaleTestContent with threshold=0 (disabled) = %v, want nil", err)
	}
	if err := admin.CheckStaleTestContent(srcIdx, -5); err != nil {
		t.Errorf("CheckStaleTestContent with negative threshold (disabled) = %v, want nil", err)
	}
}

// TestCheckStaleTestContentFlagsOldTestSlug is the core regression test for
// #608: a page matching the reserved test-content prefix convention, older
// than the configured threshold, must be reported.
func TestCheckStaleTestContentFlagsOldTestSlug(t *testing.T) {
	contentRoot := t.TempDir()
	writeStaleContentFixture(t, contentRoot, "posts/mcp-audit-forgotten", 48*time.Hour)
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	err = admin.CheckStaleTestContent(srcIdx, 24)
	if err == nil {
		t.Fatal("expected an error reporting the stale test-content slug, got nil")
	}
	if !strings.Contains(err.Error(), "mcp-audit-forgotten") {
		t.Errorf("error = %q, want it to mention the stale slug", err.Error())
	}
}

// TestCheckStaleTestContentIgnoresRecentTestSlug confirms a test-prefixed
// page younger than the threshold is not flagged — this is meant to catch
// forgotten content, not every test page created moments ago as part of an
// in-progress audit session.
func TestCheckStaleTestContentIgnoresRecentTestSlug(t *testing.T) {
	contentRoot := t.TempDir()
	writeStaleContentFixture(t, contentRoot, "posts/mcp-audit-fresh", 1*time.Hour)
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	if err := admin.CheckStaleTestContent(srcIdx, 24); err != nil {
		t.Errorf("CheckStaleTestContent = %v, want nil for a test slug younger than the threshold", err)
	}
}

// TestCheckStaleTestContentIgnoresNonTestSlugs confirms real, non-test
// content is never flagged regardless of age — this check must never
// misclassify legitimate content (e.g. an article literally about a
// security audit) as leftover test cruft.
func TestCheckStaleTestContentIgnoresNonTestSlugs(t *testing.T) {
	contentRoot := t.TempDir()
	writeStaleContentFixture(t, contentRoot, "posts/audit-securite-2026", 72*time.Hour)
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	if err := admin.CheckStaleTestContent(srcIdx, 24); err != nil {
		t.Errorf("CheckStaleTestContent = %v, want nil for legitimate non-test content regardless of age", err)
	}
}

func writeExplicitTestContentFixture(t *testing.T, contentRoot, slug string, expiresAt time.Time) {
	t.Helper()
	full := filepath.Join(contentRoot, slug, "index.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", full, err)
	}
	fm := "---\ntitle: Test\ndraft: true\ntest_content: true\ntest_content_expires_at: " + expiresAt.UTC().Format(time.RFC3339) + "\n---\nBody.\n"
	if err := os.WriteFile(full, []byte(fm), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", full, err)
	}
}

// TestCheckStaleTestContentHonorsExplicitExpiryEvenWhenThresholdDisabled is
// the core regression test for #661: a page created via create_page's
// opt-in test_content parameter with an expired test_content_expires_at
// must be flagged even when the server-wide stale_test_content_threshold_hours
// is disabled (0) — the caller explicitly asked for TTL tracking on this
// specific page, so it must not depend on the operator having separately
// opted in to the server-wide sweep.
func TestCheckStaleTestContentHonorsExplicitExpiryEvenWhenThresholdDisabled(t *testing.T) {
	contentRoot := t.TempDir()
	writeExplicitTestContentFixture(t, contentRoot, "posts/audit-run", time.Now().Add(-1*time.Hour))
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	err = admin.CheckStaleTestContent(srcIdx, 0)
	if err == nil {
		t.Fatal("expected an error reporting the expired test_content page even with threshold=0, got nil")
	}
	if !strings.Contains(err.Error(), "posts/audit-run") {
		t.Errorf("error = %q, want it to mention the expired slug", err.Error())
	}
}

// TestCheckStaleTestContentIgnoresUnexpiredExplicitTestContent confirms a
// test_content page whose TTL hasn't elapsed yet is not flagged, even
// though it matches no reserved-slug-prefix convention at all.
func TestCheckStaleTestContentIgnoresUnexpiredExplicitTestContent(t *testing.T) {
	contentRoot := t.TempDir()
	writeExplicitTestContentFixture(t, contentRoot, "posts/audit-run-fresh", time.Now().Add(2*time.Hour))
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	if err := admin.CheckStaleTestContent(srcIdx, 0); err != nil {
		t.Errorf("CheckStaleTestContent = %v, want nil for a test_content page whose TTL hasn't elapsed", err)
	}
}

// TestCheckStaleTestContentDedupsBilingualExpiredBundle confirms an expired
// bilingual test bundle is reported once by slug, not once per language
// file. This keeps the operator-facing warning actionable instead of
// duplicative.
func TestCheckStaleTestContentDedupsBilingualExpiredBundle(t *testing.T) {
	contentRoot := t.TempDir()
	expiresAt := time.Now().Add(-2 * time.Hour)
	writeExplicitTestContentFixture(t, contentRoot, "posts/test-audit-bilingual", expiresAt)
	full := filepath.Join(contentRoot, "posts", "test-audit-bilingual", "index.md")
	enPath := filepath.Join(filepath.Dir(full), "index.en.md")
	frPath := filepath.Join(filepath.Dir(full), "index.fr.md")
	if err := os.Remove(full); err != nil {
		t.Fatalf("Remove(%q): %v", full, err)
	}
	fm := "---\ntitle: Test\ndraft: true\ntest_content: true\ntest_content_expires_at: " + expiresAt.UTC().Format(time.RFC3339) + "\n---\nBody.\n"
	if err := os.WriteFile(enPath, []byte(fm), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", enPath, err)
	}
	if err := os.WriteFile(frPath, []byte(fm), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", frPath, err)
	}

	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	err = admin.CheckStaleTestContent(srcIdx, 0)
	if err == nil {
		t.Fatal("expected an error reporting the expired bilingual test bundle, got nil")
	}
	if got := strings.Count(err.Error(), "posts/test-audit-bilingual"); got != 1 {
		t.Fatalf("error = %q, want slug to appear exactly once, got %d", err.Error(), got)
	}
}

// TestCheckStaleTestContentMalformedExpiryStillFallsBackToReservedSlugAge
// confirms a malformed explicit expiry does not silently exempt a reserved
// test slug from the older age-threshold sweep.
func TestCheckStaleTestContentMalformedExpiryStillFallsBackToReservedSlugAge(t *testing.T) {
	contentRoot := t.TempDir()
	full := filepath.Join(contentRoot, "posts", "mcp-audit-malformed-expiry", "index.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", full, err)
	}
	fm := "---\ntitle: Test\ndraft: true\ntest_content: true\ntest_content_expires_at: definitely-not-rfc3339\n---\nBody.\n"
	if err := os.WriteFile(full, []byte(fm), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", full, err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(full, old, old); err != nil {
		t.Fatalf("Chtimes(%q): %v", full, err)
	}

	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}

	err = admin.CheckStaleTestContent(srcIdx, 24)
	if err == nil {
		t.Fatal("expected malformed expiry page to still be caught by reserved-slug age fallback, got nil")
	}
	if !strings.Contains(err.Error(), "posts/mcp-audit-malformed-expiry") {
		t.Fatalf("error = %q, want it to mention the stale reserved slug", err.Error())
	}
}
