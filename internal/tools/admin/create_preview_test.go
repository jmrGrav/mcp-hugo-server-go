package admin_test

import (
	"context"
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
	admin.RegisterPreviewAccessTools(s, cfg, store, "https://mcp.example.test")

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

	out := decodeStructuredResult(t, res)
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", out["data"])
	}
	if data["build"] != "passed" {
		t.Fatalf("data.build = %v, want passed", data["build"])
	}
	previewID, _ := data["preview_id"].(string)
	if previewID == "" {
		t.Fatal("data.preview_id must not be empty")
	}
	url, _ := data["url"].(string)
	if !strings.HasPrefix(url, "https://mcp.example.test/preview/"+previewID+"/") {
		t.Fatalf("data.url = %q, want prefix https://mcp.example.test/preview/%s/", url, previewID)
	}
	if data["expires_at"] == "" || data["expires_at"] == nil {
		t.Fatal("data.expires_at must not be empty")
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
	if rec.Code != http.StatusFound {
		t.Fatalf("preview entry status = %d, want redirect", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/preview/"+previewID+"/" {
		t.Fatalf("preview redirect Location = %q, want /preview/%s/", got, previewID)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("preview cookies = %v, want one session cookie", cookies)
	}
	req = httptest.NewRequest(http.MethodGet, "/preview/"+previewID+"/", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	store.HTTPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview clean-url serve status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "preview marker content") {
		t.Fatalf("served content = %q, missing marker", rec.Body.String())
	}
}

// TestCreatePreviewNoRootFieldDuplication is a regression test for #573:
// finishes #520's convergence for this tool — preview_id/url/expires_at/
// build must live only under data.*, with no root-level compatibility
// aliases (which #552 originally added when this tool first gained an
// envelope, before #520 established data-only as the convention).
func TestCreatePreviewNoRootFieldDuplication(t *testing.T) {
	hugoDir := writeMockHugoForPreview(t, "preview marker content")
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
	if res.IsError {
		t.Fatalf("create_preview returned error: %s", resultText(res))
	}

	out := decodeStructuredResult(t, res)
	if got := out["success"]; got != true {
		t.Fatalf("success = %v, want true (#573)", got)
	}
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any (#573)", out["data"])
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("meta type = %T, want map[string]any (#573)", out["meta"])
	}
	for _, field := range []string{"preview_id", "url", "expires_at", "build"} {
		if _, present := data[field]; !present {
			t.Fatalf("data.%s missing (#573)", field)
		}
		if _, present := out[field]; present {
			t.Fatalf("root %s = %v, want absent — no more root/data duplication (#573)", field, out[field])
		}
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
	out := decodeStructuredResult(t, res)
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", out["data"])
	}
	if got := data["effective_ttl_seconds"]; got != float64(3600) {
		t.Fatalf("data.effective_ttl_seconds = %v, want 3600", got)
	}
	warnings, _ := out["warnings"].([]any)
	if len(warnings) == 0 {
		t.Fatal("warnings missing, want clamp warning")
	}
	expiresAt, _ := data["expires_at"].(string)
	if expiresAt == "" {
		t.Fatal("data.expires_at must not be empty")
	}
}

func TestCreatePreviewClampsExplicitNonPositiveTTLToMinimum(t *testing.T) {
	hugoDir := writeMockHugoForPreview(t, "marker")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.HugoRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir()

	session, _, done := newCreatePreviewServer(t, cfg)
	defer done()

	for _, ttl := range []int{0, -1} {
		res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_preview", Arguments: map[string]any{"ttl_seconds": ttl}})
		if err != nil {
			t.Fatalf("CallTool error: %v", err)
		}
		if res.IsError {
			t.Fatalf("create_preview returned error: %s", resultText(res))
		}
		out := decodeStructuredResult(t, res)
		data, ok := out["data"].(map[string]any)
		if !ok {
			t.Fatalf("data type = %T, want map[string]any", out["data"])
		}
		if got := data["effective_ttl_seconds"]; got != float64(60) {
			t.Fatalf("ttl_seconds=%d data.effective_ttl_seconds = %v, want 60", ttl, got)
		}
		warnings, _ := out["warnings"].([]any)
		if len(warnings) == 0 {
			t.Fatalf("ttl_seconds=%d warnings missing, want clamp warning", ttl)
		}
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
	out := decodeStructuredResult(t, res)
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", out["data"])
	}
	returnedURL, _ := data["url"].(string)
	parts := strings.Split(strings.TrimPrefix(returnedURL, "https://mcp.example.test/preview/"), "/")
	if len(parts) < 2 {
		t.Fatalf("returned preview URL %q did not contain preview id + token", returnedURL)
	}
	cleanURL := "https://mcp.example.test/preview/" + parts[0] + "/"

	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read captured argv: %v", err)
	}
	argvStr := strings.TrimSpace(string(argv))
	if !strings.Contains(argvStr, "--baseURL "+cleanURL) {
		t.Fatalf("hugo invocation missing --baseURL pointed at the clean preview URL %q; argv = %q", cleanURL, argvStr)
	}
	if strings.Contains(argvStr, "--baseURL "+returnedURL) {
		t.Fatalf("hugo invocation still used the token-bearing entry URL %q as baseURL; argv = %q", returnedURL, argvStr)
	}
	if !strings.Contains(argvStr, "--environment preview") {
		t.Fatalf("hugo invocation missing preview environment flag; argv = %q", argvStr)
	}
	if strings.Contains(argvStr, cfg.SiteURL) {
		t.Fatalf("hugo invocation must not use the public site's baseURL for a preview build; argv = %q", argvStr)
	}
}

// TestCreatePreviewRejectsOverPerCallerCountCap proves the #871 per-caller
// active-preview cap rejects the (N+1)th create with an explicit signal, not a
// silent no-op.
func TestCreatePreviewRejectsOverPerCallerCountCap(t *testing.T) {
	hugoDir := writeMockHugoForPreview(t, "marker")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.HugoRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir()
	cfg.PreviewMaxPerCaller = 1

	session, _, done := newCreatePreviewServer(t, cfg)
	defer done()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_preview", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("first create_preview should succeed: %s", resultText(res))
	}

	res, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_preview", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !res.IsError {
		t.Fatal("second create_preview past the per-caller cap must return an error, not silently succeed")
	}
	if !strings.Contains(resultText(res), "preview_limit_exceeded") {
		t.Fatalf("error must explicitly signal the cap; got %q", resultText(res))
	}
}

// TestCreatePreviewRejectsOverDiskCap proves the #871 global preview-disk cap
// rejects a new create once existing preview builds meet/exceed the budget.
func TestCreatePreviewRejectsOverDiskCap(t *testing.T) {
	hugoDir := writeMockHugoForPreview(t, "marker content that occupies some bytes on disk")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.HugoRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir()
	cfg.PreviewMaxDiskBytes = 1 // any existing preview usage trips the cap

	session, _, done := newCreatePreviewServer(t, cfg)
	defer done()

	// First create sees 0 bytes used (0 >= 1 is false) and proceeds.
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_preview", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("first create_preview should succeed: %s", resultText(res))
	}

	// Second create sees the first build's bytes on disk and is refused.
	res, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_preview", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !res.IsError {
		t.Fatal("create_preview over the disk cap must return an error, not silently succeed")
	}
	if !strings.Contains(resultText(res), "preview_disk_limit_exceeded") {
		t.Fatalf("error must explicitly signal the disk cap; got %q", resultText(res))
	}
}

// TestListPreviewsSurfacesCapsAndUsage proves list_previews reports current
// usage against the caps (#871 AC5 "enforced and surfaced").
func TestListPreviewsSurfacesCapsAndUsage(t *testing.T) {
	hugoDir := writeMockHugoForPreview(t, "marker")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.HugoRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir()
	cfg.PreviewMaxPerCaller = 7

	session, _, done := newCreatePreviewServer(t, cfg)
	defer done()

	if res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_preview", Arguments: map[string]any{}}); err != nil || res.IsError {
		t.Fatalf("create_preview failed: err=%v res=%v", err, res)
	}

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_previews", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_previews returned error: %s", resultText(res))
	}
	out := decodeStructuredResult(t, res)
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map[string]any", out["data"])
	}
	if got := data["caller_active_count"]; got != float64(1) {
		t.Fatalf("caller_active_count = %v, want 1", got)
	}
	if got := data["max_previews_per_caller"]; got != float64(7) {
		t.Fatalf("max_previews_per_caller = %v, want 7 (configured cap surfaced)", got)
	}
	if got, ok := data["preview_disk_used_bytes"].(float64); !ok || got <= 0 {
		t.Fatalf("preview_disk_used_bytes = %v, want a positive byte count", data["preview_disk_used_bytes"])
	}
	if _, present := data["preview_disk_max_bytes"]; !present {
		t.Fatal("preview_disk_max_bytes must be surfaced")
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
