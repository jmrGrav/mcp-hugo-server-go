package write

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"golang.org/x/time/rate"
)

func TestCallerLimiterEnforcesPerMinuteBudget(t *testing.T) {
	var mu sync.Mutex
	m := make(map[string]*rate.Limiter)
	for i := 0; i < 3; i++ {
		if !callerLimiter(&mu, m, "caller-a", 3).Allow() {
			t.Fatalf("call %d: expected allowed within budget", i)
		}
	}
	if callerLimiter(&mu, m, "caller-a", 3).Allow() {
		t.Fatal("expected 4th call to be denied")
	}
}

func TestCallerLimiterIsolatesCallersByKey(t *testing.T) {
	var mu sync.Mutex
	m := make(map[string]*rate.Limiter)
	if !callerLimiter(&mu, m, "caller-a", 1).Allow() {
		t.Fatal("caller-a's first call should be allowed")
	}
	if callerLimiter(&mu, m, "caller-a", 1).Allow() {
		t.Fatal("caller-a's second call should be denied")
	}
	if !callerLimiter(&mu, m, "caller-b", 1).Allow() {
		t.Fatal("caller-b must have its own independent budget")
	}
}

func TestCallerLimiterZeroOrNegativePerMinuteFailsOpen(t *testing.T) {
	// A zero/unset config value must never divide by zero or silently deny
	// every call — it fails open (always allows), matching the documented
	// behavior in callerLimiter's doc comment. In production this branch is
	// unreachable for destructive/mutation limits because config.Load clamps
	// non-positive values to safe defaults; this is defense-in-depth for any
	// other caller of callerLimiter that doesn't go through config.Load.
	var mu sync.Mutex
	m := make(map[string]*rate.Limiter)
	for _, perMinute := range []int{0, -1} {
		l := callerLimiter(&mu, m, "caller-zero", perMinute)
		for i := 0; i < 100; i++ {
			if !l.Allow() {
				t.Fatalf("perMinute=%d: expected fail-open (always allow), denied on call %d", perMinute, i)
			}
		}
		delete(m, "caller-zero")
	}
}

func TestWriteHelpers(t *testing.T) {
	fm := buildFrontmatter("Title", []string{"go"}, []string{"docs"}, "Body")
	if !strings.Contains(fm, "Title") || !strings.Contains(fm, "draft: false") || !strings.Contains(fm, "Body") {
		t.Fatalf("buildFrontmatter() = %q", fm)
	}
	m := map[string]any{"title": "Title", "tags": []string{"go"}}
	fm2 := buildFrontmatterFromMap(m, "Body")
	if !strings.Contains(fm2, "Title") || !strings.Contains(fm2, "Body") {
		t.Fatalf("buildFrontmatterFromMap() = %q", fm2)
	}
	if !*fileutil.BoolPtr(true) {
		t.Fatal("fileutil.BoolPtr() returned false")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "page.md")
	if err := fileutil.AtomicWrite(path, "content"); err != nil {
		t.Fatalf("fileutil.AtomicWrite() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "content" {
		t.Fatalf("atomicWrite() content = %q", string(data))
	}

	audit := filepath.Join(dir, "audit.log")
	if err := appendAuditLog(audit, "entry\n"); err != nil {
		t.Fatalf("appendAuditLog() error = %v", err)
	}
	raw, err := os.ReadFile(audit)
	if err != nil {
		t.Fatalf("ReadFile(audit) error = %v", err)
	}
	if string(raw) != "entry\n" {
		t.Fatalf("appendAuditLog() content = %q", string(raw))
	}

	defs := Defs()
	if len(defs) != 4 || defs[0].RequiredScope != "content.write" {
		t.Fatalf("Defs() = %#v", defs)
	}
}

func TestSimpleDiff(t *testing.T) {
	// Identical content → empty diff
	if got := simpleDiff("f.md", "a\nb\n", "a\nb\n"); got != "" {
		t.Fatalf("simpleDiff(identical) = %q", got)
	}
	// Single-line change contains + and - markers
	got := simpleDiff("f.md", "hello\nworld\n", "hello\nearth\n")
	if !strings.Contains(got, "-world") || !strings.Contains(got, "+earth") {
		t.Fatalf("simpleDiff(change) = %q", got)
	}
	// Addition
	got = simpleDiff("f.md", "a\n", "a\nb\n")
	if !strings.Contains(got, "+b") {
		t.Fatalf("simpleDiff(addition) = %q", got)
	}
	// Deletion
	got = simpleDiff("f.md", "a\nb\n", "a\n")
	if !strings.Contains(got, "-b") {
		t.Fatalf("simpleDiff(deletion) = %q", got)
	}
	// Large-file fallback (>500 lines each)
	big := strings.Repeat("line\n", 501)
	got = simpleDiff("big.md", big, big+"extra\n")
	if !strings.Contains(got, "content changed") {
		t.Fatalf("simpleDiff(large) = %q", got)
	}
}

func TestWriteHelperBranches(t *testing.T) {
	fm := buildFrontmatter("Title", nil, nil, "")
	if !strings.Contains(fm, "tags: []") || !strings.Contains(fm, "categories: []") {
		t.Fatalf("buildFrontmatter(nil slices) = %q", fm)
	}
	m := map[string]any{"title": "Title"}
	fm2 := buildFrontmatterFromMap(m, "")
	if !strings.Contains(fm2, "title: Title") {
		t.Fatalf("buildFrontmatterFromMap() = %q", fm2)
	}

	dir := t.TempDir()
	blocker := filepath.Join(dir, "audit.log")
	if err := os.MkdirAll(blocker, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := appendAuditLog(blocker, "entry\n"); err == nil {
		t.Fatal("appendAuditLog() should fail when target path is a directory")
	}

	// validateFrontmatterRoundTrip: thematic-break body must not be rejected
	okContent := "---\ntitle: Test\n---\n---\n\nSome content after a horizontal rule.\n"
	if err := validateFrontmatterRoundTrip(okContent); err != nil {
		t.Fatalf("validateFrontmatterRoundTrip(thematic-break body) = %v", err)
	}
	// duplicated frontmatter block must be caught
	dupContent := "---\ntitle: Test\n---\n---\ntitle: Test\ndate: 2026-07-01\n---\nReal body\n"
	if err := validateFrontmatterRoundTrip(dupContent); err == nil {
		t.Fatal("validateFrontmatterRoundTrip(duplicated frontmatter) should return error")
	}
	// valid content passes
	if err := validateFrontmatterRoundTrip("---\ntitle: T\n---\nBody\n"); err != nil {
		t.Fatalf("validateFrontmatterRoundTrip(valid) = %v", err)
	}
}

func TestRegisterNilServer(t *testing.T) {
	Register(nil, nil, nil, config.Default(), nil)
}

func TestApplyPageUpdatesPreservesOrder(t *testing.T) {
	input := "---\ntitle: Old Title\ndate: 2026-01-01T00:00:00Z\ntags:\n  - go\n---\n\nOriginal body."

	got, err := applyPageUpdates(input, "New Title", "", pageUpdateOpts{})
	if err != nil {
		t.Fatalf("applyPageUpdates error: %v", err)
	}
	if !strings.Contains(got, "New Title") {
		t.Errorf("title not updated: %s", got)
	}
	titleIdx := strings.Index(got, "title:")
	dateIdx := strings.Index(got, "date:")
	if dateIdx < titleIdx {
		t.Errorf("field order not preserved: date appears before title in:\n%s", got)
	}
	if !strings.Contains(got, "Original body.") {
		t.Errorf("body changed unexpectedly: %s", got)
	}
}

func TestApplyPageUpdatesBody(t *testing.T) {
	input := "---\ntitle: Page\n---\n\nOld body."
	got, err := applyPageUpdates(input, "", "New body.", pageUpdateOpts{})
	if err != nil {
		t.Fatalf("applyPageUpdates error: %v", err)
	}
	if !strings.Contains(got, "New body.") {
		t.Errorf("body not updated: %s", got)
	}
	if strings.Contains(got, "Old body.") {
		t.Errorf("old body still present: %s", got)
	}
}

func TestApplyPageUpdatesExtendedFields(t *testing.T) {
	input := "---\ntitle: Page\ndate: 2026-01-01T00:00:00Z\ntags:\n  - old\ncategories:\n  - OldCat\ndraft: false\n---\n\nBody."

	draft := true
	got, err := applyPageUpdates(input, "", "", pageUpdateOpts{
		Tags:        []string{"go", "hugo"},
		Categories:  []string{"Infrastructure"},
		Draft:       &draft,
		Description: "A test page.",
	})
	if err != nil {
		t.Fatalf("applyPageUpdates error: %v", err)
	}
	if !strings.Contains(got, "- go") || !strings.Contains(got, "- hugo") {
		t.Errorf("tags not updated: %s", got)
	}
	if strings.Contains(got, "- old") {
		t.Errorf("old tag still present: %s", got)
	}
	if !strings.Contains(got, "- Infrastructure") {
		t.Errorf("category not updated: %s", got)
	}
	if !strings.Contains(got, "OldCat") == strings.Contains(got, "OldCat") { // always passes
	}
	if strings.Contains(got, "- OldCat") {
		t.Errorf("old category still present: %s", got)
	}
	if !strings.Contains(got, "draft: true") {
		t.Errorf("draft not set to true: %s", got)
	}
	if !strings.Contains(got, "description: A test page.") {
		t.Errorf("description not added: %s", got)
	}
	if !strings.Contains(got, "Body.") {
		t.Errorf("body changed unexpectedly: %s", got)
	}
}

func TestApplyPageUpdatesPreservesTwoSpaceSequenceIndent(t *testing.T) {
	input := "---\ntitle: Page\ntags:\n  - old\ncategories:\n  - OldCat\n---\n\nBody."

	got, err := applyPageUpdates(input, "", "", pageUpdateOpts{
		Tags:       []string{"go", "hugo"},
		Categories: []string{"Infrastructure"},
	})
	if err != nil {
		t.Fatalf("applyPageUpdates error: %v", err)
	}
	for _, want := range []string{
		"tags:\n  - go\n  - hugo",
		"categories:\n  - Infrastructure",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("applyPageUpdates sequence indent mismatch, missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\n    - ") {
		t.Fatalf("applyPageUpdates wrote 4-space sequence indent:\n%s", got)
	}
}

func TestApplyPageUpdatesNewDescription(t *testing.T) {
	input := "---\ntitle: Page\ntags: []\n---\n\nBody."
	got, err := applyPageUpdates(input, "", "", pageUpdateOpts{Description: "New desc."})
	if err != nil {
		t.Fatalf("applyPageUpdates error: %v", err)
	}
	if !strings.Contains(got, "description: New desc.") {
		t.Errorf("description not appended: %s", got)
	}
}

func TestApplyPageUpdatesRejectsNonMappingFrontmatter(t *testing.T) {
	input := "---\n- item\n---\n\nBody."
	_, err := applyPageUpdates(input, "Title", "", pageUpdateOpts{})
	if err == nil {
		t.Fatal("applyPageUpdates() should reject non-mapping frontmatter")
	}
	if !strings.Contains(err.Error(), "frontmatter root must be a mapping") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveExistingSource(t *testing.T) {
	makeBundle := func(root, slug string, files ...string) {
		dir := filepath.Join(append([]string{root}, strings.Split(slug, "/")...)...)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		for _, name := range files {
			f, _ := os.Create(filepath.Join(dir, name))
			f.Close()
		}
	}

	t.Run("single index.md", func(t *testing.T) {
		root := t.TempDir()
		makeBundle(root, "posts/test", "index.md")
		got, err := resolveExistingSource(root, "posts/test", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if filepath.Base(got.SourcePath) != "index.md" {
			t.Errorf("want index.md, got %s", filepath.Base(got.SourcePath))
		}
	})

	t.Run("single lang file with no lang param", func(t *testing.T) {
		root := t.TempDir()
		makeBundle(root, "posts/test", "index.fr.md")
		got, err := resolveExistingSource(root, "posts/test", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if filepath.Base(got.SourcePath) != "index.fr.md" {
			t.Errorf("want index.fr.md, got %s", filepath.Base(got.SourcePath))
		}
		if got.Lang != "fr" {
			t.Errorf("want lang=fr, got %q", got.Lang)
		}
	})

	t.Run("ambiguous: two lang files, no lang param", func(t *testing.T) {
		root := t.TempDir()
		makeBundle(root, "posts/test", "index.fr.md", "index.en.md")
		_, err := resolveExistingSource(root, "posts/test", "")
		if err == nil || !strings.Contains(err.Error(), "ambiguous_language") {
			t.Errorf("want ambiguous_language error, got %v", err)
		}
	})

	t.Run("lang param selects correct file", func(t *testing.T) {
		root := t.TempDir()
		makeBundle(root, "posts/test", "index.fr.md", "index.en.md")
		got, err := resolveExistingSource(root, "posts/test", "fr")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if filepath.Base(got.SourcePath) != "index.fr.md" {
			t.Errorf("want index.fr.md, got %s", filepath.Base(got.SourcePath))
		}
	})

	t.Run("lang param not found", func(t *testing.T) {
		root := t.TempDir()
		makeBundle(root, "posts/test", "index.fr.md")
		_, err := resolveExistingSource(root, "posts/test", "de")
		if err == nil || !strings.Contains(err.Error(), "not_found") {
			t.Errorf("want not_found error, got %v", err)
		}
	})

	t.Run("no files", func(t *testing.T) {
		root := t.TempDir()
		_, err := resolveExistingSource(root, "posts/test", "")
		if err == nil || !strings.Contains(err.Error(), "not_found") {
			t.Errorf("want not_found error, got %v", err)
		}
	})
}
