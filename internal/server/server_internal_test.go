package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	toolsadmin "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	toolsanon "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/anonymous"
	toolsread "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
	toolswrite "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
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

func TestDiscoveryBuildersFallbacks(t *testing.T) {
	cfg := config.Default()
	cfg.SiteURL = ""
	cfg.SiteName = ""
	cfg.OAuth.Issuer = "https://mcp.test"
	cfg.OAuth.Resource = ""
	cfg.OAuth.DynamicClientEnabled = true

	authMeta := buildAuthServerMeta(cfg)
	if authMeta.Issuer != "https://mcp.test" {
		t.Fatalf("buildAuthServerMeta issuer = %q", authMeta.Issuer)
	}
	if authMeta.RegistrationEndpoint != "https://mcp.test/register" {
		t.Fatalf("buildAuthServerMeta registration endpoint = %q", authMeta.RegistrationEndpoint)
	}
	if authMeta.ServiceDocumentation != "https://mcp.test/mcp" {
		t.Fatalf("buildAuthServerMeta service documentation = %q", authMeta.ServiceDocumentation)
	}

	resourceMeta := buildProtectedResourceMeta(cfg)
	if resourceMeta.Resource != "https://mcp.test/mcp" {
		t.Fatalf("buildProtectedResourceMeta resource = %q", resourceMeta.Resource)
	}

	card := buildMCPServerCard(cfg)
	if card.ServerInfo.Title != "MCP Server" {
		t.Fatalf("buildMCPServerCard title = %q", card.ServerInfo.Title)
	}
	if card.DocumentationURL != "https://mcp.test/auth.md" {
		t.Fatalf("buildMCPServerCard documentation = %q", card.DocumentationURL)
	}

	llms := buildLLMsTxt(cfg)
	if !strings.Contains(llms, "MCP endpoint: https://mcp.test/mcp") {
		t.Fatalf("buildLLMsTxt() = %q", llms)
	}
	if got := buildAgentCard(cfg); got.Name != "MCP Hugo Server" || got.URL != "https://mcp.test" {
		t.Fatalf("buildAgentCard() = %#v", got)
	}
}

// TestRegistryServerConsistency guards against drift between the Defs()
// declarations in each tool package and the global registry built by
// buildRegistry() in the server. If a tool is added to a package but
// not to its Defs(), or if a scope is changed in one place but not the
// other, this test will catch it (#70).
func TestRegistryServerConsistency(t *testing.T) {
	// Collect all Defs from every tool package (the authoritative declarations).
	allDefs := make(map[string]string) // name -> requiredScope
	for _, d := range toolsanon.Defs() {
		allDefs[d.Name] = d.RequiredScope
	}
	for _, d := range toolsread.Defs() {
		allDefs[d.Name] = d.RequiredScope
	}
	for _, d := range toolswrite.Defs() {
		allDefs[d.Name] = d.RequiredScope
	}
	for _, d := range toolsadmin.Defs() {
		allDefs[d.Name] = d.RequiredScope
	}

	// Build the registry the same way the server does.
	reg := buildRegistry()

	// Every tool in Defs() must be in the registry with the same scope.
	for name, wantScope := range allDefs {
		gotScope, ok := reg.RequiredScopeFor(name)
		if !ok {
			t.Errorf("tool %q is declared in Defs() but missing from buildRegistry()", name)
			continue
		}
		if gotScope != wantScope {
			t.Errorf("tool %q: Defs() scope=%q, registry scope=%q — drift detected", name, wantScope, gotScope)
		}
	}

	// Every tool in the registry must appear in at least one Defs().
	for _, d := range reg.All() {
		if _, ok := allDefs[d.Name]; !ok {
			t.Errorf("tool %q is in buildRegistry() but not declared in any package Defs()", d.Name)
		}
	}
}

func TestServerRunWithOAuthEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.HTTPBindAddr = "127.0.0.1"
	cfg.HTTPBindPort = 0
	cfg.OAuth.Enabled = true
	cfg.OAuth.Issuer = "https://mcp.test"
	cfg.OAuth.Resource = "https://mcp.test/mcp"
	cfg.OAuth.DynamicClientEnabled = true
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
