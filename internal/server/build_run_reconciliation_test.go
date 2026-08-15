package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func writeBuildRunTranslation(t *testing.T, root, name, lang, title string) string {
	t.Helper()
	dir := filepath.Join(root, "posts", "external")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	body := "---\ntitle: " + title + "\nlang: " + lang + "\ndraft: false\n---\nBody\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func buildRunPages(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("data=%T", envelope["data"])
	}
	pages, ok := data["pages"].(map[string]any)
	if !ok {
		t.Fatalf("pages=%T data=%#v", data["pages"], data)
	}
	return pages
}

func stringSlice(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func writeBuildRunPublic(t *testing.T, root, rel, lang, title string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel), "index.html")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "<html lang=\"" + lang + "\"><head><title>" + title + "</title></head><body>published</body></html>"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSiteReportsExternalMultilingualChangesAfterRestart(t *testing.T) {
	root := t.TempDir()
	contentRoot := filepath.Join(root, "content")
	frPath := writeBuildRunTranslation(t, contentRoot, "index.fr.md", "fr", "FR A")
	enPath := writeBuildRunTranslation(t, contentRoot, "index.en.md", "en", "EN A")
	_ = frPath

	hugoRoot := filepath.Join(root, "hugo")
	if err := os.MkdirAll(hugoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Emit the surviving FR translation so the post-build public fingerprint
	// is real; EN is deliberately absent after its source deletion.
	mockHugo := "#!/bin/sh\n" +
		"if [ \"$1\" = \"version\" ]; then echo 'hugo v0.164.0+extended linux/amd64'; exit 0; fi\n" +
		"while [ \"$#\" -gt 0 ]; do if [ \"$1\" = \"--destination\" ]; then shift; dest=\"$1\"; fi; shift; done\n" +
		"mkdir -p \"$dest/fr/posts/external\"\n" +
		"printf '%s' '<html lang=\"fr\"><head><title>FR B</title></head><body>published</body></html>' > \"$dest/fr/posts/external/index.html\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "hugo"), []byte(mockHugo), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.HugoRoot = hugoRoot
	cfg.SiteRoot = filepath.Join(root, "public")
	cfg.DBPath = filepath.Join(root, "runtime.sqlite")
	cfg.OAuth.StoragePath = filepath.Join(root, "oauth.sqlite")
	cfg.DefaultLanguage = "en"
	if err := os.MkdirAll(cfg.SiteRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBuildRunPublic(t, cfg.SiteRoot, "fr/posts/external", "fr", "FR A")
	writeBuildRunPublic(t, cfg.SiteRoot, "posts/external", "en", "EN A")
	publicIndex, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sourceIndex, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatal(err)
	}
	state, err := db.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.BeginBuildRun("baseline", publicIndex, sourceIndex, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := state.CompleteBuildRun(db.PublicationManifest{BuildID: "baseline", SourceRevision: "source-a", OutputRevision: "public-a", Status: "ok", ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate SSH/Git writes while the MCP process is stopped.
	writeBuildRunTranslation(t, contentRoot, "index.fr.md", "fr", "FR B")
	if err := os.Remove(enPath); err != nil {
		t.Fatal(err)
	}

	server, err := NewStdio(cfg, publicIndex)
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "restart-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "build_site", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("build_site err=%v result=%#v", err, result)
	}
	pages := buildRunPages(t, result)
	included := stringSlice(pages["included"])
	deleted := stringSlice(pages["deleted_outputs"])
	if len(included) != 1 || included[0] != "posts/external:fr" {
		t.Fatalf("included=%#v, want external FR edit", included)
	}
	if len(deleted) != 1 || deleted[0] != "posts/external:en" {
		t.Fatalf("deleted_outputs=%#v, want external EN deletion", deleted)
	}

	second, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "build_site", Arguments: map[string]any{}})
	if err != nil || second.IsError {
		t.Fatalf("second build_site err=%v result=%#v", err, second)
	}
	secondPages := buildRunPages(t, second)
	if got := stringSlice(secondPages["included"]); len(got) != 0 {
		t.Fatalf("second included=%#v, want durable no-op", got)
	}
	if got := stringSlice(secondPages["deleted_outputs"]); len(got) != 0 {
		t.Fatalf("second deleted_outputs=%#v, want durable no-op", got)
	}
}
