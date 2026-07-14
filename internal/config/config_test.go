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
	if cfg.Transport != "stdio" {
		t.Fatalf("want stdio, got %s", cfg.Transport)
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
