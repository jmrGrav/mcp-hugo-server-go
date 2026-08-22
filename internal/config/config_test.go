package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func TestDefaultConfig(t *testing.T) {
	cfg := config.Default()
	if cfg.HTTPBindPort != 8088 {
		t.Fatalf("want port 8088, got %d", cfg.HTTPBindPort)
	}
	if cfg.HTTPBindAddr != "127.0.0.1" {
		t.Fatalf("want 127.0.0.1, got %s", cfg.HTTPBindAddr)
	}
	if cfg.Transport != "http" {
		t.Fatalf("want http, got %s", cfg.Transport)
	}
	if cfg.GitBaseline.Remote != "origin" {
		t.Fatalf("want default git baseline remote origin, got %q", cfg.GitBaseline.Remote)
	}
	if cfg.GitBaseline.Branch != "main" {
		t.Fatalf("want default git baseline branch main, got %q", cfg.GitBaseline.Branch)
	}
	if cfg.GitBaseline.Mode != "auto" {
		t.Fatalf("want default git baseline mode auto, got %q", cfg.GitBaseline.Mode)
	}
	if cfg.IdempotencyTTLSeconds != 900 {
		t.Fatalf("want default idempotency_ttl_seconds 900 (15 minutes), got %d", cfg.IdempotencyTTLSeconds)
	}
}

// TestLoadConfigIdempotencyTTL is a regression test for #616: the
// idempotency-key retention window (backing create_page/update_page/
// delete_page/upload_page_asset/delete_page_asset and get_mutation_status)
// must be configurable via idempotency_ttl_seconds instead of the
// previously-hardcoded 15 minutes.
func TestLoadConfigIdempotencyTTL(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
	f.WriteString("idempotency_ttl_seconds: 3600\n")
	f.Close()
	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IdempotencyTTLSeconds != 3600 {
		t.Fatalf("want idempotency_ttl_seconds 3600, got %d", cfg.IdempotencyTTLSeconds)
	}
}

// TestLoadConfigClampsNonPositiveIdempotencyTTL guards against a config file
// zeroing or negating the idempotency TTL, which would make every mutation
// call effectively non-idempotent (no replay protection window) rather than
// evading protection outright — still a misconfiguration that must not be
// allowed to silently take effect. Mirrors
// TestLoadConfigClampsNonPositiveMutationRateLimits' treatment of the same
// class of issue for rate limits (#616).
func TestLoadConfigClampsNonPositiveIdempotencyTTL(t *testing.T) {
	for _, seconds := range []string{"0", "-5"} {
		f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
		f.WriteString("idempotency_ttl_seconds: " + seconds + "\n")
		f.Close()
		cfg, err := config.Load(f.Name())
		if err != nil {
			t.Fatalf("idempotency_ttl_seconds: %s: %v", seconds, err)
		}
		if cfg.IdempotencyTTLSeconds != 900 {
			t.Fatalf("idempotency_ttl_seconds: %s: want clamped to default 900, got %d", seconds, cfg.IdempotencyTTLSeconds)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
	f.WriteString("http_bind_port: 9000\nsite_root: /tmp/site\n")
	f.Close()
	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPBindPort != 9000 {
		t.Fatalf("want 9000, got %d", cfg.HTTPBindPort)
	}
	if cfg.SiteRoot != "/tmp/site" {
		t.Fatalf("want /tmp/site, got %s", cfg.SiteRoot)
	}
}

func TestLoadConfigContentRoot(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
	f.WriteString("content_root: /tmp/content\n")
	f.Close()
	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ContentRoot != "/tmp/content" {
		t.Fatalf("want /tmp/content, got %s", cfg.ContentRoot)
	}
}

func TestLoadConfigClampsNonPositiveMutationRateLimits(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
	f.WriteString("rate_limit:\n  destructive_per_min: 0\n  create_update_per_min: -1\n")
	f.Close()
	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RateLimit.DestructivePerMin != 5 {
		t.Fatalf("want destructive_per_min clamped to default 5, got %d", cfg.RateLimit.DestructivePerMin)
	}
	if cfg.RateLimit.CreateUpdatePerMin != 60 {
		t.Fatalf("want create_update_per_min clamped to default 60, got %d", cfg.RateLimit.CreateUpdatePerMin)
	}
}

// clampPreviewLimits (#871) is a DoS guard: a zeroed/negative config value
// must never be read as "unlimited" for either the per-caller preview count
// or the global preview-disk cap, or a misconfiguration reintroduces the
// unbounded preview-accumulation/disk-exhaustion risk create_preview's caps
// exist to prevent. Mirrors TestLoadConfigClampsNonPositiveMutationRateLimits.
func TestLoadConfigClampsNonPositivePreviewLimits(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
	f.WriteString("preview_max_per_caller: 0\npreview_max_disk_bytes: -1\n")
	f.Close()
	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PreviewMaxPerCaller != config.DefaultPreviewMaxPerCaller {
		t.Fatalf("want preview_max_per_caller clamped to default %d, got %d", config.DefaultPreviewMaxPerCaller, cfg.PreviewMaxPerCaller)
	}
	if cfg.PreviewMaxDiskBytes != config.DefaultPreviewMaxDiskBytes {
		t.Fatalf("want preview_max_disk_bytes clamped to default %d, got %d", config.DefaultPreviewMaxDiskBytes, cfg.PreviewMaxDiskBytes)
	}
}

// A positive, deliberately-configured value must pass through unclamped —
// the guard is against non-positive misconfiguration only, not a general
// override of operator intent.
func TestLoadConfigPreservesPositivePreviewLimits(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
	f.WriteString("preview_max_per_caller: 3\npreview_max_disk_bytes: 1048576\n")
	f.Close()
	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PreviewMaxPerCaller != 3 {
		t.Fatalf("want preview_max_per_caller preserved at 3, got %d", cfg.PreviewMaxPerCaller)
	}
	if cfg.PreviewMaxDiskBytes != 1048576 {
		t.Fatalf("want preview_max_disk_bytes preserved at 1048576, got %d", cfg.PreviewMaxDiskBytes)
	}
}

func TestLoadConfigGitBaseline(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
	f.WriteString("git_baseline:\n  mode: configured\n  repo_path: /srv/hugo-arleo.eu\n  branch: release\n  remote: backup\n")
	f.Close()
	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitBaseline.Mode != "configured" {
		t.Fatalf("want git baseline mode configured, got %q", cfg.GitBaseline.Mode)
	}
	if cfg.GitBaseline.RepoPath != "/srv/hugo-arleo.eu" {
		t.Fatalf("want git baseline repo_path /srv/hugo-arleo.eu, got %q", cfg.GitBaseline.RepoPath)
	}
	if cfg.GitBaseline.Branch != "release" {
		t.Fatalf("want git baseline branch release, got %q", cfg.GitBaseline.Branch)
	}
	if cfg.GitBaseline.Remote != "backup" {
		t.Fatalf("want git baseline remote backup, got %q", cfg.GitBaseline.Remote)
	}
}

func TestLoadConfigGitBaselineValidation(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{
			name: "invalid mode",
			yaml: "git_baseline:\n  mode: maybe\n",
		},
		{
			name: "configured without repo_path",
			yaml: "git_baseline:\n  mode: configured\n",
		},
		{
			name: "configured with relative repo_path",
			yaml: "git_baseline:\n  mode: configured\n  repo_path: backups/hugo-arleo.eu\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
			f.WriteString(tc.yaml)
			f.Close()
			if _, err := config.Load(f.Name()); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPBindPort != 8088 {
		t.Fatalf("want 8088, got %d", cfg.HTTPBindPort)
	}
}

func TestLoadNonexistentFileErrors(t *testing.T) {
	_, err := config.Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestOAuthEnabledWithoutIssuerFails(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
	f.WriteString("oauth:\n  enabled: true\n")
	f.Close()
	_, err := config.Load(f.Name())
	if err == nil {
		t.Fatal("expected error: oauth.enabled requires oauth.issuer")
	}
}

func TestOAuthEnabledWithIssuerSucceeds(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
	f.WriteString("oauth:\n  enabled: true\n  issuer: https://mcp.test\n")
	f.Close()
	_, err := config.Load(f.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrivateIPHookRejected(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
	f.WriteString("post_build_hooks:\n  - http://127.0.0.1/hook\n")
	f.Close()
	_, err := config.Load(f.Name())
	if err == nil {
		t.Fatal("expected error: hook URL with localhost/private IP should be rejected")
	}
}

func TestLinkLocalIPHookRejected(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
	f.WriteString("post_build_hooks:\n  - http://169.254.169.254/latest/meta-data/\n")
	f.Close()
	_, err := config.Load(f.Name())
	if err == nil {
		t.Fatal("expected error: link-local hook URL should be rejected")
	}
}

func TestNonHTTPHookRejected(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
	f.WriteString("post_build_hooks:\n  - file:///etc/passwd\n")
	f.Close()
	_, err := config.Load(f.Name())
	if err == nil {
		t.Fatal("expected error: file:// scheme should be rejected")
	}
}

func TestLoadInvalidTransport(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
	f.WriteString("transport: websocket\n")
	f.Close()
	_, err := config.Load(f.Name())
	if err == nil {
		t.Fatal("expected error for invalid transport")
	}
}

func TestExternalURLValidationEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{
			name: "missing host",
			yaml: "image_gen_url: https:///no-host\n",
		},
		{
			name: "private ip literal",
			yaml: "image_gen_url: http://127.0.0.1/image\n",
		},
		{
			name: "invalid scheme",
			yaml: "image_gen_url: ftp://example.com/image\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
			f.WriteString(tc.yaml)
			f.Close()
			_, err := config.Load(f.Name())
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

// TestLoadFileWithNoEnvVarsIsUnchanged is the discriminating regression test
// for the MCP_HUGO_*-namespaced env overlay (#782 Phase 2, MCPB support): a
// config.yaml that already sets its fields, loaded in an environment with
// none of the overlay's env vars present, must produce a byte-for-byte
// identical Config to what config.Load returned before the overlay existed.
// This is what proves every real HTTP deployment (which always sets these
// fields explicitly in its config.yaml) can't be silently affected by this
// change — it exists purely to let a file-less MCPB/stdio install work.
func TestLoadFileWithNoEnvVarsIsUnchanged(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
	f.WriteString("site_root: /tmp/site\nhugo_root: /tmp/hugo\ncontent_root: /tmp/content\nsite_url: https://example.test\nsite_name: Example\n")
	f.Close()

	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SiteRoot != "/tmp/site" || cfg.HugoRoot != "/tmp/hugo" || cfg.ContentRoot != "/tmp/content" ||
		cfg.SiteURL != "https://example.test" || cfg.SiteName != "Example" {
		t.Fatalf("file values were not preserved: %+v", cfg)
	}
}

// TestLoadEnvOverlayNeverOverridesFileValue proves file-wins precedence:
// even with every overlay env var set, a config.yaml that already sets its
// own value for that field keeps the file's value.
func TestLoadEnvOverlayNeverOverridesFileValue(t *testing.T) {
	t.Setenv("MCP_HUGO_SITE_ROOT", "/env/site")
	t.Setenv("MCP_HUGO_HUGO_ROOT", "/env/hugo")
	t.Setenv("MCP_HUGO_CONTENT_ROOT", "/env/content")
	t.Setenv("MCP_HUGO_SITE_URL", "https://env.test")
	t.Setenv("MCP_HUGO_SITE_NAME", "EnvName")

	f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
	f.WriteString("site_root: /file/site\nhugo_root: /file/hugo\ncontent_root: /file/content\nsite_url: https://file.test\nsite_name: FileName\n")
	f.Close()

	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SiteRoot != "/file/site" || cfg.HugoRoot != "/file/hugo" || cfg.ContentRoot != "/file/content" ||
		cfg.SiteURL != "https://file.test" || cfg.SiteName != "FileName" {
		t.Fatalf("env overlay must not override values already set by the file: %+v", cfg)
	}
}

// TestLoadEnvOverlayFillsGapsWithNoFile is the MCPB scenario itself: no
// config.yaml at all (empty path, matching MCP_HUGO_SERVER_CONFIG being
// unset), values arrive purely via env — the only channel the MCPB manifest
// spec's ${user_config.*} substitution can inject into.
func TestLoadEnvOverlayFillsGapsWithNoFile(t *testing.T) {
	t.Setenv("MCP_HUGO_SITE_ROOT", "/env/site")
	t.Setenv("MCP_HUGO_HUGO_ROOT", "/env/hugo")
	t.Setenv("MCP_HUGO_CONTENT_ROOT", "/env/content")
	t.Setenv("MCP_HUGO_SITE_URL", "https://env.test")
	t.Setenv("MCP_HUGO_SITE_NAME", "EnvName")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SiteRoot != "/env/site" || cfg.HugoRoot != "/env/hugo" || cfg.ContentRoot != "/env/content" ||
		cfg.SiteURL != "https://env.test" || cfg.SiteName != "EnvName" {
		t.Fatalf("env overlay should fill every field when there's no file: %+v", cfg)
	}
}

func TestHugoUpgradeDefaultsAreDisabledAndOfficial(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HugoUpgrade.Enabled {
		t.Fatal("managed Hugo upgrades must be disabled by default")
	}
	if cfg.HugoUpgrade.ReleaseAPIBaseURL != "https://api.github.com/repos/gohugoio/hugo" {
		t.Fatalf("release API = %q", cfg.HugoUpgrade.ReleaseAPIBaseURL)
	}
	if cfg.HugoUpgrade.MaxDownloadBytes <= 0 || len(cfg.HugoUpgrade.AllowedHosts) == 0 {
		t.Fatalf("unsafe Hugo upgrade defaults: %+v", cfg.HugoUpgrade)
	}
}

func TestHugoUpgradeEnabledRequiresManagedContainedPaths(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"relative paths", "hugo_upgrade:\n  enabled: true\n  managed_dir: relative\n  binary_link: relative/hugo\n"},
		{"link outside root", "hugo_upgrade:\n  enabled: true\n  managed_dir: /srv/mcp-hugo\n  binary_link: /usr/local/bin/hugo\n"},
		{"non HTTPS API", "hugo_upgrade:\n  release_api_base_url: http://api.github.com/repos/gohugoio/hugo\n"},
		{"API host outside allowlist", "hugo_upgrade:\n  release_api_base_url: https://example.test/hugo\n"},
		{"invalid version policy", "hugo_upgrade:\n  enabled: true\n  managed_dir: /srv/mcp-hugo\n  binary_link: /srv/mcp-hugo/current/hugo\n  minimum_version: latest\n"},
		{"reversed version policy", "hugo_upgrade:\n  enabled: true\n  managed_dir: /srv/mcp-hugo\n  binary_link: /srv/mcp-hugo/current/hugo\n  minimum_version: v2.0.0\n  maximum_version: v1.0.0\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := config.Load(path); err == nil {
				t.Fatal("Load succeeded, want fail-closed Hugo upgrade validation")
			}
		})
	}
}

func TestHugoUpgradeManagedConfigurationLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := "hugo_upgrade:\n  enabled: true\n  managed_dir: /srv/mcp-hugo\n  binary_link: /srv/mcp-hugo/current/hugo\n  require_extended: true\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HugoUpgrade.Enabled || cfg.HugoUpgrade.BinaryLink != "/srv/mcp-hugo/current/hugo" {
		t.Fatalf("loaded Hugo upgrade config = %+v", cfg.HugoUpgrade)
	}
}

func TestDefaultRequireDeleteConfirmationIsTrue(t *testing.T) {
	if !config.Default().RequireDeleteConfirmation {
		t.Fatal("config.Default().RequireDeleteConfirmation = false, want true — a fresh install should enforce the delete-confirmation ceremony without an operator opting in")
	}
}

func TestLoadRequireDeleteConfirmationFalseOverridesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("require_delete_confirmation: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RequireDeleteConfirmation {
		t.Fatal("Load with require_delete_confirmation: false left RequireDeleteConfirmation = true, want the explicit opt-out to override the default")
	}
}
