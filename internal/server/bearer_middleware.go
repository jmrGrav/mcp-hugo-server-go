package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

type mcpBearerResult struct {
	scope  string
	legacy bool
}

type interceptResponseWriter struct {
	real        http.ResponseWriter
	header      http.Header
	status      int
	body        bytes.Buffer
	passThrough bool
}

func newInterceptResponseWriter(w http.ResponseWriter) *interceptResponseWriter {
	return &interceptResponseWriter{
		real:   w,
		header: make(http.Header),
	}
}

func (w *interceptResponseWriter) Header() http.Header {
	if w.passThrough {
		return w.real.Header()
	}
	return w.header
}

func (w *interceptResponseWriter) WriteHeader(status int) {
	if w.passThrough {
		w.real.WriteHeader(status)
		return
	}
	w.status = status
}

func (w *interceptResponseWriter) Write(p []byte) (int, error) {
	if w.passThrough {
		return w.real.Write(p)
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(p)
}

func (w *interceptResponseWriter) Flush() {
	if w.passThrough {
		if flusher, ok := w.real.(http.Flusher); ok {
			flusher.Flush()
		}
	}
}

func (w *interceptResponseWriter) flushBufferedToReal() {
	dst := w.real.Header()
	for k, vals := range w.header {
		dst.Del(k)
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
	if w.status != 0 {
		w.real.WriteHeader(w.status)
	}
	if w.body.Len() > 0 {
		_, _ = io.Copy(w.real, &w.body)
	}
}

func normalizeMCPBearerChallenge(realm, resourceMetadataURL string, invalidToken bool) string {
	parts := []string{`realm="` + realm + `"`}
	if resourceMetadataURL != "" {
		parts = append(parts, `resource_metadata="`+resourceMetadataURL+`"`)
	}
	if invalidToken {
		parts = append(parts, `error="invalid_token"`)
	}
	return "Bearer " + strings.Join(parts, ", ")
}

func rejectMCPBearer(w http.ResponseWriter, r *http.Request, reason, realm, resourceMetadataURL string, invalidToken bool) {
	logMCPAuthRejection(r, reason)
	w.Header().Set("WWW-Authenticate", normalizeMCPBearerChallenge(realm, resourceMetadataURL, invalidToken))
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func newMCPBearerAuthMiddleware(verifier sdkauth.TokenVerifier, realm, resourceMetadataURL string) func(http.Handler) http.Handler {
	sdkMiddleware := sdkauth.RequireBearerToken(verifier, &sdkauth.RequireBearerTokenOptions{
		ResourceMetadataURL: resourceMetadataURL,
	})
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
			if authHeader == "" {
				rejectMCPBearer(w, r, "missing_bearer", realm, resourceMetadataURL, false)
				return
			}
			if !strings.HasPrefix(authHeader, "Bearer ") {
				rejectMCPBearer(w, r, "invalid_bearer_format", realm, resourceMetadataURL, false)
				return
			}

			intercept := newInterceptResponseWriter(w)
			calledNext := false
			wrappedNext := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calledNext = true
				if iw, ok := w.(*interceptResponseWriter); ok {
					iw.passThrough = true
				}
				next.ServeHTTP(w, r)
			})

			sdkMiddleware(wrappedNext).ServeHTTP(intercept, r)
			if calledNext {
				return
			}

			if intercept.status == http.StatusUnauthorized {
				rejectMCPBearer(w, r, "invalid_token", realm, resourceMetadataURL, true)
				return
			}
			intercept.flushBufferedToReal()
		})
	}
}

func oauthTokenVerifier(oauthSvc *oauth.Service) sdkauth.TokenVerifier {
	return func(ctx context.Context, token string, req *http.Request) (*sdkauth.TokenInfo, error) {
		scope, expiresAt, legacy, ok := oauthSvc.ValidateBearerInfo(token)
		if !ok {
			return nil, sdkauth.ErrInvalidToken
		}
		return &sdkauth.TokenInfo{
			Scopes:     []string{scope},
			Expiration: expiresAt,
			Extra: map[string]any{
				"mcp_bearer": mcpBearerResult{
					scope:  scope,
					legacy: legacy,
				},
			},
		}, nil
	}
}

func bearerResultFromContext(ctx context.Context) (mcpBearerResult, bool) {
	ti := sdkauth.TokenInfoFromContext(ctx)
	if ti == nil || ti.Extra == nil {
		return mcpBearerResult{}, false
	}
	result, ok := ti.Extra["mcp_bearer"].(mcpBearerResult)
	return result, ok
}
