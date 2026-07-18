package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestServer(t *testing.T, cfg config.Config) (*mcp.ClientSession, func()) {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	admin.Register(s, cfg)

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	return session, func() { _ = session.Close() }
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()
	return session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
}

func resultText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && tc.Text != "" {
			return tc.Text
		}
	}
	b, _ := json.Marshal(res.Content)
	return string(b)
}

func decodeStructuredResult(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	return out
}

func TestGenerateFeaturedImage_Success(t *testing.T) {
	fakeBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(fakeBytes)
	}))
	defer srv.Close()

	hugoRoot := t.TempDir()
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = hugoRoot
	cfg.ImageGenURL = srv.URL
	cfg.ImageGenKey = "test-key"

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "generate_hero_image", map[string]any{
		"slug":   "my-post",
		"prompt": "a scenic mountain landscape",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}

	expectedPath := filepath.Join(hugoRoot, "static", "images", "my-post-featured.jpg")
	if _, statErr := os.Stat(expectedPath); statErr != nil {
		t.Fatalf("expected file not found at %s: %v", expectedPath, statErr)
	}
	got, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != string(fakeBytes) {
		t.Fatalf("file content mismatch: got %v, want %v", got, fakeBytes)
	}

	text := resultText(res)
	if !strings.Contains(text, expectedPath) {
		t.Fatalf("result text %q does not contain path %q", text, expectedPath)
	}
	out := decodeStructuredResult(t, res)
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", out["data"])
	}
	if got := data["path"]; got != expectedPath {
		t.Fatalf("generate_hero_image data.path = %v, want %q", got, expectedPath)
	}
}

func TestGenerateFeaturedImage_MIMEReject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintln(w, "<html>not an image</html>")
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()
	cfg.ImageGenURL = srv.URL

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "generate_hero_image", map[string]any{
		"slug":   "test-post",
		"prompt": "test prompt",
	})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected tool to return error for non-image content-type, got success")
	}
	text := resultText(res)
	if !strings.Contains(strings.ToLower(text), "unexpected content-type") {
		t.Fatalf("error text %q does not contain 'unexpected content-type'", text)
	}
}

func TestGenerateFeaturedImage_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xFF})
	}))
	defer srv.Close()

	siteRoot := t.TempDir()
	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.ImageGenURL = srv.URL

	session, done := newTestServer(t, cfg)
	defer done()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "generate_hero_image",
		Arguments: map[string]any{
			"slug":   "timeout-post",
			"prompt": "timeout test",
		},
	})
	// timeout: transport error or IsError both acceptable
	if err != nil {
		return
	}
	if !res.IsError {
		t.Fatal("expected tool to return error on timeout, got success")
	}
}

func TestGenerateFeaturedImageAlwaysRegistered(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()
	// No image_gen_url — tool must still be registered (uses local renderer).

	session, done := newTestServer(t, cfg)
	defer done()

	ctx := context.Background()
	result, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range result.Tools {
		if tool.Name == "generate_hero_image" {
			return
		}
	}
	t.Fatal("generate_hero_image not found in tools list — tool must always be registered")
}

func TestGenerateFeaturedImageLocalRender(t *testing.T) {
	hugoRoot := t.TempDir()
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = hugoRoot
	// No image_gen_url → must use local renderer.
	// No background photos in tempdir → falls back to gradient.

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "generate_hero_image", map[string]any{
		"slug":  "my-post",
		"title": "Hello World from Local Renderer",
		"tags":  []string{"go", "hugo"},
		"style": "tech",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}

	expectedPath := filepath.Join(hugoRoot, "static", "images", "my-post-featured.jpg")
	info, statErr := os.Stat(expectedPath)
	if statErr != nil {
		t.Fatalf("expected file not found at %s: %v", expectedPath, statErr)
	}
	if info.Size() < 1000 {
		t.Fatalf("rendered JPEG suspiciously small: %d bytes", info.Size())
	}
}

func TestGenerateFeaturedImageWriteErrorIsActionable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF})
	}))
	defer srv.Close()

	// Images now go to {HugoRoot}/static/images/ — make hugoRoot read-only to trigger write error.
	hugoRoot := filepath.Join(t.TempDir(), "readonly")
	if err := os.MkdirAll(hugoRoot, 0o555); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	defer func() { _ = os.Chmod(hugoRoot, 0o755) }()

	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = hugoRoot
	cfg.ImageGenURL = srv.URL

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "generate_hero_image", map[string]any{
		"slug":   "test-post",
		"prompt": "test prompt",
	})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected write error when site_root is not writable")
	}
	text := resultText(res)
	for _, want := range []string{"write_error", "target_directory", "target_path", "ReadWritePaths"} {
		if !strings.Contains(text, want) {
			t.Fatalf("error text %q does not contain %q", text, want)
		}
	}
}

func TestGenerateFeaturedImageDescriptionWhenConfigured(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HugoRoot = t.TempDir()
	cfg.ImageGenURL = "https://example.test/gen"

	session, done := newTestServer(t, cfg)
	defer done()

	ctx := context.Background()
	result, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range result.Tools {
		if tool.Name == "generate_hero_image" {
			if strings.Contains(tool.Description, "not configured") {
				t.Errorf("description should not contain 'not configured' when configured, got: %q", tool.Description)
			}
			return
		}
	}
	t.Fatal("generate_hero_image not found in tools list")
}

func TestGenerateFeaturedImage_TraversalSlug(t *testing.T) {
	siteRoot := t.TempDir()
	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.ImageGenURL = "http://127.0.0.1:0" // unreachable; validation must fire first

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "generate_hero_image", map[string]any{
		"slug":   "../../etc/passwd",
		"prompt": "traversal test",
	})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for traversal slug, got success")
	}
	text := resultText(res)
	if !strings.Contains(text, "invalid_params") {
		t.Fatalf("error text %q does not contain 'invalid_params'", text)
	}
	m := decodeStructuredResult(t, res)
	errors, ok := m["errors"].([]any)
	if !ok || len(errors) != 1 {
		t.Fatalf("structured errors = %#v", m["errors"])
	}
	err0 := errors[0].(map[string]any)
	if got := err0["code"]; got != "invalid_params" {
		t.Fatalf("generate_hero_image error code = %v, want invalid_params", got)
	}
	if got := err0["field"]; got != "slug" {
		t.Fatalf("generate_hero_image error field = %v, want slug", got)
	}
}

// TestGenerateFeaturedImage_SymlinkedImagesDir_APIMode verifies that
// generate_hero_image (API mode) fails closed when static/images is
// symlinked to a directory outside hugo_root. Uses RejectSymlinks=false to
// confirm the fix forces detection regardless of the operator config setting
// (#234).
func TestGenerateFeaturedImage_SymlinkedImagesDir_APIMode(t *testing.T) {
	fakeBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(fakeBytes)
	}))
	defer srv.Close()

	hugoRoot := t.TempDir()
	outside := t.TempDir()

	// Create static/ but make static/images a symlink to outside.
	staticDir := filepath.Join(hugoRoot, "static")
	if err := os.MkdirAll(staticDir, 0o755); err != nil {
		t.Fatalf("MkdirAll static: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(staticDir, "images")); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	cfg := config.Default()
	cfg.HugoRoot = hugoRoot
	cfg.SiteRoot = t.TempDir()
	cfg.ImageGenURL = srv.URL
	cfg.RejectSymlinks = false // deliberately off to prove the fix forces detection

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "generate_hero_image", map[string]any{
		"slug":   "my-post",
		"prompt": "test prompt",
	})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error when static/images is a symlink, got success")
	}
	// Verify no file escaped to the outside directory.
	if _, statErr := os.Stat(filepath.Join(outside, "my-post-featured.jpg")); !os.IsNotExist(statErr) {
		t.Error("image was written to symlink target — hugo_root escape not prevented")
	}
}

// TestGenerateFeaturedImage_SymlinkedImagesDir_LocalRender verifies that
// generate_hero_image (local render mode) fails closed when static/images
// is symlinked outside hugo_root, even when RejectSymlinks=false (#234).
func TestGenerateFeaturedImage_SymlinkedImagesDir_LocalRender(t *testing.T) {
	hugoRoot := t.TempDir()
	outside := t.TempDir()

	staticDir := filepath.Join(hugoRoot, "static")
	if err := os.MkdirAll(staticDir, 0o755); err != nil {
		t.Fatalf("MkdirAll static: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(staticDir, "images")); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	cfg := config.Default()
	cfg.HugoRoot = hugoRoot
	cfg.SiteRoot = t.TempDir()
	cfg.RejectSymlinks = false // deliberately off to prove the fix forces detection
	// No ImageGenURL → local render path.

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "generate_hero_image", map[string]any{
		"slug":  "my-post",
		"title": "My Post",
	})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error when static/images is a symlink (local render), got success")
	}
	// Verify no file escaped to the outside directory.
	if _, statErr := os.Stat(filepath.Join(outside, "my-post-featured.jpg")); !os.IsNotExist(statErr) {
		t.Error("image was written to symlink target — hugo_root escape not prevented")
	}
}

// TestGenerateFeaturedImage_NormalPath_StillSucceeds verifies that the
// symlink-detection fix does not break the normal (non-symlinked) path (#234).
func TestGenerateFeaturedImage_NormalPath_StillSucceeds(t *testing.T) {
	hugoRoot := t.TempDir()
	cfg := config.Default()
	cfg.HugoRoot = hugoRoot
	cfg.SiteRoot = t.TempDir()
	cfg.RejectSymlinks = false // prove fix works regardless of config

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "generate_hero_image", map[string]any{
		"slug":  "normal-post",
		"title": "Normal Post",
	})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("normal path must succeed after fix; got error: %s", resultText(res))
	}

	expectedPath := filepath.Join(hugoRoot, "static", "images", "normal-post-featured.jpg")
	if _, statErr := os.Stat(expectedPath); statErr != nil {
		t.Fatalf("expected file at %s: %v", expectedPath, statErr)
	}
}
