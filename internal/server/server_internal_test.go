package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	toolsanon "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/anonymous"
	toolsread "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func TestOpenStoreBranches(t *testing.T) {
	if store, err := openStore(config.OAuthConfig{}); err != nil || store == nil {
		t.Fatalf("openStore(memory) = %v, %v", store, err)
	}

	if _, err := openStore(config.OAuthConfig{StorageBackend: "json"}); err == nil {
		t.Fatal("openStore(json) should require storage_path")
	}
	if _, err := openStore(config.OAuthConfig{StorageBackend: "sqlite"}); err == nil {
		t.Fatal("openStore(sqlite) should require storage_path")
	}

	jsonPath := filepath.Join(t.TempDir(), "tokens.json")
	jsonStore, err := openStore(config.OAuthConfig{StorageBackend: "json", StoragePath: jsonPath})
	if err != nil {
		t.Fatalf("openStore(json path) error = %v", err)
	}
	_ = jsonStore.Close()

	sqlitePath := filepath.Join(t.TempDir(), "tokens.sqlite")
	sqliteStore, err := openStore(config.OAuthConfig{StorageBackend: "sqlite", StoragePath: sqlitePath})
	if err != nil {
		t.Fatalf("openStore(sqlite path) error = %v", err)
	}
	_ = sqliteStore.Close()
}

func TestRegistryRequiredScopeFor(t *testing.T) {
	reg := tools.NewRegistry()
	for _, d := range toolsanon.Defs() {
		reg.Register(d)
	}
	for _, d := range toolsread.Defs() {
		reg.Register(d)
	}
	if got, ok := reg.RequiredScopeFor("list_pages"); !ok || got != "" {
		t.Fatalf("RequiredScopeFor(list_pages) = %q, %v", got, ok)
	}
	if got, ok := reg.RequiredScopeFor("validate_site"); !ok || got != "content.read" {
		t.Fatalf("RequiredScopeFor(validate_site) = %q, %v", got, ok)
	}
	if got, ok := reg.RequiredScopeFor("missing"); ok || got != "" {
		t.Fatalf("RequiredScopeFor(missing) = %q, %v", got, ok)
	}
}

func TestServerRunShutsDown(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HTTPBindAddr = "127.0.0.1"
	cfg.HTTPBindPort = 0
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	srv, err := New(cfg, idx)
	if err != nil {
		t.Fatalf("server.New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
		done <- srv.Run(ctx)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after context cancellation")
	}
}
