package server

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/storage"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	toolsadmin "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	toolsanon "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/anonymous"
	toolsread "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
	toolswrite "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
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

func TestInitWriteBootstrapBranches(t *testing.T) {
	t.Run("disabled without content root", func(t *testing.T) {
		pg, srcIdx, writeEnabled, err := initWriteBootstrap(config.Config{})
		if err != nil {
			t.Fatalf("initWriteBootstrap(disabled) error = %v", err)
		}
		if writeEnabled {
			t.Fatal("initWriteBootstrap(disabled) writeEnabled = true, want false")
		}
		if pg != nil || srcIdx != nil {
			t.Fatalf("initWriteBootstrap(disabled) = %#v, %#v, want nil, nil", pg, srcIdx)
		}
	})

	t.Run("enabled with content root", func(t *testing.T) {
		contentRoot := t.TempDir()
		pg, srcIdx, writeEnabled, err := initWriteBootstrap(config.Config{
			ContentRoot:    contentRoot,
			RejectSymlinks: true,
		})
		if err != nil {
			t.Fatalf("initWriteBootstrap(enabled) error = %v", err)
		}
		if !writeEnabled {
			t.Fatal("initWriteBootstrap(enabled) writeEnabled = false, want true")
		}
		if pg == nil || srcIdx == nil {
			t.Fatalf("initWriteBootstrap(enabled) = %#v, %#v, want non-nil values", pg, srcIdx)
		}
	})
}

func TestOpenSiteDBBranches(t *testing.T) {
	idx, err := site.NewIndex(config.Default())
	if err != nil {
		t.Fatalf("site.NewIndex(default) error = %v", err)
	}

	t.Run("disabled without db path", func(t *testing.T) {
		siteDB, err := openSiteDB(config.Config{}, idx, nil)
		if err != nil {
			t.Fatalf("openSiteDB(disabled) error = %v", err)
		}
		if siteDB != nil {
			t.Fatalf("openSiteDB(disabled) = %#v, want nil", siteDB)
		}
	})

	t.Run("invalid db path wraps open failure", func(t *testing.T) {
		dir := t.TempDir()
		siteDB, err := openSiteDB(config.Config{DBPath: dir}, idx, nil)
		if err == nil {
			if siteDB != nil {
				_ = siteDB.Close()
			}
			t.Fatal("openSiteDB(dir path) error = nil, want wrapped sqlite index error")
		}
		if siteDB != nil {
			t.Fatalf("openSiteDB(dir path) = %#v, want nil on error", siteDB)
		}
		if !strings.Contains(err.Error(), "server: sqlite index:") {
			t.Fatalf("openSiteDB(dir path) error = %q, want sqlite index wrapper", err)
		}
	})

	t.Run("startup sync warning path still returns db handle", func(t *testing.T) {
		contentRoot := t.TempDir()
		if err := os.WriteFile(filepath.Join(contentRoot, "index.html"), []byte("<html><head><title>x</title></head><body></body></html>"), 0o644); err != nil {
			t.Fatalf("WriteFile(index.html): %v", err)
		}
		cfg := config.Default()
		cfg.SiteRoot = contentRoot
		siteIdx, err := site.NewIndex(cfg)
		if err != nil {
			t.Fatalf("site.NewIndex(site root) error = %v", err)
		}
		dbPath := filepath.Join(t.TempDir(), "site.sqlite")
		siteDB, err := openSiteDB(config.Config{DBPath: dbPath}, siteIdx, nil)
		if err != nil {
			t.Fatalf("openSiteDB(valid path) error = %v", err)
		}
		defer func() { _ = siteDB.Close() }()
		if siteDB == nil {
			t.Fatal("openSiteDB(valid path) = nil, want non-nil handle")
		}
	})
}

func TestOpenSiteDBReconcilesOnlyInterruptedBuildsAfterFreshStartupSync(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html><head><title>x</title></head><body></body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.SiteRoot = root
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "site.sqlite")
	journal, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []db.RecoveryEntry{
		{OperationID: "build-before-swap", Kind: "build", State: "in_progress", Payload: []byte(`{}`)},
		{OperationID: "build-after-swap", Kind: "build", State: "file_written", Payload: []byte(`{}`)},
		{OperationID: "build-unknown", Kind: "build", State: "cleanup_pending", Payload: []byte(`{}`)},
		{OperationID: "mutation-unknown", Kind: "content_write", State: "file_written", Payload: []byte(`{}`)},
	} {
		if err := journal.RecordRecovery(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}

	siteDB, err := openSiteDB(config.Config{DBPath: path}, idx, nil)
	if err != nil {
		t.Fatalf("openSiteDB: %v", err)
	}
	defer siteDB.Close()
	pending, err := siteDB.PendingRecovery()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].OperationID != "build-unknown" || pending[1].OperationID != "mutation-unknown" {
		t.Fatalf("pending after startup reconciliation = %+v, want unknown build and untouched mutation", pending)
	}
}

func TestOpenSiteDBLeavesInterruptedBuildPendingWhenStartupSyncFails(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html><head><title>x</title></head><body></body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.SiteRoot = root
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "site.sqlite")
	journal, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.RecordRecovery(db.RecoveryEntry{OperationID: "build-pending", Kind: "build", State: "file_written", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	rawDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.Exec(`CREATE TRIGGER reject_page_insert BEFORE INSERT ON pages BEGIN SELECT RAISE(ABORT, 'injected startup sync failure'); END`); err != nil {
		rawDB.Close()
		t.Fatal(err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatal(err)
	}

	siteDB, err := openSiteDB(config.Config{DBPath: path}, idx, nil)
	if err != nil {
		t.Fatalf("openSiteDB: %v", err)
	}
	defer siteDB.Close()
	pending, err := siteDB.PendingRecovery()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].OperationID != "build-pending" || pending[0].State != "file_written" {
		t.Fatalf("pending after failed startup sync = %+v, want untouched build", pending)
	}
}

func TestKnownToolsSet(t *testing.T) {
	reg := buildRegistry()
	known := knownToolsSet(reg)
	if len(known) != len(reg.All()) {
		t.Fatalf("knownToolsSet() len = %d, want %d", len(known), len(reg.All()))
	}
	for _, d := range reg.All() {
		if !known[d.Name] {
			t.Fatalf("knownToolsSet() missing %q", d.Name)
		}
	}
}

func TestConfiguredMaxRequestBytes(t *testing.T) {
	if got := configuredMaxRequestBytes(config.Config{}); got != 1<<20 {
		t.Fatalf("configuredMaxRequestBytes(default) = %d, want %d", got, 1<<20)
	}
	if got := configuredMaxRequestBytes(config.Config{MaxRequestBytes: -1}); got != 1<<20 {
		t.Fatalf("configuredMaxRequestBytes(negative) = %d, want %d", got, 1<<20)
	}
	if got := configuredMaxRequestBytes(config.Config{MaxRequestBytes: 4096}); got != 4096 {
		t.Fatalf("configuredMaxRequestBytes(explicit) = %d, want 4096", got)
	}
}

func TestApplyOAuthCORS(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/oauth/register", nil)
	if handled := applyOAuthCORS(rec, req, "GET, POST"); !handled {
		t.Fatal("applyOAuthCORS(OPTIONS) = false, want true")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("OPTIONS Allow-Origin = %q, want *", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST" {
		t.Fatalf("OPTIONS Allow-Methods = %q, want GET, POST", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, Authorization" {
		t.Fatalf("OPTIONS Allow-Headers = %q", got)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/oauth/register", nil)
	if handled := applyOAuthCORS(rec, req, "GET, POST"); handled {
		t.Fatal("applyOAuthCORS(POST) = true, want false")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("POST Allow-Origin = %q, want *", got)
	}
}

func TestPostBuildCallbacksPreserveStableOrder(t *testing.T) {
	want := []string{
		"build_pages",
		"recovery_journal",
		"index_reload",
		"db_reindex",
		"publication_manifest",
		"cloudflare_purge",
		"search_index_submit",
		"stale_test_content_check",
	}
	cfg := config.Default()
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("site.NewIndex(default) error = %v", err)
	}

	for _, action := range []string{"build_site", "publish_changes"} {
		t.Run(action, func(t *testing.T) {
			callbacks := postBuildCallbacks(action, slog.Default(), cfg, idx, nil, nil)
			got := make([]string, 0, len(callbacks))
			for _, cb := range callbacks {
				got = append(got, cb.Name)
				// A callback runs via a normal function, a lifecycle hook, or a
				// completion callback. Recovery is lifecycle-only until the final
				// completion transition.
				// (build.go's runner dispatches on whichever is set) — never
				// require Fn specifically, or a legitimate
				// OnBuildComplete-only callback like publication_manifest
				// reads as broken.
				if cb.Fn == nil && cb.OnBuildPrepared == nil && cb.OnBuildStart == nil && cb.OnOutputSwapped == nil && cb.OnBuildComplete == nil && cb.OnBuildFailed == nil {
					t.Fatalf("postBuildCallbacks(%q) returned no callback for %q", action, cb.Name)
				}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("postBuildCallbacks(%q) names = %v, want %v", action, got, want)
			}
		})
	}
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
	if got, ok := reg.RequiredScopeFor("validate_site"); !ok || got != "" {
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
	started := make(chan struct{})
	go func() {
		close(started)
		cancel()
		done <- srv.Run(ctx)
	}()
	<-started
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

func TestReaderAcquisitionProfileMatrix(t *testing.T) {
	tests := []struct {
		name            string
		cfg             config.Config
		wantMode        string
		wantDescription string
	}{
		{
			name:            "oauth disabled",
			cfg:             config.Default(),
			wantMode:        "anonymous_mcp_access",
			wantDescription: "anonymous MCP access",
		},
		{
			name: "dynamic and self-registration",
			cfg: func() config.Config {
				cfg := config.Default()
				cfg.OAuth.Enabled = true
				cfg.OAuth.DynamicClientEnabled = true
				cfg.OAuth.AllowReaderSelfRegistration = true
				return cfg
			}(),
			wantMode:        "self_serve_oauth_or_agent_identity_registration",
			wantDescription: "self-serve OAuth registration or anonymous agent identity registration",
		},
		{
			name: "dynamic only",
			cfg: func() config.Config {
				cfg := config.Default()
				cfg.OAuth.Enabled = true
				cfg.OAuth.DynamicClientEnabled = true
				return cfg
			}(),
			wantMode:        "self_serve_oauth_registration",
			wantDescription: "self-serve OAuth registration",
		},
		{
			name: "self-registration only",
			cfg: func() config.Config {
				cfg := config.Default()
				cfg.OAuth.Enabled = true
				cfg.OAuth.AllowReaderSelfRegistration = true
				return cfg
			}(),
			wantMode:        "self_serve_agent_identity_registration",
			wantDescription: "anonymous agent identity registration",
		},
		{
			name: "operator approved",
			cfg: func() config.Config {
				cfg := config.Default()
				cfg.OAuth.Enabled = true
				return cfg
			}(),
			wantMode:        "operator_approved_claim_or_pre_registered_oauth_client",
			wantDescription: "operator-approved anonymous claim or pre-registered OAuth client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMode, gotDescription := readerAcquisitionProfile(tt.cfg)
			if gotMode != tt.wantMode || gotDescription != tt.wantDescription {
				t.Fatalf("readerAcquisitionProfile() = (%q, %q), want (%q, %q)", gotMode, gotDescription, tt.wantMode, tt.wantDescription)
			}
		})
	}
}

func TestServeDiscoveryTextSupportsGetAndHeadOnly(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
	serveDiscoveryText(rec, req, "text/plain; charset=utf-8", "hello")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("GET Content-Type = %q", got)
	}
	if got := rec.Body.String(); got != "hello" {
		t.Fatalf("GET body = %q, want hello", got)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodHead, "/llms.txt", nil)
	serveDiscoveryText(rec, req, "text/plain; charset=utf-8", "hello")
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "" {
		t.Fatalf("HEAD body = %q, want empty", got)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/llms.txt", nil)
	serveDiscoveryText(rec, req, "text/plain; charset=utf-8", "hello")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("POST Allow = %q, want GET, HEAD", got)
	}
}

func TestHandleSecurityTxtMethodAndMissingConfig(t *testing.T) {
	cfg := config.Default()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/security.txt", nil)
	handleSecurityTxt(rec, req, cfg)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET without SecurityContactURL status = %d, want 404", rec.Code)
	}

	cfg.SecurityContact = "mailto:security@example.test"
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/.well-known/security.txt", nil)
	handleSecurityTxt(rec, req, cfg)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST with SecurityContact status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("POST Allow = %q, want GET, HEAD", got)
	}
}

func TestHandleAuthMdMethodAndMissingFile(t *testing.T) {
	cfg := config.Default()
	cfg.OAuth.Issuer = "https://mcp.test"

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth.md", nil)
	handleAuthMd(rec, req, cfg)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET missing auth.md status = %d, want 404", rec.Code)
	}

	root := t.TempDir()
	cfg.SiteRoot = root
	if err := os.WriteFile(filepath.Join(root, "auth.md"), []byte("# auth\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(auth.md): %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/auth.md", nil)
	handleAuthMd(rec, req, cfg)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST auth.md status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("POST Allow = %q, want GET, HEAD", got)
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
	started := make(chan struct{})
	go func() {
		close(started)
		cancel()
		done <- srv.Run(ctx)
	}()
	<-started
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after context cancellation")
	}
}

func TestMCPBearerAuthMiddlewareMissingBearerPreservesChallengeShape(t *testing.T) {
	mw := newMCPBearerAuthMiddleware(func(context.Context, string, *http.Request) (*sdkauth.TokenInfo, error) {
		t.Fatal("verifier should not run when bearer header is missing")
		return nil, nil
	}, "https://mcp.test", "https://mcp.test/.well-known/oauth-protected-resource")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not run on missing bearer")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d want 401", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "unauthorized" {
		t.Fatalf("body = %q want unauthorized", got)
	}
	wwwAuth := rec.Header().Get("WWW-Authenticate")
	if !strings.Contains(wwwAuth, `realm="https://mcp.test"`) {
		t.Fatalf("WWW-Authenticate = %q, want realm", wwwAuth)
	}
	if !strings.Contains(wwwAuth, `resource_metadata="https://mcp.test/.well-known/oauth-protected-resource"`) {
		t.Fatalf("WWW-Authenticate = %q, want resource_metadata", wwwAuth)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q want no-store", got)
	}
}

func TestMCPBearerAuthMiddlewareInvalidBearerPreservesInvalidTokenMarker(t *testing.T) {
	mw := newMCPBearerAuthMiddleware(func(context.Context, string, *http.Request) (*sdkauth.TokenInfo, error) {
		return nil, errors.Join(sdkauth.ErrInvalidToken, errors.New("wrapped verifier detail"))
	}, "https://mcp.test", "https://mcp.test/.well-known/oauth-protected-resource")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer broken")
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not run on invalid bearer")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d want 401", rec.Code)
	}
	wwwAuth := rec.Header().Get("WWW-Authenticate")
	if !strings.Contains(wwwAuth, `error="invalid_token"`) {
		t.Fatalf("WWW-Authenticate = %q, want invalid_token marker", wwwAuth)
	}
}

func TestInterceptResponseWriterFlushBufferedToReal(t *testing.T) {
	rec := httptest.NewRecorder()
	iw := newInterceptResponseWriter(rec)
	iw.Header().Set("Content-Type", "application/json")
	iw.Header().Add("X-Test", "one")
	iw.WriteHeader(http.StatusAccepted)
	if _, err := iw.Write([]byte(`{"ok":true}`)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	iw.flushBufferedToReal()

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := rec.Header().Get("X-Test"); got != "one" {
		t.Fatalf("X-Test = %q, want one", got)
	}
	if got := rec.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body = %q, want JSON payload", got)
	}
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flushRecorder) Flush() {
	f.flushed = true
	f.ResponseRecorder.Flush()
}

func TestInterceptResponseWriterPassThroughWriteAndFlush(t *testing.T) {
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	iw := newInterceptResponseWriter(rec)
	iw.passThrough = true

	iw.Header().Set("X-Test", "yes")
	iw.WriteHeader(http.StatusCreated)
	if _, err := iw.Write([]byte("ok")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	iw.Flush()

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := rec.Header().Get("X-Test"); got != "yes" {
		t.Fatalf("X-Test = %q, want yes", got)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Fatalf("body = %q, want ok", got)
	}
	if !rec.flushed {
		t.Fatal("Flush() did not reach underlying flusher in pass-through mode")
	}
}

func TestMCPBearerAuthMiddlewareInvalidFormatPreservesChallengeShape(t *testing.T) {
	mw := newMCPBearerAuthMiddleware(func(context.Context, string, *http.Request) (*sdkauth.TokenInfo, error) {
		t.Fatal("verifier should not run when bearer header format is invalid")
		return nil, nil
	}, "https://mcp.test", "https://mcp.test/.well-known/oauth-protected-resource")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Basic nope")
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not run on invalid bearer format")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d want 401", rec.Code)
	}
	wwwAuth := rec.Header().Get("WWW-Authenticate")
	if !strings.Contains(wwwAuth, `realm="https://mcp.test"`) {
		t.Fatalf("WWW-Authenticate = %q, want realm", wwwAuth)
	}
	if strings.Contains(wwwAuth, `error="invalid_token"`) {
		t.Fatalf("WWW-Authenticate = %q, invalid format must not be marked invalid_token", wwwAuth)
	}
}

func TestRejectMCPBearerOmitsResourceMetadataWhenEmpty(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)

	rejectMCPBearer(rec, req, "missing_bearer", "https://mcp.test", "", false)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	wwwAuth := rec.Header().Get("WWW-Authenticate")
	if !strings.Contains(wwwAuth, `realm="https://mcp.test"`) {
		t.Fatalf("WWW-Authenticate = %q, want realm", wwwAuth)
	}
	if strings.Contains(wwwAuth, "resource_metadata=") {
		t.Fatalf("WWW-Authenticate = %q, did not expect resource_metadata when URL is empty", wwwAuth)
	}
}

func TestMCPBearerAuthMiddlewareValidBearerReachesNextWithoutCorruption(t *testing.T) {
	store := storage.NewMemory()
	svc := oauth.NewService(config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		Resource:              "https://mcp.test/mcp",
		TrustedAuthorizeCIDRs: []string{"127.0.0.1/32"},
	}, store)
	if err := store.AddAccessToken(oauth.HashToken("token-valid"), "reader", "reader-client", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("AddAccessToken() error = %v", err)
	}

	var (
		called bool
		gotBR  mcpBearerResult
		gotOK  bool
	)
	mw := newMCPBearerAuthMiddleware(oauthTokenVerifier(svc), "https://mcp.test", "https://mcp.test/.well-known/oauth-protected-resource")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer token-valid")
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		gotBR, gotOK = bearerResultFromContext(r.Context())
		w.Header().Set("X-Next", "yes")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	})).ServeHTTP(rec, req)

	if !called {
		t.Fatal("next handler was not called for valid bearer")
	}
	if !gotOK {
		t.Fatal("bearerResultFromContext() = not ok, want ok")
	}
	if gotBR.scope != "read" || !gotBR.legacy || gotBR.tokenHash != oauth.HashToken("token-valid") || gotBR.principal != "reader-client" {
		t.Fatalf("bearerResultFromContext() = %#v, want canonical scope, legacy alias marker, token hash, and principal", gotBR)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := rec.Header().Get("X-Next"); got != "yes" {
		t.Fatalf("X-Next = %q, want yes", got)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Fatalf("body = %q, want ok", got)
	}
}

func TestBearerResultFromContextMissingOrWrongType(t *testing.T) {
	if got, ok := bearerResultFromContext(context.Background()); ok || got != (mcpBearerResult{}) {
		t.Fatalf("bearerResultFromContext(background) = (%#v, %v), want zero/false", got, ok)
	}

	var (
		gotResult mcpBearerResult
		gotOK     bool
	)
	mw := newMCPBearerAuthMiddleware(func(context.Context, string, *http.Request) (*sdkauth.TokenInfo, error) {
		return &sdkauth.TokenInfo{
			Scopes:     []string{"read"},
			Expiration: time.Now().Add(time.Hour),
			Extra:      map[string]any{"mcp_bearer": "wrong-type"},
		}, nil
	}, "https://mcp.test", "https://mcp.test/.well-known/oauth-protected-resource")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer token-valid")
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotResult, gotOK = bearerResultFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if gotOK || gotResult != (mcpBearerResult{}) {
		t.Fatalf("bearerResultFromContext(wrong-type) = (%#v, %v), want zero/false", gotResult, gotOK)
	}
}
