package mcphugoserver_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

// manifestJSON mirrors the subset of the MCPB manifest.json schema this
// test cares about validating — not the full spec, just the fields whose
// correctness this repo can actually assert (required top-level fields,
// server.type=binary shape, and that mcp_config.env references the real
// MCP_HUGO_* env var names rather than stale/hand-typed duplicates).
type manifestJSON struct {
	ManifestVersion string `json:"manifest_version"`
	Name            string `json:"name"`
	Version         string `json:"version"`
	Description     string `json:"description"`
	Author          struct {
		Name string `json:"name"`
	} `json:"author"`
	Server struct {
		Type      string `json:"type"`
		MCPConfig struct {
			Command string            `json:"command"`
			Env     map[string]string `json:"env"`
		} `json:"mcp_config"`
	} `json:"server"`
	UserConfig      map[string]json.RawMessage `json:"user_config"`
	PrivacyPolicies []string                   `json:"privacy_policies"`
}

// TestManifestJSONIsValid is a draft-stage check: this cannot verify the
// .mcpb bundle installs in Claude Desktop (not runnable from this Linux
// dev environment), but it can catch the class of mistake a hand-edited
// static JSON file is prone to — missing required fields, a typo'd env var
// name that silently stops user_config from reaching the binary, or an
// empty/missing privacy policy — before either ships in a real submission.
func TestManifestJSONIsValid(t *testing.T) {
	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	var m manifestJSON
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("manifest.json is not valid JSON: %v", err)
	}

	// Required top-level fields per the MCPB MANIFEST.md spec.
	for name, val := range map[string]string{
		"manifest_version": m.ManifestVersion,
		"name":             m.Name,
		"version":          m.Version,
		"description":      m.Description,
		"author.name":      m.Author.Name,
	} {
		if strings.TrimSpace(val) == "" {
			t.Errorf("manifest.json: required field %q is empty", name)
		}
	}

	if m.Server.Type != "binary" {
		t.Errorf("server.type = %q, want %q (a Go binary needs no Node.js shim per the real MCPB spec)", m.Server.Type, "binary")
	}
	if strings.TrimSpace(m.Server.MCPConfig.Command) == "" {
		t.Error("server.mcp_config.command is empty")
	}

	// The whole point of mcp_config.env is to deliver user_config values to
	// the process via the exact MCP_HUGO_* names applyEnvOverlay actually
	// reads (internal/config/config_env_overlay.go) — a stale/typo'd name
	// here would silently break every install using this manifest.
	wantEnvVars := []string{
		config.EnvSiteRoot,
		config.EnvHugoRoot,
		config.EnvContentRoot,
		config.EnvSiteURL,
		config.EnvSiteName,
	}
	for _, envVar := range wantEnvVars {
		if _, ok := m.Server.MCPConfig.Env[envVar]; !ok {
			t.Errorf("server.mcp_config.env is missing %q — user_config values referencing this name would never reach the binary", envVar)
		}
	}

	// user_config must define an entry for every required env var above
	// (minus site_name, which the overlay treats as optional) so Claude
	// Desktop actually prompts the user for it.
	for _, key := range []string{"site_root", "hugo_root", "content_root", "site_url"} {
		if _, ok := m.UserConfig[key]; !ok {
			t.Errorf("user_config is missing %q", key)
		}
	}

	if len(m.PrivacyPolicies) == 0 {
		t.Error("privacy_policies is empty — required for a connector that touches the filesystem and can be configured to call external services (Cloudflare/IndexNow/Google Indexing/webhooks)")
	}
	for _, u := range m.PrivacyPolicies {
		if !strings.HasPrefix(u, "https://") {
			t.Errorf("privacy_policies entry %q is not an https:// URL", u)
		}
	}
}
