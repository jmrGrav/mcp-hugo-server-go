package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/previewstore"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// writeMockHugoForPreview writes a mock `hugo` binary that, when invoked
// with `--destination <dir>`, writes a marker index.html into that
// directory — so create_preview's isolation and the resulting preview URL
// can both be verified against real files rather than just exit codes.
func writeMockHugoForPreview(t *testing.T, marker string) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
dest=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "--destination" ]; then
    dest="$arg"
  fi
  prev="$arg"
done
if [ -z "$dest" ]; then
  echo "missing --destination" >&2
  exit 1
fi
mkdir -p "$dest"
echo "` + marker + `" > "$dest/index.html"
exit 0
`
	p := filepath.Join(dir, "hugo")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write mock hugo: %v", err)
	}
	return dir
}

// writeMockHugoCapturingArgv writes a mock `hugo` binary that records its
// full argv to argvFile and writes a marker index.html into --destination.
// Used to prove create_preview passes --baseURL pointed at the preview's
// own mount, not left at the site's real baseURL/root-relative — without
// that, a browser opening the preview would request assets against the
// wrong host/path and the preview would render broken (issue #345 review).
func writeMockHugoCapturingArgv(t *testing.T, argvFile string) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
echo "$@" > "` + argvFile + `"
dest=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "--destination" ]; then
    dest="$arg"
  fi
  prev="$arg"
done
mkdir -p "$dest"
echo "marker" > "$dest/index.html"
exit 0
`
	p := filepath.Join(dir, "hugo")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write mock hugo: %v", err)
	}
	return dir
}

func newCreatePreviewServer(t *testing.T, cfg config.Config) (*mcp.ClientSession, *previewstore.Store, func()) {
	t.Helper()
	store := previewstore.New()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	admin.RegisterCreatePreview(s, cfg, store, "https://mcp.example.test")

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
	return session, store, func() { _ = session.Close() }
}

func TestCreatePreviewBuildsIsolatedDirAndServesViaStore(t *testing.T) {
	hugoDir := writeMockHugoForPreview(t, "preview marker content")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.HugoRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir() // the public site — must remain untouched

	session, store, done := newCreatePreviewServer(t, cfg)
	defer done()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_preview", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("create_preview returned error: %s", resultText(res))
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(resultText(res)), &out); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if out["build"] != "passed" {
		t.Fatalf("build = %v, want passed", out["build"])
	}
	previewID, _ := out["preview_id"].(string)
	if previewID == "" {
		t.Fatal("preview_id must not be empty")
	}
	url, _ := out["url"].(string)
	if !strings.HasPrefix(url, "https://mcp.example.test/preview/"+previewID+"/") {
		t.Fatalf("url = %q, want prefix https://mcp.example.test/preview/%s/", url, previewID)
	}
	if out["expires_at"] == "" || out["expires_at"] == nil {
		t.Fatal("expires_at must not be empty")
	}

	// The public site directory must remain untouched by the preview build.
	entries, _ := os.ReadDir(cfg.SiteRoot)
	if len(entries) != 0 {
		t.Fatalf("cfg.SiteRoot must remain empty after create_preview, got %v", entries)
	}

	// The URL's token must actually work against the shared store.
	token := strings.TrimPrefix(url, "https://mcp.example.test/preview/"+previewID+"/")
	token = strings.TrimSuffix(token, "/")
	req := httptest.NewRequest(http.MethodGet, "/preview/"+previewID+"/"+token+"/", nil)
	rec := httptest.NewRecorder()
	store.HTTPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview serve status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "preview marker content") {
		t.Fatalf("served content = %q, missing marker", rec.Body.String())
	}
}

func TestCreatePreviewClampsTTLToConfiguredBounds(t *testing.T) {
	hugoDir := writeMockHugoForPreview(t, "marker")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.HugoRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir()

	session, _, done := newCreatePreviewServer(t, cfg)
	defer done()

	// A wildly excessive TTL request must not result in an unbounded exposure window.
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_preview", Arguments: map[string]any{"ttl_seconds": 999999}})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("create_preview returned error: %s", resultText(res))
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(resultText(res)), &out); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	expiresAt, _ := out["expires_at"].(string)
	if expiresAt == "" {
		t.Fatal("expires_at must not be empty")
	}
}

func TestCreatePreviewPassesBaseURLPointedAtOwnMount(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "argv.txt")
	hugoDir := writeMockHugoCapturingArgv(t, argvFile)
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.HugoRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir()
	cfg.SiteURL = "https://the-real-public-site.example" // must NOT end up as --baseURL

	session, _, done := newCreatePreviewServer(t, cfg)
	defer done()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_preview", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("create_preview returned error: %s", resultText(res))
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(resultText(res)), &out); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	returnedURL, _ := out["url"].(string)

	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read captured argv: %v", err)
	}
	argvStr := strings.TrimSpace(string(argv))
	if !strings.Contains(argvStr, "--baseURL "+returnedURL) {
		t.Fatalf("hugo invocation missing --baseURL pointed at the returned preview URL %q; argv = %q", returnedURL, argvStr)
	}
	if strings.Contains(argvStr, cfg.SiteURL) {
		t.Fatalf("hugo invocation must not use the public site's baseURL for a preview build; argv = %q", argvStr)
	}
}

func TestCreatePreviewBuildFailureReturnsError(t *testing.T) {
	hugoDir := writeMockHugo(t, "#!/bin/sh\necho 'Error: TOML parse error' >&2\nexit 1\n")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.HugoRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir()

	session, _, done := newCreatePreviewServer(t, cfg)
	defer done()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_preview", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("create_preview on hugo failure: want error, got success")
	}
}
