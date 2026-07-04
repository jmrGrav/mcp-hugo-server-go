package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
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

const (
	Name    = "mcp-hugo-server-go"
	Version = "v0.1.0"
)

type Server struct {
	cfg     config.Config
	handler http.Handler
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

func New(cfg config.Config, idx *site.Index) (*Server, error) {
	impl := &mcp.Implementation{Name: Name, Version: Version}

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

	anonServer := mcp.NewServer(impl, nil)
	anonymous.Register(anonServer, idx, cfg)

	readServer := mcp.NewServer(impl, nil)
	anonymous.Register(readServer, idx, cfg)
	read.Register(readServer, idx, cfg)

	writeServer := mcp.NewServer(impl, nil)
	anonymous.Register(writeServer, idx, cfg)
	read.Register(writeServer, idx, cfg)
	if writeEnabled {
		toolswrite.Register(writeServer, pg, srcIdx, cfg)
	}

	siteAdminServer := mcp.NewServer(impl, nil)
	anonymous.Register(siteAdminServer, idx, cfg)
	read.Register(siteAdminServer, idx, cfg)
	if writeEnabled {
		toolswrite.Register(siteAdminServer, pg, srcIdx, cfg)
	}
	admin.RegisterSiteAdmin(siteAdminServer, cfg)

	sysAdminServer := mcp.NewServer(impl, nil)
	anonymous.Register(sysAdminServer, idx, cfg)
	read.Register(sysAdminServer, idx, cfg)
	if writeEnabled {
		toolswrite.Register(sysAdminServer, pg, srcIdx, cfg)
	}
	admin.Register(sysAdminServer, cfg)

	opts := &mcp.StreamableHTTPOptions{
		Stateless:                  true,
		DisableLocalhostProtection: true,
	}
	streaming := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		scope, _ := r.Context().Value(oauth.CtxScope).(string)
		switch tools.ScopeRank(scope) {
		case 4:
			return sysAdminServer
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
	if cfg.OAuth.Enabled {
		store, err := openStore(cfg.OAuth)
		if err != nil {
			return nil, err
		}
		oauthSvc = oauth.NewService(cfg.OAuth, store)
	}

	rateLimitedStreaming := oauth.NewRateLimiter(cfg.RateLimit).Middleware(streaming)

	maxBody := cfg.MaxRequestBytes
	if maxBody <= 0 {
		maxBody = 1 << 20
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			handleOAuthAuthServer(w, r, cfg)
		case "/.well-known/oauth-protected-resource":
			handleOAuthProtectedResource(w, r, cfg)
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
			oauthSvc.HandleRegister(w, r)
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
			oauthSvc.HandleAgentIdentity(w, r)
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
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}

			callerScope := ""
			if oauthSvc != nil {
				auth := strings.TrimSpace(r.Header.Get("Authorization"))
				if auth != "" {
					if !strings.HasPrefix(auth, "Bearer ") {
						w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
						http.Error(w, "unauthorized", http.StatusUnauthorized)
						return
					}
					token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
					scope, ok := oauthSvc.ValidateBearer(token)
					if !ok {
						w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
						http.Error(w, "unauthorized", http.StatusUnauthorized)
						return
					}
					callerScope = scope
					r = r.WithContext(context.WithValue(r.Context(), oauth.CtxScope, scope))
				}

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

			rateLimitedStreaming.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	logger := observability.NewLogger()
	return &Server{cfg: cfg, handler: observability.RequestMiddleware(handler, logger)}, nil
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) Run(ctx context.Context) error {
	shutdownTimeout := 15 * time.Second

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", s.cfg.HTTPBindAddr, s.cfg.HTTPBindPort),
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
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
	return nil
}
