package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/cloudflare"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/googleindex"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/indexnow"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/observability"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/storage"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/anonymous"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
	toolswrite "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const Name = "mcp-hugo-server-go"

type Server struct {
	cfg           config.Config
	handler       http.Handler
	store         storage.Store
	oauthSvc      *oauth.Service
	resetIPCounts func()
	siteDB        *db.DB
}

// buildRegistry returns a registry populated from every known tool package.
// The registry is always complete regardless of which tools are enabled by config.
func buildRegistry() *tools.Registry {
	reg := tools.NewRegistry()
	for _, d := range anonymous.Defs() {
		reg.Register(d)
	}
	for _, d := range read.Defs() {
		reg.Register(d)
	}
	for _, d := range toolswrite.Defs() {
		reg.Register(d)
	}
	for _, d := range admin.Defs() {
		reg.Register(d)
	}
	return reg
}

func logMCPAuthRejection(r *http.Request, reason string) {
	if r == nil {
		return
	}
	slog.Warn(
		"mcp: auth rejected",
		"reason", reason,
		"method", r.Method,
		"path", r.URL.Path,
		"has_session", strings.TrimSpace(r.Header.Get("Mcp-Session-Id")) != "",
		"remote_addr", r.RemoteAddr,
	)
}

// openStore creates the OAuth token store from the config.
// Access tokens are persisted via the chosen backend. All other OAuth state
// (clients, auth codes, agent registrations) is intentionally in-Service
// memory and resets on restart (see issue #26).
func openStore(cfg config.OAuthConfig) (storage.Store, error) {
	switch cfg.StorageBackend {
	case "json":
		if cfg.StoragePath == "" {
			return nil, fmt.Errorf("server: oauth.storage_path required for json backend")
		}
		return storage.NewJSON(cfg.StoragePath)
	case "sqlite":
		if cfg.StoragePath == "" {
			return nil, fmt.Errorf("server: oauth.storage_path required for sqlite backend")
		}
		return storage.NewSQLite(cfg.StoragePath)
	default:
		return storage.NewMemory(), nil
	}
}

// ScopeExtension is a plug-and-play hook for registering additional MCP tools
// without modifying core server packages. It is called once per scope server
// (scopeName is one of "", "content.read", "content.write", "site.admin").
// Implementations should call mcp.AddTool on s to add tools to that scope.
//
// Example:
//
//	ext := server.ScopeExtension(func(scope string, s *mcp.Server) {
//	    if scope == "content.read" {
//	        mcp.AddTool(s, &mcp.Tool{Name: "my_custom_tool", ...}, myHandler)
//	    }
//	})
//	srv, _ := server.New(cfg, idx, ext)
type ScopeExtension func(scopeName string, s *mcp.Server)

func New(cfg config.Config, idx *site.Index, extensions ...ScopeExtension) (*Server, error) {
	impl := &mcp.Implementation{Name: Name, Version: buildinfo.Version}
	serverCaps := defaultServerCapabilities()
	// Explicitly declare capabilities so static scanners (mcpscan.dev) can
	// inspect them. The SDK merges these with auto-detected tool/resource caps.
	serverOpts := &mcp.ServerOptions{
		Capabilities: serverCaps,
	}
	logger := observability.NewLogger()
	metrics := observability.NewMetrics()

	reg := buildRegistry()
	scopePolicy := oauth.NewScopePolicy(reg)

	var pg *security.PathGuard
	var srcIdx *hugosite.SourceIndex
	writeEnabled := cfg.ContentRoot != ""
	if writeEnabled {
		var err error
		pg, err = security.New(cfg.ContentRoot, cfg.RejectSymlinks)
		if err != nil {
			return nil, fmt.Errorf("server: pathguard: %w", err)
		}
		srcIdx, err = hugosite.NewSourceIndex(cfg.ContentRoot)
		if err != nil {
			return nil, fmt.Errorf("server: source index: %w", err)
		}
	}

	// Open the SQLite derived index when db_path is configured.
	// When nil (db_path unset) all tools fall back to existing in-memory behaviour.
	var siteDB *db.DB
	if cfg.DBPath != "" {
		var err error
		siteDB, err = db.Open(cfg.DBPath)
		if err != nil {
			return nil, fmt.Errorf("server: sqlite index: %w", err)
		}
		// Hash-gated startup reindex — skips unchanged pages.
		if err := siteDB.StartupSync(idx, srcIdx); err != nil {
			slog.Warn("server: startup db sync incomplete", "error", err)
		}
	}

	// Build the known-tools set from the registry so the middleware can bucket
	// any unrecognised client-supplied name as "unknown" (caps Prometheus cardinality).
	knownTools := make(map[string]bool, len(reg.All()))
	for _, d := range reg.All() {
		knownTools[d.Name] = true
	}

	anonServer := mcp.NewServer(impl, serverOpts)
	anonServer.AddReceivingMiddleware(observability.NewToolCallMiddleware(logger, metrics, "", knownTools))
	registerSharedResources(anonServer)
	anonymous.Register(anonServer, idx, cfg, srcIdx)
	for _, ext := range extensions {
		ext("", anonServer)
	}

	readServer := mcp.NewServer(impl, serverOpts)
	readServer.AddReceivingMiddleware(observability.NewToolCallMiddleware(logger, metrics, "content.read", knownTools))
	registerSharedResources(readServer)
	anonymous.Register(readServer, idx, cfg, srcIdx)
	read.Register(readServer, idx, cfg, srcIdx)
	if srcIdx != nil {
		read.RegisterWithSourceIndex(readServer, idx, srcIdx, cfg, siteDB)
	}
	for _, ext := range extensions {
		ext("content.read", readServer)
	}

	writeServer := mcp.NewServer(impl, serverOpts)
	writeServer.AddReceivingMiddleware(observability.NewToolCallMiddleware(logger, metrics, "content.write", knownTools))
	registerSharedResources(writeServer)
	anonymous.Register(writeServer, idx, cfg, srcIdx)
	read.Register(writeServer, idx, cfg, srcIdx)
	if srcIdx != nil {
		read.RegisterWithSourceIndex(writeServer, idx, srcIdx, cfg, siteDB)
	}
	if writeEnabled {
		toolswrite.Register(writeServer, pg, srcIdx, cfg, siteDB, idx)
	}
	for _, ext := range extensions {
		ext("content.write", writeServer)
	}

	siteAdminServer := mcp.NewServer(impl, serverOpts)
	siteAdminServer.AddReceivingMiddleware(observability.NewToolCallMiddleware(logger, metrics, "site.admin", knownTools))
	registerSharedResources(siteAdminServer)
	anonymous.Register(siteAdminServer, idx, cfg, srcIdx)
	read.Register(siteAdminServer, idx, cfg, srcIdx)
	if srcIdx != nil {
		read.RegisterWithSourceIndex(siteAdminServer, idx, srcIdx, cfg, siteDB)
	}
	if writeEnabled {
		toolswrite.Register(siteAdminServer, pg, srcIdx, cfg, siteDB, idx)
	}
	for _, ext := range extensions {
		ext("site.admin", siteAdminServer)
	}
	admin.Register(siteAdminServer, cfg,
		func() error {
			if err := idx.Reload(cfg); err != nil {
				return err
			}
			if srcIdx != nil {
				srcIdx.ClearAllBuildPending()
			}
			return nil
		},
		func() error {
			// Reindex the SQLite derived index after a successful build.
			if siteDB != nil {
				if err := siteDB.PostBuildSync(idx); err != nil {
					slog.Warn("build_site: db reindex failed", "error", err)
				}
				if err := siteDB.SnapshotSiteHealth(); err != nil {
					slog.Warn("build_site: db health snapshot failed", "error", err)
				}
			}
			return nil
		},
		func() error {
			if err := cloudflare.PurgeAll(cfg.Cloudflare); err != nil {
				slog.Warn("build_site: cloudflare purge failed", "error", err)
			}
			return nil
		},
		func() error {
			urls := sitemapPageURLs(idx)
			if err := indexnow.Submit(cfg.IndexNow, urls); err != nil {
				slog.Warn("build_site: indexnow submit failed", "error", err)
			}
			if err := googleindex.Submit(cfg.GoogleIndex, urls, googleindex.TypeUpdated); err != nil {
				slog.Warn("build_site: google index submit failed", "error", err)
			}
			return nil
		},
	)

	opts := &mcp.StreamableHTTPOptions{
		DisableLocalhostProtection: true,
		// Keep sessions alive for 24 h so long-running agent conversations
		// don't lose tool availability mid-session.
		SessionTimeout: 24 * time.Hour,
		// MemoryEventStore lets clients resume an SSE stream with Last-Event-ID
		// after a transient network drop without creating a new session.
		EventStore: mcp.NewMemoryEventStore(nil),
		// Forward SDK warnings (SSE write errors, stream close failures) to the
		// application logger so session drops are visible in journald.
		Logger: slog.Default(),
	}
	streaming := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		scope, _ := r.Context().Value(oauth.CtxScope).(string)
		rank := tools.ScopeRank(scope)
		slog.Info("mcp: session created", "scope", scope, "rank", rank, "remote_addr", r.RemoteAddr)
		switch rank {
		case 3:
			return siteAdminServer
		case 2:
			return writeServer
		case 1:
			return readServer
		default:
			return anonServer
		}
	}, opts)

	var oauthSvc *oauth.Service
	var tokenStore storage.Store
	if cfg.OAuth.Enabled {
		var err error
		tokenStore, err = openStore(cfg.OAuth)
		if err != nil {
			return nil, err
		}
		oauthSvc = oauth.NewService(cfg.OAuth, tokenStore)
		if err := oauthSvc.LoadClientRegistry(cfg.OAuth.ClientRegistryPath); err != nil {
			return nil, fmt.Errorf("server: oauth client registry: %w", err)
		}
	}

	rateLimitedStreaming := oauth.NewRateLimiter(cfg.RateLimit).Middleware(streaming)

	maxBody := cfg.MaxRequestBytes
	if maxBody <= 0 {
		maxBody = 1 << 20
	}

	// rateLimitedOAuth applies a simple per-IP call counter to allocation
	// endpoints (/register, /agent/identity) to mitigate unbounded map growth
	// (issue #30). The limit is coarse — 100 calls per unique remote addr.
	var oauthIPMu sync.Mutex
	oauthIPCounts := make(map[string]int)
	const oauthIPMax = 100
	rateLimitOAuth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			host, _, _ := strings.Cut(r.RemoteAddr, ":")
			oauthIPMu.Lock()
			n := oauthIPCounts[host] + 1
			oauthIPCounts[host] = n
			oauthIPMu.Unlock()
			if n > oauthIPMax {
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBody)
			next(w, r)
		}
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			handleLandingPage(w, r, cfg)
		case "/.well-known/oauth-authorization-server":
			handleOAuthAuthServer(w, r, cfg)
		case "/.well-known/oauth-protected-resource":
			handleOAuthProtectedResource(w, r, cfg)
		case "/.well-known/oauth-protected-resource/mcp":
			handleOAuthProtectedResource(w, r, cfg)
		case "/.well-known/mcp/server-card.json":
			handleMCPServerCard(w, r, cfg)
		case "/.well-known/mcp.json":
			handleMCPJSON(w, r, cfg)
		case "/.well-known/agent.json":
			handleAgentJSON(w, r, cfg)
		case "/metrics":
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodHead {
				return
			}
			_, _ = io.WriteString(w, metrics.RenderPrometheus())
		case "/.well-known/security.txt":
			handleSecurityTxt(w, r, cfg)
		case "/robots.txt":
			handleRobotsTxt(w, r, cfg)
		case "/llms.txt":
			handleLLMsTxt(w, r, cfg)
		case "/auth.md":
			handleAuthMd(w, r, cfg)
		case "/register":
			if oauthSvc == nil {
				http.NotFound(w, r)
				return
			}
			rateLimitOAuth(oauthSvc.HandleRegister)(w, r)
		case "/authorize":
			if oauthSvc == nil {
				http.NotFound(w, r)
				return
			}
			oauthSvc.HandleAuthorize(w, r)
		case "/token":
			if oauthSvc == nil {
				http.NotFound(w, r)
				return
			}
			oauthSvc.HandleToken(w, r)
		case "/agent/identity":
			if oauthSvc == nil {
				http.NotFound(w, r)
				return
			}
			rateLimitOAuth(oauthSvc.HandleAgentIdentity)(w, r)
		case "/agent/identity/verify":
			if oauthSvc == nil {
				http.NotFound(w, r)
				return
			}
			oauthSvc.HandleAgentVerify(w, r)
		case "/agent/identity/claim":
			if oauthSvc == nil {
				http.NotFound(w, r)
				return
			}
			oauthSvc.HandleAgentClaim(w, r)
		case "/agent/event/notify":
			if oauthSvc == nil {
				http.NotFound(w, r)
				return
			}
			oauthSvc.HandleAgentEvent(w, r)
		case "/mcp":
			switch r.Method {
			case http.MethodPost, http.MethodGet, http.MethodDelete:
				// all three are valid per MCP Streamable HTTP spec
			default:
				w.Header().Set("Allow", "GET, POST, DELETE")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}

			callerScope := ""
			if oauthSvc != nil {
				issuer := strings.TrimRight(cfg.OAuth.Issuer, "/")
				wwwAuth := fmt.Sprintf(`Bearer realm=%q, resource_metadata=%q`,
					issuer, issuer+"/.well-known/oauth-protected-resource")
				auth := strings.TrimSpace(r.Header.Get("Authorization"))
				if auth == "" {
					logMCPAuthRejection(r, "missing_bearer")
					// No token: challenge so OAuth clients (Claude.ai, ChatGPT) discover
					// the auth server and start the PKCE flow. Without this 401 they see
					// 200 + anonymous tools and never learn auth is available (RFC 6750 §3.1).
					w.Header().Set("WWW-Authenticate", wwwAuth)
					w.Header().Set("Cache-Control", "no-store")
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				if !strings.HasPrefix(auth, "Bearer ") {
					logMCPAuthRejection(r, "invalid_bearer_format")
					w.Header().Set("WWW-Authenticate", wwwAuth)
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
				scope, legacy, ok := oauthSvc.ValidateBearerDetails(token)
				if !ok {
					logMCPAuthRejection(r, "invalid_token")
					w.Header().Set("WWW-Authenticate", wwwAuth+`, error="invalid_token"`)
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				callerScope = scope
				if legacy {
					metrics.RecordLegacyScope(scope)
					logger.Warn("accepted deprecated legacy scope alias", "scope", oauth.LegacyScopeAlias, "canonical_scope", callerScope, "issuer", strings.TrimRight(cfg.OAuth.Issuer, "/"), "path", r.URL.Path)
				}
				callerIP, _, _ := strings.Cut(r.RemoteAddr, ":")
				ctx := context.WithValue(r.Context(), oauth.CtxScope, callerScope)
				ctx = context.WithValue(ctx, oauth.CtxCallerIP, callerIP)
				if callerScope == site.AccessProfileReader {
					ctx = site.WithAccessProfile(ctx, site.AccessProfileReader)
				}
				r = r.WithContext(ctx)

				// Scope-based ACL applies only to POST (GET/DELETE have no JSON body)
				if r.Method == http.MethodPost {
					body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
					if err != nil {
						http.Error(w, "bad request", http.StatusBadRequest)
						return
					}
					if !scopePolicy.AllowRequest(body, callerScope) {
						reason := scopePolicy.DenyReason(body, callerScope)
						w.Header().Set("Content-Type", "application/json; charset=utf-8")
						w.WriteHeader(http.StatusForbidden)
						_ = json.NewEncoder(w).Encode(map[string]any{
							"jsonrpc": "2.0",
							"id":      nil,
							"error":   map[string]any{"code": -32001, "message": reason},
						})
						return
					}
					r.Body = io.NopCloser(bytes.NewReader(body))
				}
			}

			// Prevent clients from caching scoped tool lists. Without these headers,
			// a client that calls tools/list before OAuth (receiving the anonymous
			// set) may cache and reuse that response after acquiring a token.
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Vary", "Authorization")
			rateLimitedStreaming.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	resetIP := func() {
		oauthIPMu.Lock()
		oauthIPCounts = make(map[string]int)
		oauthIPMu.Unlock()
	}
	return &Server{
		cfg:           cfg,
		handler:       observability.RequestMiddleware(handler, logger),
		store:         tokenStore,
		oauthSvc:      oauthSvc,
		resetIPCounts: resetIP,
		siteDB:        siteDB,
	}, nil
}

func defaultServerCapabilities() *mcp.ServerCapabilities {
	return &mcp.ServerCapabilities{
		Logging:   &mcp.LoggingCapabilities{},
		Tools:     &mcp.ToolCapabilities{ListChanged: true},
		Prompts:   &mcp.PromptCapabilities{ListChanged: true},
		Resources: &mcp.ResourceCapabilities{ListChanged: true, Subscribe: true},
	}
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) Run(ctx context.Context) error {
	shutdownTimeout := 15 * time.Second

	if s.store != nil {
		go func() {
			t := time.NewTicker(15 * time.Minute)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					_ = s.store.PurgeExpiredTokens()
				}
			}
		}()
	}

	if s.oauthSvc != nil {
		go func() {
			t := time.NewTicker(5 * time.Minute)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					s.oauthSvc.PurgeExpired()
				}
			}
		}()
	}

	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.resetIPCounts()
			}
		}
	}()

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", s.cfg.HTTPBindAddr, s.cfg.HTTPBindPort),
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// WriteTimeout must be 0 for SSE GET streams: a non-zero value causes
		// Go's HTTP server to close any response that takes longer than the
		// deadline, which terminates long-lived SSE connections. Nginx provides
		// the upstream timeout backstop (proxy_read_timeout 1h).
		WriteTimeout:   0,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	if s.store != nil {
		_ = s.store.Close()
	}
	if s.siteDB != nil {
		_ = s.siteDB.Close()
	}
	return nil
}

// sitemapPageURLs returns all non-taxonomy page URLs from the site index.
func sitemapPageURLs(idx *site.Index) []string {
	pages := idx.Sitemap()
	urls := make([]string, 0, len(pages))
	for _, p := range pages {
		if p.URL == "" {
			continue
		}
		skip := false
		for _, pfx := range []string{"/tags/", "/categories/", "/authors/", "/search/"} {
			if strings.Contains(p.URL, pfx) {
				skip = true
				break
			}
		}
		if !skip {
			urls = append(urls, p.URL)
		}
	}
	return urls
}
