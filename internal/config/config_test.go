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
