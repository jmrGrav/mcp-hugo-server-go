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

func TestGenerateFeaturedImage_Success(t *testing.T) {
	fakeBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(fakeBytes)
	}))
	defer srv.Close()

	siteRoot := t.TempDir()
	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.ImageGenURL = srv.URL
	cfg.ImageGenKey = "test-key"

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "generate_featured_image", map[string]any{
		"slug":   "my-post",
		"prompt": "a scenic mountain landscape",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %s", resultText(res))
	}

	expectedPath := filepath.Join(siteRoot, "images", "featured", "my-post.jpg")
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
}

func TestGenerateFeaturedImage_MIMEReject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintln(w, "<html>not an image</html>")
	}))
	defer srv.Close()

	siteRoot := t.TempDir()
	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.ImageGenURL = srv.URL

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "generate_featured_image", map[string]any{
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
		Name: "generate_featured_image",
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

func TestGenerateFeaturedImage_TraversalSlug(t *testing.T) {
	siteRoot := t.TempDir()
	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.ImageGenURL = "http://127.0.0.1:0" // unreachable; validation must fire first

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "generate_featured_image", map[string]any{
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
}
