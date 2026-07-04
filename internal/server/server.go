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
	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/observability"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/storage"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/anonymous"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
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

func New(cfg config.Config, idx *site.Index) (*Server, error) {
	impl := &mcp.Implementation{Name: Name, Version: Version}

	anonServer := mcp.NewServer(impl, nil)
	anonymous.Register(anonServer, idx, cfg)

	readServer := mcp.NewServer(impl, nil)
	anonymous.Register(readServer, idx, cfg)
	read.Register(readServer, idx, cfg)

	opts := &mcp.StreamableHTTPOptions{
		Stateless:                  true,
		DisableLocalhostProtection: true,
	}
	streaming := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		scope, _ := r.Context().Value(oauth.CtxScope).(string)
		if tools.ScopeRank(scope) >= 1 { // content.read or higher
			return readServer
		}
		return anonServer
	}, opts)

	var oauthSvc *oauth.Service
	if cfg.OAuth.Enabled {
		oauthSvc = oauth.NewService(cfg.OAuth, storage.NewMemory())
	}

	aclPolicy := oauth.NewACLPolicy([]string{
		"list_pages", "get_page", "search_pages", "get_recent_posts",
		"list_tags", "list_categories", "get_sitemap", "get_feed", "get_site_information",
	})

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
					r = r.WithContext(context.WithValue(r.Context(), oauth.CtxScope, scope))
				} else {
					body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					if err != nil {
						http.Error(w, "bad request", http.StatusBadRequest)
						return
					}
					if !aclPolicy.AllowRequest(body) {
						reason := aclPolicy.DenyReason(body)
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
			streaming.ServeHTTP(w, r)
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
	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", s.cfg.HTTPBindAddr, s.cfg.HTTPBindPort),
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
