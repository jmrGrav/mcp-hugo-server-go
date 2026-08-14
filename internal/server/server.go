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

	"github.com/jmrGrav/mcp-hugo-server-go/internal/audit"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/cloudflare"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/googleindex"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/indexnow"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/observability"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/previewstore"
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
	audit.Warn(audit.EventAuthRejected, reason,
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
// (scopeName is one of "", "write", "admin").
// Implementations should call mcp.AddTool on s to add tools to that scope.
//
// Example:
//
//	ext := server.ScopeExtension(func(scope string, s *mcp.Server) {
//	    if scope == "" {
//	        mcp.AddTool(s, &mcp.Tool{Name: "my_custom_tool", ...}, myHandler)
//	    }
//	})
//	srv, _ := server.New(cfg, idx, ext)
type ScopeExtension func(scopeName string, s *mcp.Server)

func initWriteBootstrap(cfg config.Config) (*security.PathGuard, *hugosite.SourceIndex, bool, error) {
	writeEnabled := cfg.ContentRoot != ""
	if !writeEnabled {
		return nil, nil, false, nil
	}
	pg, err := security.New(cfg.ContentRoot, cfg.RejectSymlinks)
	if err != nil {
		return nil, nil, false, fmt.Errorf("server: pathguard: %w", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(cfg.ContentRoot)
	if err != nil {
		return nil, nil, false, fmt.Errorf("server: source index: %w", err)
	}
	return pg, srcIdx, true, nil
}

func openSiteDB(cfg config.Config, idx *site.Index, srcIdx *hugosite.SourceIndex) (*db.DB, error) {
	if cfg.DBPath == "" {
		return nil, nil
	}
	siteDB, err := db.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("server: sqlite index: %w", err)
	}
	if err := siteDB.StartupSync(idx, srcIdx); err != nil {
		slog.Warn("server: startup db sync incomplete", "error", err)
	}
	return siteDB, nil
}

func knownToolsSet(reg *tools.Registry) map[string]bool {
	knownTools := make(map[string]bool, len(reg.All()))
	for _, d := range reg.All() {
		knownTools[d.Name] = true
	}
	return knownTools
}

func newScopedServer(
	scopeName string,
	impl *mcp.Implementation,
	serverOpts *mcp.ServerOptions,
	logger *slog.Logger,
	metrics *observability.Metrics,
	knownTools map[string]bool,
	idx *site.Index,
	cfg config.Config,
	srcIdx *hugosite.SourceIndex,
	siteDB *db.DB,
	pg *security.PathGuard,
	writeEnabled bool,
	extensions []ScopeExtension,
) *mcp.Server {
	s := mcp.NewServer(impl, serverOpts)
	s.AddReceivingMiddleware(observability.NewToolCallMiddleware(logger, metrics, scopeName, knownTools))
	registerSharedResources(s)
	anonymous.Register(s, idx, cfg, scopeName, srcIdx)
	read.Register(s, idx, cfg, srcIdx)
	if srcIdx != nil {
		read.RegisterWithSourceIndex(s, idx, srcIdx, cfg, siteDB)
	}
	if (scopeName == "write" || scopeName == "admin") && writeEnabled {
		toolswrite.Register(s, pg, srcIdx, cfg, siteDB, idx)
	}
	for _, ext := range extensions {
		ext(scopeName, s)
	}
	return s
}

func initOAuthService(cfg config.Config) (*oauth.Service, storage.Store, error) {
	if !cfg.OAuth.Enabled {
		return nil, nil, nil
	}
	tokenStore, err := openStore(cfg.OAuth)
	if err != nil {
		return nil, nil, err
	}
	oauthSvc := oauth.NewService(cfg.OAuth, tokenStore)
	if err := oauthSvc.LoadClientRegistry(cfg.OAuth.ClientRegistryPath); err != nil {
		return nil, nil, fmt.Errorf("server: oauth client registry: %w", err)
	}
	return oauthSvc, tokenStore, nil
}

func configuredMaxRequestBytes(cfg config.Config) int64 {
	maxBody := cfg.MaxRequestBytes
	if maxBody <= 0 {
		return 1 << 20
	}
	return maxBody
}

func newMCPToolHandler(
	cfg config.Config,
	oauthSvc *oauth.Service,
	scopePolicy *oauth.ScopePolicy,
	metrics *observability.Metrics,
	logger *slog.Logger,
	rateLimitedStreaming http.Handler,
	maxBody int64,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callerScope := ""
		if oauthSvc != nil {
			bearerResult, ok := bearerResultFromContext(r.Context())
			if !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			callerScope = bearerResult.scope
			if bearerResult.legacy {
				metrics.RecordLegacyScope(callerScope)
				logger.Warn("accepted deprecated legacy scope alias", "scope", oauth.LegacyScopeAlias, "canonical_scope", callerScope, "issuer", strings.TrimRight(cfg.OAuth.Issuer, "/"), "path", r.URL.Path)
			}
			callerIP, _, _ := strings.Cut(r.RemoteAddr, ":")
			ctx := context.WithValue(r.Context(), oauth.CtxScope, callerScope)
			ctx = context.WithValue(ctx, oauth.CtxCallerIP, callerIP)
			ctx = context.WithValue(ctx, oauth.CtxTokenID, bearerResult.tokenHash)
			ctx = context.WithValue(ctx, oauth.CtxPrincipal, bearerResult.principal)
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
					audit.Warn(audit.EventScopeDenied, "denied",
						"scope", callerScope,
						"reason", reason,
						"path", r.URL.Path,
						"remote_addr", r.RemoteAddr,
					)
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
	})
}

func newProtectedMCPHandler(cfg config.Config, oauthSvc *oauth.Service, mcpToolHandler http.Handler) http.Handler {
	if oauthSvc == nil {
		return mcpToolHandler
	}
	issuer := strings.TrimRight(cfg.OAuth.Issuer, "/")
	return newMCPBearerAuthMiddleware(
		oauthTokenVerifier(oauthSvc),
		issuer,
		issuer+"/.well-known/oauth-protected-resource",
	)(mcpToolHandler)
}

func newOAuthAllocationLimiter(maxBody int64) (func(http.HandlerFunc) http.HandlerFunc, func()) {
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
	resetIP := func() {
		oauthIPMu.Lock()
		oauthIPCounts = make(map[string]int)
		oauthIPMu.Unlock()
	}
	return rateLimitOAuth, resetIP
}

func newRootHandler(
	cfg config.Config,
	oauthSvc *oauth.Service,
	rateLimitOAuth func(http.HandlerFunc) http.HandlerFunc,
	protectedMCPHandler http.Handler,
	previewHandler http.Handler,
	metrics *observability.Metrics,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		case "/.well-known/mcp/server-card/mcp":
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
			if applyOAuthCORS(w, r, http.MethodPost) {
				return
			}
			if oauthSvc == nil {
				http.NotFound(w, r)
				return
			}
			rateLimitOAuth(oauthSvc.HandleRegister)(w, r)
		case "/authorize":
			if applyOAuthCORS(w, r, http.MethodGet+", "+http.MethodPost) {
				return
			}
			if oauthSvc == nil {
				http.NotFound(w, r)
				return
			}
			oauthSvc.HandleAuthorize(w, r)
		case "/token":
			if applyOAuthCORS(w, r, http.MethodPost) {
				return
			}
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
			default:
				w.Header().Set("Allow", "GET, POST, DELETE")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			protectedMCPHandler.ServeHTTP(w, r)
		default:
			if strings.HasPrefix(r.URL.Path, "/preview/") {
				previewHandler.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
		}
	})
}

func postBuildCallbacks(
	action string,
	logger *slog.Logger,
	cfg config.Config,
	idx *site.Index,
	srcIdx *hugosite.SourceIndex,
	siteDB *db.DB,
) []admin.PostBuildCallback {
	return []admin.PostBuildCallback{
		{Name: "index_reload", Fn: func() error {
			if err := idx.Reload(cfg); err != nil {
				return err
			}
			if srcIdx != nil {
				if err := srcIdx.Reload(cfg.ContentRoot); err != nil {
					return err
				}
				srcIdx.ClearAllBuildPending()
			}
			return nil
		}},
		{Name: "db_reindex", Fn: func() error {
			if siteDB != nil {
				if err := siteDB.PostBuildSync(idx); err != nil {
					logger.Warn(action+": db reindex failed", "error", err)
				}
				if err := siteDB.SnapshotSiteHealth(); err != nil {
					logger.Warn(action+": db health snapshot failed", "error", err)
				}
			}
			return nil
		}},
		{Name: "publication_manifest", OnBuildComplete: func(completion admin.BuildCompletion) error {
			if siteDB == nil {
				return nil
			}
			return siteDB.RecordPublicationManifest(db.PublicationManifest{
				BuildID:        completion.BuildID,
				SourceRevision: completion.SourceRevision,
				OutputRevision: completion.OutputRevision,
				HugoVersion:    completion.HugoVersion,
				Status:         completion.Status,
				ObservedAt:     completion.ObservedAt,
			})
		}},
		{Name: "cloudflare_purge", Fn: func() error {
			if err := cloudflare.PurgeAll(cfg.Cloudflare); err != nil {
				logger.Warn(action+": cloudflare purge failed", "error", err)
			}
			return nil
		}},
		{Name: "search_index_submit", Fn: func() error {
			urls := sitemapPageURLs(idx)
			if err := indexnow.Submit(cfg.IndexNow, urls); err != nil {
				logger.Warn(action+": indexnow submit failed", "error", err)
			}
			if err := googleindex.Submit(cfg.GoogleIndex, urls, googleindex.TypeUpdated); err != nil {
				logger.Warn(action+": google index submit failed", "error", err)
			}
			return nil
		}},
		{Name: "stale_test_content_check", Fn: func() error {
			return admin.CheckStaleTestContent(srcIdx, cfg.StaleTestContentThresholdHours)
		}},
	}
}

// buildServerCore assembles the pieces shared by both the HTTP (New) and
// stdio (NewStdio) entrypoints: the MCP implementation/capabilities, logger,
// metrics, tool registry, write bootstrap (path guard / source index /
// writeEnabled), and the derived SQLite index. Kept as a single source of
// truth so the two entrypoints can never register a different set of tools
// or wire write access differently by accident.
type serverCore struct {
	impl         *mcp.Implementation
	serverOpts   *mcp.ServerOptions
	logger       *slog.Logger
	metrics      *observability.Metrics
	knownTools   map[string]bool
	pg           *security.PathGuard
	srcIdx       *hugosite.SourceIndex
	writeEnabled bool
	siteDB       *db.DB
}

func buildServerCore(cfg config.Config, idx *site.Index) (*serverCore, error) {
	impl := &mcp.Implementation{Name: Name, Version: buildinfo.Version}
	serverCaps := defaultServerCapabilities()
	// Explicitly declare capabilities so static scanners (mcpscan.dev) can
	// inspect them. The SDK merges these with auto-detected tool/resource caps.
	serverOpts := &mcp.ServerOptions{
		Capabilities: serverCaps,
	}
	logger := observability.NewLogger()
	// Unify every package-level slog.Info/Warn/Error call (agent_auth.go,
	// build.go, hooks.go, the audit package, ...) onto the same structured
	// JSON handler as the request/tool-call logs above, instead of Go's
	// plain-text default. This is what makes the security audit trail
	// (#371) durable and uniformly parseable without touching every
	// individual call site.
	slog.SetDefault(logger)
	metrics := observability.NewMetrics()

	reg := buildRegistry()

	pg, srcIdx, writeEnabled, err := initWriteBootstrap(cfg)
	if err != nil {
		return nil, err
	}

	// Open the SQLite derived index when db_path is configured.
	// When nil (db_path unset) all tools fall back to existing in-memory behaviour.
	siteDB, err := openSiteDB(cfg, idx, srcIdx)
	if err != nil {
		return nil, err
	}

	// Build the known-tools set from the registry so the middleware can bucket
	// any unrecognised client-supplied name as "unknown" (caps Prometheus cardinality).
	knownTools := knownToolsSet(reg)

	return &serverCore{
		impl:         impl,
		serverOpts:   serverOpts,
		logger:       logger,
		metrics:      metrics,
		knownTools:   knownTools,
		pg:           pg,
		srcIdx:       srcIdx,
		writeEnabled: writeEnabled,
		siteDB:       siteDB,
	}, nil
}

// buildWriteScopedServer constructs the "write" scoped *mcp.Server — the one
// that exposes every read AND write/admin tool — used identically by the
// HTTP transport (behind OAuth, only reachable at bearer scope rank >= 1)
// and the stdio transport (granted unconditionally: stdio's whole premise is
// a trusted local single-user process, per #782 Phase 2's dual-transport
// design). This is the ONLY place write tools get registered — the HTTP
// scope-routing callback in New() decides *when* a caller reaches this
// server, but never re-implements *what's on* it.
// previews is returned alongside the server so HTTP callers can bind the
// same store instance to their preview-serving handler — a preview created
// through the create_preview tool must be readable through that handler,
// which only works if both sides share one previewstore.Store.
func buildWriteScopedServer(core *serverCore, cfg config.Config, idx *site.Index, extensions []ScopeExtension) (*mcp.Server, *previewstore.Store) {
	previews := previewstore.New()
	writeServer := buildPrivilegedScopedServer("write", core, cfg, idx, extensions, previews)
	// Keep managed Hugo binary tools out of tools/list for ordinary write
	// callers. Calls are also denied by ScopePolicy; this removes discovery
	// ambiguity as well as enforcing the boundary.
	writeServer.RemoveTools("stage_hugo_upgrade", "activate_hugo", "rollback_hugo", "bootstrap_hugo")
	return writeServer, previews
}

func buildAdminScopedServer(core *serverCore, cfg config.Config, idx *site.Index, extensions []ScopeExtension, previews *previewstore.Store) *mcp.Server {
	return buildPrivilegedScopedServer("admin", core, cfg, idx, extensions, previews)
}

func buildPrivilegedScopedServer(scopeName string, core *serverCore, cfg config.Config, idx *site.Index, extensions []ScopeExtension, previews *previewstore.Store) *mcp.Server {
	server := newScopedServer(scopeName, core.impl, core.serverOpts, core.logger, core.metrics, core.knownTools, idx, cfg, core.srcIdx, core.siteDB, core.pg, core.writeEnabled, extensions)
	admin.Register(server, cfg, core.srcIdx, postBuildCallbacks("build_site", core.logger, cfg, idx, core.srcIdx, core.siteDB)...)
	// RegisterRuntimeStatus again with the live public index: the generic admin
	// registration keeps its compatibility signature for unit registrations,
	// while the production server can reconcile source and public output after
	// restarts instead of trusting volatile BuildPending flags (#1066).
	admin.RegisterRuntimeStatusWithDB(server, cfg, core.srcIdx, core.siteDB, idx)
	admin.RegisterVerifyPublication(server, idx, core.srcIdx, cfg)
	admin.RegisterPublishChanges(server, idx, core.srcIdx, cfg, postBuildCallbacks("publish_changes", core.logger, cfg, idx, core.srcIdx, core.siteDB)...)
	previewBaseURL := strings.TrimRight(cfg.OAuth.Issuer, "/")
	admin.RegisterCreatePreview(server, cfg, previews, previewBaseURL)
	admin.RegisterPreviewAccessTools(server, cfg, previews, previewBaseURL)
	admin.RegisterStorageHealth(server, cfg, core.srcIdx, previews)
	read.RegisterInspectPreviewRenderedPage(server, idx, core.srcIdx, cfg, previews, previewBaseURL)
	return server
}

// NewStdio builds a write-scoped *mcp.Server for the stdio transport
// (MCPB/local desktop use, #782 Phase 2). Unlike New (HTTP), there is no
// OAuth, no scope routing, and no publicServer/writeServer split by
// request — stdio is a single local process talking to a single local
// caller over its own stdin/stdout, so it gets the full write-scoped tool
// set unconditionally. Callers run it with:
//
//	srv.Run(ctx, &mcp.StdioTransport{})
func NewStdio(cfg config.Config, idx *site.Index, extensions ...ScopeExtension) (*mcp.Server, error) {
	core, err := buildServerCore(cfg, idx)
	if err != nil {
		return nil, err
	}
	// Stdio is a trusted local operator transport; expose the full privileged
	// catalog, including admin-gated Hugo lifecycle tools.
	return buildPrivilegedScopedServer("admin", core, cfg, idx, extensions, previewstore.New()), nil
}

func New(cfg config.Config, idx *site.Index, extensions ...ScopeExtension) (*Server, error) {
	core, err := buildServerCore(cfg, idx)
	if err != nil {
		return nil, err
	}
	logger := core.logger
	metrics := core.metrics
	pg := core.pg
	srcIdx := core.srcIdx
	writeEnabled := core.writeEnabled
	siteDB := core.siteDB

	reg := buildRegistry()
	scopePolicy := oauth.NewScopePolicy(reg)

	publicServer := newScopedServer("", core.impl, core.serverOpts, logger, metrics, core.knownTools, idx, cfg, srcIdx, siteDB, pg, writeEnabled, extensions)
	writeServer, previews := buildWriteScopedServer(core, cfg, idx, extensions)
	adminServer := buildAdminScopedServer(core, cfg, idx, extensions, previews)
	previewHandler := previews.HTTPHandler()

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
		if rank >= 2 {
			return adminServer
		}
		if rank >= 1 {
			return writeServer
		}
		return publicServer
	}, opts)

	oauthSvc, tokenStore, err := initOAuthService(cfg)
	if err != nil {
		return nil, err
	}

	rateLimitedStreaming := oauth.NewRateLimiter(cfg.RateLimit).Middleware(streaming)

	maxBody := configuredMaxRequestBytes(cfg)
	mcpToolHandler := newMCPToolHandler(cfg, oauthSvc, scopePolicy, metrics, logger, rateLimitedStreaming, maxBody)
	protectedMCPHandler := newProtectedMCPHandler(cfg, oauthSvc, mcpToolHandler)
	rateLimitOAuth, resetIP := newOAuthAllocationLimiter(maxBody)
	handler := newRootHandler(cfg, oauthSvc, rateLimitOAuth, protectedMCPHandler, previewHandler, metrics)
	return &Server{
		cfg:           cfg,
		handler:       observability.RequestMiddleware(handler, logger),
		store:         tokenStore,
		oauthSvc:      oauthSvc,
		resetIPCounts: resetIP,
		siteDB:        siteDB,
	}, nil
}

// applyOAuthCORS sets CORS headers for browser-based OAuth clients calling
// /register, /authorize, or /token directly (not just navigating to them),
// and short-circuits an OPTIONS preflight with a 204. Before this existed,
// these three endpoints had no CORS support at all: an OPTIONS preflight
// got a plain 405 with no Access-Control-Allow-Origin header, so a
// browser-based OAuth client's cross-origin fetch()/XHR would fail the
// preflight and the browser would block the real request before it ever
// reached this server — surfacing to the client as a generic "can't
// connect" with nothing in this server's own request logs to explain it
// (observed: Mistral Le Chat, 2026-07-18). Discovery endpoints
// (serveDiscoveryJSON in discovery.go) already had this; "*" matches that
// same policy — these are public server metadata/registration surfaces,
// not authenticated data, so there's no per-origin access control to
// enforce here.
//
// Returns true if the request was an OPTIONS preflight (already fully
// handled — the caller must return immediately without invoking the real
// handler). Callers still get Access-Control-Allow-Origin set on their own
// actual GET/POST response too, since a preflight passing isn't enough —
// browsers also require CORS headers on the real response before letting
// client-side JS read it.
func applyOAuthCORS(w http.ResponseWriter, r *http.Request, allowedMethods string) bool {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", allowedMethods)
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
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
