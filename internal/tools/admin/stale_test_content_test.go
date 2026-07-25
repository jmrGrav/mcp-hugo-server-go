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
