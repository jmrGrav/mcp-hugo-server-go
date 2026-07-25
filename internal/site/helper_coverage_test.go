package site

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
)

func TestAccessProfileHelpers(t *testing.T) {
	var nilCtx context.Context
	if got := WithAccessProfile(nilCtx, AccessProfileReader); got != nil {
		t.Fatalf("WithAccessProfile(nil, reader) = %#v, want nil", got)
	}

	base := context.Background()
	if got := WithAccessProfile(base, ""); got != base {
		t.Fatal("WithAccessProfile(ctx, empty) should return the original context")
	}

	if got := AccessProfileFromContext(nilCtx); got != "" {
		t.Fatalf("AccessProfileFromContext(nil) = %q, want empty", got)
	}
	if IsReaderProfile(nilCtx) {
		t.Fatal("IsReaderProfile(nil) = true, want false")
	}

	readerCtx := WithAccessProfile(base, AccessProfileReader)
	if got := AccessProfileFromContext(readerCtx); got != AccessProfileReader {
		t.Fatalf("AccessProfileFromContext(readerCtx) = %q, want %q", got, AccessProfileReader)
	}
	if !IsReaderProfile(readerCtx) {
		t.Fatal("IsReaderProfile(readerCtx) = false, want true")
	}
}

func TestReaderSafeResolvedPage(t *testing.T) {
	public := &Page{Slug: "/posts/public/", Title: "Public"}
	source := &hugosite.SourcePage{Slug: "posts/public", Title: "Source"}

	t.Run("public only", func(t *testing.T) {
		got, ok := ReaderSafeResolvedPage(ResolvedPage{Public: public})
		if !ok {
			t.Fatal("ReaderSafeResolvedPage(public-only) = false, want true")
		}
		if got.Public != public {
			t.Fatalf("ReaderSafeResolvedPage(public-only).Public = %#v, want same public pointer", got.Public)
		}
		if got.Source != nil {
			t.Fatalf("ReaderSafeResolvedPage(public-only).Source = %#v, want nil", got.Source)
		}
	})

	t.Run("source only", func(t *testing.T) {
		if got, ok := ReaderSafeResolvedPage(ResolvedPage{Source: source}); ok || got.Public != nil || got.Source != nil {
			t.Fatalf("ReaderSafeResolvedPage(source-only) = (%#v, %v), want (zero, false)", got, ok)
		}
	})

	t.Run("public and source", func(t *testing.T) {
		got, ok := ReaderSafeResolvedPage(ResolvedPage{Public: public, Source: source, SourcePath: "/tmp/source.md"})
		if !ok {
			t.Fatal("ReaderSafeResolvedPage(public+source) = false, want true")
		}
		if got.Public != public {
			t.Fatalf("ReaderSafeResolvedPage(public+source).Public = %#v, want same public pointer", got.Public)
		}
		if got.Source != nil || got.SourcePath != "" {
			t.Fatalf("ReaderSafeResolvedPage(public+source) leaked source = %#v path=%q", got.Source, got.SourcePath)
		}
	})
}

func TestStateForResolvedPage(t *testing.T) {
	t.Run("public only", func(t *testing.T) {
		got := StateForResolvedPage(ResolvedPage{
			Public: &Page{Slug: "/posts/public/"},
		}, t.TempDir())
		want := LifecycleState{
			SourceState: "absent",
			BuildState:  "built",
			PublicState: "available",
			IndexState:  "fresh",
		}
		if got != want {
			t.Fatalf("StateForResolvedPage(public-only) = %#v, want %#v", got, want)
		}
	})

	t.Run("source only", func(t *testing.T) {
		got := StateForResolvedPage(ResolvedPage{
			Source: &hugosite.SourcePage{Slug: "posts/source-only"},
		}, t.TempDir())
		want := LifecycleState{
			SourceState: "present",
			BuildState:  "pending",
			PublicState: "not_yet_available",
			IndexState:  "source_only",
		}
		if got != want {
			t.Fatalf("StateForResolvedPage(source-only) = %#v, want %#v", got, want)
		}
	})

	t.Run("source older than public html", func(t *testing.T) {
		siteRoot := t.TempDir()
		sourcePath := filepath.Join(t.TempDir(), "content", "posts", "page", "index.md")
		writeFileWithTimes(t, sourcePath, "source", time.Now().Add(-2*time.Hour))
		publicPath := filepath.Join(siteRoot, "posts", "page", "index.html")
		writeFileWithTimes(t, publicPath, "<main>public</main>", time.Now().Add(-time.Hour))

		got := StateForResolvedPage(ResolvedPage{
			Public:     &Page{Slug: "/posts/page/"},
			Source:     &hugosite.SourcePage{Slug: "posts/page", FilePath: sourcePath},
			SourcePath: sourcePath,
		}, siteRoot)
		want := LifecycleState{
			SourceState: "present",
			BuildState:  "built",
			PublicState: "available",
			IndexState:  "fresh",
		}
		if got != want {
			t.Fatalf("StateForResolvedPage(source older) = %#v, want %#v", got, want)
		}
	})

	t.Run("source newer than public html", func(t *testing.T) {
		siteRoot := t.TempDir()
		sourcePath := filepath.Join(t.TempDir(), "content", "posts", "page", "index.md")
		writeFileWithTimes(t, sourcePath, "source", time.Now())
		publicPath := filepath.Join(siteRoot, "posts", "page", "index.html")
		writeFileWithTimes(t, publicPath, "<main>public</main>", time.Now().Add(-2*time.Hour))

		got := StateForResolvedPage(ResolvedPage{
			Public:     &Page{Slug: "/posts/page/"},
			Source:     &hugosite.SourcePage{Slug: "posts/page", FilePath: sourcePath},
			SourcePath: sourcePath,
		}, siteRoot)
		want := LifecycleState{
			SourceState: "present",
			BuildState:  "pending",
			PublicState: "stale",
			IndexState:  "stale",
		}
		if got != want {
			t.Fatalf("StateForResolvedPage(source newer) = %#v, want %#v", got, want)
		}
	})

	t.Run("build pending flag wins without filesystem comparison", func(t *testing.T) {
		got := StateForResolvedPage(ResolvedPage{
			Public: &Page{Slug: "/posts/page/"},
			Source: &hugosite.SourcePage{
				Slug:         "posts/page",
				BuildPending: true,
			},
		}, "")
		want := LifecycleState{
			SourceState: "present",
			BuildState:  "pending",
			PublicState: "stale",
			IndexState:  "stale",
		}
		if got != want {
			t.Fatalf("StateForResolvedPage(build pending) = %#v, want %#v", got, want)
		}
	})

	t.Run("zero value when nothing resolved", func(t *testing.T) {
		if got := StateForResolvedPage(ResolvedPage{}, t.TempDir()); got != (LifecycleState{}) {
			t.Fatalf("StateForResolvedPage(empty) = %#v, want zero value", got)
		}
	})
}

func TestSourceNewerThanPublicOutputAndPublicHTMLPath(t *testing.T) {
	t.Run("root slug path", func(t *testing.T) {
		got := publicHTMLPath("/srv/site", "/")
		want := filepath.Join("/srv/site", "index.html")
		if got != want {
			t.Fatalf("publicHTMLPath(root) = %q, want %q", got, want)
		}
	})

	t.Run("nested slug path", func(t *testing.T) {
		got := publicHTMLPath("/srv/site", "/posts/example/")
		want := filepath.Join("/srv/site", "posts", "example", "index.html")
		if got != want {
			t.Fatalf("publicHTMLPath(nested) = %q, want %q", got, want)
		}
	})

	t.Run("false when site root missing", func(t *testing.T) {
		sourcePath := filepath.Join(t.TempDir(), "content", "posts", "missing", "index.md")
		writeFileWithTimes(t, sourcePath, "source", time.Now())
		got := sourceNewerThanPublicOutput(ResolvedPage{
			Public:     &Page{Slug: "/posts/missing/"},
			Source:     &hugosite.SourcePage{Slug: "posts/missing", FilePath: sourcePath},
			SourcePath: sourcePath,
		}, "")
		if got {
			t.Fatal("sourceNewerThanPublicOutput(siteRoot missing) = true, want false")
		}
	})

	t.Run("false when source file missing", func(t *testing.T) {
		siteRoot := t.TempDir()
		writeFileWithTimes(t, filepath.Join(siteRoot, "posts", "missing", "index.html"), "<main>public</main>", time.Now())
		got := sourceNewerThanPublicOutput(ResolvedPage{
			Public:     &Page{Slug: "/posts/missing/"},
			Source:     &hugosite.SourcePage{Slug: "posts/missing", FilePath: filepath.Join(t.TempDir(), "does-not-exist.md")},
			SourcePath: filepath.Join(t.TempDir(), "does-not-exist.md"),
		}, siteRoot)
		if got {
			t.Fatal("sourceNewerThanPublicOutput(source missing) = true, want false")
		}
	})
}

func writeFileWithTimes(t *testing.T, path, body string, modTime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("Chtimes(%q): %v", path, err)
	}
}
