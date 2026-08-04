package admin_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestListPreviewsAndRevokePreview(t *testing.T) {
	hugoDir := writeMockHugoForPreview(t, "preview marker content")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	cfg := fixtureAdminConfig(t)
	session, _, done := newCreatePreviewServer(t, cfg)
	defer done()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_preview", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("create_preview CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("create_preview returned error: %s", resultText(res))
	}
	data := decodeStructuredResult(t, res)["data"].(map[string]any)
	previewID := data["preview_id"].(string)

	listRes, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_previews", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("list_previews CallTool error: %v", err)
	}
	if listRes.IsError {
		t.Fatalf("list_previews returned error: %s", resultText(listRes))
	}
	listData := decodeStructuredResult(t, listRes)["data"].(map[string]any)
	if got := listData["configured_count"]; got != float64(1) {
		t.Fatalf("configured_count = %v, want 1", got)
	}
	previews := listData["previews"].([]any)
	if len(previews) != 1 {
		t.Fatalf("previews = %#v, want one preview", previews)
	}
	item := previews[0].(map[string]any)
	if got := item["preview_id"]; got != previewID {
		t.Fatalf("preview_id = %v, want %q", got, previewID)
	}
	if got := item["url"]; !strings.HasSuffix(got.(string), "/preview/"+previewID+"/") {
		t.Fatalf("url = %q, want clean preview URL", got)
	}

	revokeRes, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "revoke_preview",
		Arguments: map[string]any{"preview_id": previewID},
	})
	if err != nil {
		t.Fatalf("revoke_preview CallTool error: %v", err)
	}
	if revokeRes.IsError {
		t.Fatalf("revoke_preview returned error: %s", resultText(revokeRes))
	}
	revokeData := decodeStructuredResult(t, revokeRes)["data"].(map[string]any)
	if got := revokeData["status"]; got != "revoked" {
		t.Fatalf("status = %v, want revoked", got)
	}
}

func TestRevokeAllPreviews(t *testing.T) {
	hugoDir := writeMockHugoForPreview(t, "preview marker content")
	t.Setenv("PATH", hugoDir+":"+os.Getenv("PATH"))

	cfg := fixtureAdminConfig(t)
	session, _, done := newCreatePreviewServer(t, cfg)
	defer done()

	for i := 0; i < 2; i++ {
		res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_preview", Arguments: map[string]any{}})
		if err != nil {
			t.Fatalf("create_preview CallTool error: %v", err)
		}
		if res.IsError {
			t.Fatalf("create_preview returned error: %s", resultText(res))
		}
	}

	revokeAllRes, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "revoke_all_previews", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("revoke_all_previews CallTool error: %v", err)
	}
	if revokeAllRes.IsError {
		t.Fatalf("revoke_all_previews returned error: %s", resultText(revokeAllRes))
	}
	data := decodeStructuredResult(t, revokeAllRes)["data"].(map[string]any)
	if got := data["revoked_count"]; got != float64(2) {
		t.Fatalf("revoked_count = %v, want 2", got)
	}
}

func fixtureAdminConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.HugoRoot = t.TempDir()
	cfg.SiteRoot = t.TempDir()
	return cfg
}
