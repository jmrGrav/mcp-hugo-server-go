package write

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
)

func TestValidateDeletePageInputStages(t *testing.T) {
	tests := []struct {
		name string
		in   deletePageInput
		code string
	}{
		{"empty slug", deletePageInput{}, "invalid_params"},
		{"invalid lang", deletePageInput{Slug: "post", Lang: "../fr"}, "invalid_params"},
		{"invalid response mode", deletePageInput{Slug: "post", ResponseMode: "wide"}, "invalid_params"},
		{"invalid idempotency key", deletePageInput{Slug: "post", IdempotencyKey: strings.Repeat("x", 300)}, "invalid_params"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := validateDeletePageInput(tt.in); err == nil || !strings.Contains(err.Error(), tt.code) {
				t.Fatalf("validateDeletePageInput() error = %v", err)
			}
		})
	}
	lang, mode, err := validateDeletePageInput(deletePageInput{Slug: "post", Lang: "fr", ResponseMode: "compact", IdempotencyKey: "key"})
	if err != nil || lang != "fr" || mode != toolcontract.ResponseModeCompact {
		t.Fatalf("valid input = lang %q mode %q err %v", lang, mode, err)
	}
}

func TestDeletePageContentLockImmediateAndIdempotent(t *testing.T) {
	lock := &deletePageContentLock{}
	lock.release()
	if err := lock.acquire(); err != nil {
		t.Fatal(err)
	}
	if err := lock.acquire(); err != nil {
		t.Fatal(err)
	}
	lock.release()
}

func TestDeletePageContentLockTimesOut(t *testing.T) {
	hugosite.ContentMu.Lock()
	defer hugosite.ContentMu.Unlock()
	lock := &deletePageContentLock{}
	if err := lock.acquire(); err == nil || !strings.Contains(err.Error(), "build_in_progress") {
		t.Fatalf("lock timeout error = %v", err)
	}
}

func TestReplayDeletePageShortCircuits(t *testing.T) {
	if hash, cached, err := replayDeletePage(context.Background(), deletePageInput{DryRun: true, IdempotencyKey: "key"}, "", nil); err != nil || hash != "" || cached != nil {
		t.Fatalf("dry-run replay = %q, %v, %v", hash, cached, err)
	}
	if hash, cached, err := replayDeletePage(context.Background(), deletePageInput{}, "", nil); err != nil || hash != "" || cached != nil {
		t.Fatalf("unkeyed replay = %q, %v, %v", hash, cached, err)
	}
	runtime := &writeRegisterRuntime{idem: newIdempotencyStore(idempotencyTTLFromConfig(config.Config{}), 8, nil)}
	in := deletePageInput{Slug: "post", IdempotencyKey: "key", ExpectedRevision: "rev"}
	hash, cached, err := replayDeletePage(context.Background(), in, "", runtime)
	if err != nil || hash == "" || cached != nil {
		t.Fatalf("first keyed replay = %q, %v, %v", hash, cached, err)
	}
	want := newDeletePageOutput(deletePageData{Status: "deleted"}, 1)
	if err := runtime.idem.remember(idempotencyCallerKey(context.Background()), "delete_page", in.IdempotencyKey, hash, want); err != nil {
		t.Fatal(err)
	}
	_, cached, err = replayDeletePage(context.Background(), in, "", runtime)
	if err != nil || cached == nil || cached.Data.Status != "deleted" {
		t.Fatalf("cached replay = %v, %v", cached, err)
	}
}

func TestResolveDeletePageSourceAllowsOnlyUnscopedMissingSource(t *testing.T) {
	root := t.TempDir()
	if got, err := resolveDeletePageSource("missing", "", root); err != nil || got != (contentmodel.ResolvedSource{}) {
		t.Fatalf("unscoped missing source = %+v, %v", got, err)
	}
	if _, err := resolveDeletePageSource("missing", "fr", root); err == nil {
		t.Fatal("explicit missing language should fail")
	}
}

func TestValidateDeletePageConfirmationEarlyExits(t *testing.T) {
	if err := validateDeletePageConfirmation(deletePageInput{}, config.Config{}, contentmodel.ResolvedSource{}); err != nil {
		t.Fatal(err)
	}
	if err := validateDeletePageConfirmation(deletePageInput{ConfirmDeleteOfPublishedPage: true}, config.Config{RequireDeleteConfirmation: true}, contentmodel.ResolvedSource{SourcePath: "unused"}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	testPage := filepath.Join(root, "test.md")
	if err := os.WriteFile(testPage, []byte("---\ntest_content: true\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateDeletePageConfirmation(deletePageInput{Slug: "test"}, config.Config{RequireDeleteConfirmation: true}, contentmodel.ResolvedSource{SourcePath: testPage}); err != nil {
		t.Fatal(err)
	}
	realPage := filepath.Join(root, "real.md")
	if err := os.WriteFile(realPage, []byte("---\ntitle: Real\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateDeletePageConfirmation(deletePageInput{Slug: "real"}, config.Config{RequireDeleteConfirmation: true}, contentmodel.ResolvedSource{SourcePath: realPage}); err == nil {
		t.Fatal("real page without confirmation should fail")
	}
}

func TestCommitDeletePageSourceRemovesLastLanguageAndSourceLessBundle(t *testing.T) {
	root := t.TempDir()
	guard, err := security.New(root, true)
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(root, "post")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(bundle, "index.md")
	if err := os.WriteFile(source, []byte("---\ntitle: Post\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fullyRemoved, op, err := commitDeletePageSource(context.Background(), deletePageInput{Slug: "post"}, bundle, contentmodel.ResolvedSource{SourcePath: source}, "rev", "", guard, config.Config{ContentRoot: root}, nil)
	if err != nil || !fullyRemoved || op == nil {
		t.Fatalf("source delete = fullyRemoved %v op %v err %v", fullyRemoved, op, err)
	}
	if _, err := os.Stat(bundle); !os.IsNotExist(err) {
		t.Fatalf("bundle still exists: %v", err)
	}

	sourceLess := filepath.Join(root, "public-only")
	if err := os.MkdirAll(sourceLess, 0o755); err != nil {
		t.Fatal(err)
	}
	fullyRemoved, op, err = commitDeletePageSource(context.Background(), deletePageInput{Slug: "public-only"}, sourceLess, contentmodel.ResolvedSource{}, "", "", guard, config.Config{ContentRoot: root}, nil)
	if err != nil || !fullyRemoved || op == nil {
		t.Fatalf("source-less delete = fullyRemoved %v op %v err %v", fullyRemoved, op, err)
	}
}

func TestCommitDeletePageSourceFailureBoundaries(t *testing.T) {
	root := t.TempDir()
	guard, err := security.New(root, true)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("symlink revalidation", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "target.md")
		if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		bundle := filepath.Join(root, "symlink")
		if err := os.MkdirAll(bundle, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(bundle, "index.md")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, _, err := commitDeletePageSource(context.Background(), deletePageInput{Slug: "symlink"}, bundle, contentmodel.ResolvedSource{SourcePath: link}, "rev", "", guard, config.Config{ContentRoot: root}, nil); err == nil || !strings.Contains(err.Error(), "security_error") {
			t.Fatalf("symlink error = %v", err)
		}
	})

	t.Run("interruption after source-less cleanup", func(t *testing.T) {
		bundle := filepath.Join(root, "interrupted")
		if err := os.MkdirAll(bundle, 0o755); err != nil {
			t.Fatal(err)
		}
		previous := afterFilesystemWriteHook
		afterFilesystemWriteHook = func(tool, stage string) error { return context.Canceled }
		defer func() { afterFilesystemWriteHook = previous }()
		if _, _, err := commitDeletePageSource(context.Background(), deletePageInput{Slug: "interrupted"}, bundle, contentmodel.ResolvedSource{}, "", "", guard, config.Config{ContentRoot: root}, nil); err == nil || !strings.Contains(err.Error(), "interrupted after source cleanup") {
			t.Fatalf("interruption error = %v", err)
		}
	})

	t.Run("source unlink failure", func(t *testing.T) {
		bundle := filepath.Join(root, "unlink-failure")
		fakeSourceDir := filepath.Join(bundle, "index.md")
		if err := os.MkdirAll(fakeSourceDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fakeSourceDir, "child"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := commitDeletePageSource(context.Background(), deletePageInput{Slug: "unlink-failure"}, bundle, contentmodel.ResolvedSource{SourcePath: fakeSourceDir}, "rev", "", guard, config.Config{ContentRoot: root}, nil); err == nil || !strings.Contains(err.Error(), "delete_error") {
			t.Fatalf("unlink error = %v", err)
		}
	})
}

func TestValidateDeletePageConfirmationFailsClosedOnUnreadableFrontmatter(t *testing.T) {
	err := validateDeletePageConfirmation(deletePageInput{Slug: "missing"}, config.Config{RequireDeleteConfirmation: true}, contentmodel.ResolvedSource{SourcePath: filepath.Join(t.TempDir(), "missing.md")})
	if err == nil || !strings.Contains(err.Error(), "confirm_delete_of_published_page") {
		t.Fatalf("confirmation error = %v", err)
	}
}
