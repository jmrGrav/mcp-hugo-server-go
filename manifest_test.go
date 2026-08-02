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
	Icon            string `json:"icon"`
	Author          struct {
		Name string `json:"name"`
	} `json:"author"`
	Server struct {
		Type      string `json:"type"`
		MCPConfig struct {
			Command           string            `json:"command"`
			Env               map[string]string `json:"env"`
			PlatformOverrides map[string]struct {
				Command string `json:"command"`
			} `json:"platform_overrides"`
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
	if strings.HasSuffix(m.Server.MCPConfig.Command, ".exe") {
		t.Error("server.mcp_config.command (the default/darwin+linux command) must not end in .exe — that belongs only in the win32 platform_overrides entry")
	}

	// Claude Desktop only runs on macOS/Windows, but the default command
	// above is the darwin (no extension) binary — win32 needs its own
	// override or every Windows install would try to exec a filename with
	// no .exe suffix and fail immediately.
	win32, ok := m.Server.MCPConfig.PlatformOverrides["win32"]
	if !ok {
		t.Error("server.mcp_config.platform_overrides is missing a \"win32\" entry")
	} else if !strings.HasSuffix(win32.Command, ".exe") {
		t.Errorf("server.mcp_config.platform_overrides.win32.command = %q, want a .exe suffix", win32.Command)
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

	// icon is a relative path per the real MCPB spec ("Path to a png icon
	// file, either relative in the package or a https:// url") — assert it
	// resolves to a real file in this repo rather than a dangling reference.
	if strings.TrimSpace(m.Icon) == "" {
		t.Error("manifest.json: icon is empty")
	} else if !strings.HasPrefix(m.Icon, "https://") {
		if _, err := os.Stat(m.Icon); err != nil {
			t.Errorf("manifest.json: icon %q does not resolve to a file in this repo: %v", m.Icon, err)
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
