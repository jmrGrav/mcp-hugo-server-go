package admin_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
)

func TestPreviewBuildSucceeds(t *testing.T) {
	dir := writeMockHugo(t, "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	siteRoot := t.TempDir()
	cfg := config.Default()
	cfg.SiteRoot = siteRoot

	session, done := newTestServer(t, cfg)
	defer done()

	res, err := callTool(t, session, "preview_build", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("preview_build returned error: %s", resultText(res))
	}
	text := resultText(res)
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("response not JSON: %v — got %q", err, text)
	}
	if out["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", out["status"])
	}
}

func TestPreviewBuildRegisteredInSiteAdmin(t *testing.T) {
	defs := admin.Defs()
	found := false
	for _, d := range defs {
		if d.Name == "preview_build" {
			found = true
			if d.RequiredScope != "site.admin" {
				t.Fatalf("preview_build scope = %q", d.RequiredScope)
			}
		}
	}
	if !found {
		t.Fatal("preview_build not present in Defs()")
	}
}
