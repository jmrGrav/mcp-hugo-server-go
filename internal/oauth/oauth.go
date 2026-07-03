package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/storage"
)

type ctxKey string

const CtxScope ctxKey = "oauth_scope"

type Service struct {
	cfg              config.OAuthConfig
	store            storage.Store
	mu               sync.RWMutex
	clients          map[string]client
	codes            map[string]authCode
	agentRegs        map[string]agentRegistration
	agentClaimTokens map[string]string
	agentClaims      map[string]agentClaim
}

type client struct {
	RedirectURIs []string
}

type authCode struct {
	ClientID            string
	RedirectURI         string
	ExpiresAt           time.Time
	CodeChallenge       string
	CodeChallengeMethod string
}

type RegistrationRequest struct {
	RedirectURIs []string `json:"redirect_uris"`
}

type RegistrationResponse struct {
	ClientID                      string   `json:"client_id"`
	ClientIDIssuedAt              int64    `json:"client_id_issued_at"`
	RedirectURIs                  []string `json:"redirect_uris"`
	GrantTypes                    []string `json:"grant_types"`
	ResponseTypes                 []string `json:"response_types"`
	TokenEndpointAuthMethod       string   `json:"token_endpoint_auth_method"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
	Scope                         string   `json:"scope"`
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in,omitempty"`
	Scope       string `json:"scope,omitempty"`
}

func NewService(cfg config.OAuthConfig, store storage.Store) *Service {
	if cfg.AuthCodeTTLSeconds <= 0 {
		cfg.AuthCodeTTLSeconds = 300
	}
	if cfg.AccessTokenTTLSeconds <= 0 {
		cfg.AccessTokenTTLSeconds = 3600
	}
	if len(cfg.TrustedAuthorizeCIDRs) == 0 {
		cfg.TrustedAuthorizeCIDRs = []string{"127.0.0.1/32", "::1/128"}
	}
	return &Service{
		cfg:              cfg,
		store:            store,
		clients:          make(map[string]client),
		codes:            make(map[string]authCode),
		agentRegs:        make(map[string]agentRegistration),
		agentClaimTokens: make(map[string]string),
		agentClaims:      make(map[string]agentClaim),
	}
}

func (s *Service) ValidateBearer(token string) (string, bool) {
	return s.store.ValidateAccessToken(HashToken(token))
}

func (s *Service) AuthorizationServerMetadata() map[string]any {
	issuer := strings.TrimRight(s.cfg.Issuer, "/")
	resource := strings.TrimSpace(s.cfg.Resource)
	if resource == "" {
		resource = issuer + "/mcp"
	}
	return map[string]any{
		"issuer":                                issuer,
		"authorization_endpoint":                issuer + "/authorize",
		"token_endpoint":                        issuer + "/token",
		"registration_endpoint":                 issuer + "/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "urn:ietf:params:oauth:grant-type:jwt-bearer", "urn:workos:agent-auth:grant-type:claim"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{"mcp"},
		"service_documentation":                 resource,
		"agent_auth": map[string]any{
			"skill":                    issuer + "/auth.md",
			"identity_endpoint":        issuer + "/agent/identity",
			"claim_endpoint":           issuer + "/agent/identity/claim",
			"events_endpoint":          issuer + "/agent/event/notify",
			"identity_types_supported": []string{"anonymous"},
			"identity_assertion": map[string]any{
				"assertion_types_supported": []string{"urn:ietf:params:oauth:token-type:id-jag"},
			},
			"events_supported": []string{
				"https://schemas.workos.com/events/agent/auth/identity/assertion/revoked",
			},
		},
	}
}

func (s *Service) registerClient(req RegistrationRequest) (*RegistrationResponse, error) {
	if !s.cfg.DynamicClientEnabled {
		return nil, fmt.Errorf("invalid_request: dynamic_client_registration_disabled")
	}
	if len(req.RedirectURIs) == 0 {
		return nil, fmt.Errorf("invalid_request: redirect_uris missing or empty")
	}
	for _, uri := range req.RedirectURIs {
		if !isAllowedRedirectURI(uri) {
			return nil, fmt.Errorf("invalid_redirect_uri")
		}
	}
	id := randomString(24)
	s.mu.Lock()
	s.clients[id] = client{RedirectURIs: append([]string(nil), req.RedirectURIs...)}
	s.mu.Unlock()
	return &RegistrationResponse{
		ClientID:                      id,
		ClientIDIssuedAt:              time.Now().Unix(),
		RedirectURIs:                  append([]string(nil), req.RedirectURIs...),
		GrantTypes:                    []string{"authorization_code"},
		ResponseTypes:                 []string{"code"},
		TokenEndpointAuthMethod:       "none",
		CodeChallengeMethodsSupported: []string{"S256"},
		Scope:                         "mcp",
	}, nil
}

func (s *Service) validateClientRedirect(clientID, uri string) (string, error) {
	s.mu.RLock()
	c, ok := s.clients[clientID]
	s.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("unauthorized_client")
	}
	for _, r := range c.RedirectURIs {
		if r == uri {
			return r, nil
		}
	}
	return "", fmt.Errorf("invalid_redirect_uri")
}

func (s *Service) issueAuthCode(sourceIP, responseType, clientID, redirectURI, state, codeChallenge, codeChallengeMethod string) (string, error) {
	if !s.sourceAllowed(sourceIP) {
		return "", fmt.Errorf("access_denied: authorize source is not trusted")
	}
	if responseType != "code" {
		return "", fmt.Errorf("unsupported_response_type")
	}
	if state == "" {
		return "", fmt.Errorf("invalid_request: missing state parameter")
	}
	if codeChallenge == "" && s.cfg.RequirePKCE {
		return "", fmt.Errorf("invalid_request: pkce_mandatory")
	}
	if codeChallenge != "" {
		if codeChallengeMethod != "S256" {
			return "", fmt.Errorf("invalid_request: unsupported code_challenge_method")
		}
		if len(codeChallenge) < 43 || len(codeChallenge) > 128 {
			return "", fmt.Errorf("invalid_request: code_challenge length invalid")
		}
	}
	if _, err := s.validateClientRedirect(clientID, redirectURI); err != nil {
		return "", err
	}
	code := randomString(32)
	s.mu.Lock()
	s.codes[code] = authCode{
		ClientID:            clientID,
		RedirectURI:         redirectURI,
		ExpiresAt:           time.Now().Add(time.Duration(s.cfg.AuthCodeTTLSeconds) * time.Second),
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
	}
	s.mu.Unlock()
	return code, nil
}

func (s *Service) exchangeToken(grantType, clientID, redirectURI, code, codeVerifier string) (*TokenResponse, error) {
	if grantType != "authorization_code" {
		return nil, fmt.Errorf("unsupported_grant_type")
	}
	s.mu.Lock()
	data, ok := s.codes[code]
	if ok {
		delete(s.codes, code)
	}
	s.mu.Unlock()
	if !ok || data.ExpiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("invalid_grant: invalid or expired code")
	}
	if subtle.ConstantTimeCompare([]byte(clientID), []byte(data.ClientID)) != 1 {
		return nil, fmt.Errorf("invalid_client")
	}
	if redirectURI == "" || redirectURI != data.RedirectURI {
		return nil, fmt.Errorf("invalid_grant: redirect_uri mismatch")
	}
	if data.CodeChallenge != "" && !ValidatePKCE(data.CodeChallenge, codeVerifier) {
		return nil, fmt.Errorf("invalid_grant: pkce verification failed")
	}
	token := randomString(32)
	ttl := time.Duration(s.cfg.AccessTokenTTLSeconds) * time.Second
	_ = s.store.AddAccessToken(HashToken(token), "mcp", time.Now().Add(ttl))
	return &TokenResponse{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   s.cfg.AccessTokenTTLSeconds,
		Scope:       "mcp",
	}, nil
}

func (s *Service) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req RegistrationRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&req); err != nil {
		writeOAuthError(w, "invalid_request", http.StatusBadRequest)
		return
	}
	resp, err := s.registerClient(req)
	if err != nil {
		writeOAuthError(w, oauthRegisterErrorCode(err), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Service) HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, "invalid_request", http.StatusBadRequest)
		return
	}
	clientID := r.Form.Get("client_id")
	rawRedirectURI := r.Form.Get("redirect_uri")
	safeRedirectURI, err := s.validateClientRedirect(clientID, rawRedirectURI)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	code, err := s.issueAuthCode(
		requestSourceIP(r),
		r.Form.Get("response_type"),
		clientID,
		safeRedirectURI,
		r.Form.Get("state"),
		r.Form.Get("code_challenge"),
		r.Form.Get("code_challenge_method"),
	)
	if err != nil {
		status := oauthAuthorizeErrorStatus(err)
		if strings.Contains(err.Error(), "unauthorized_client") || strings.Contains(err.Error(), "access_denied") {
			http.Error(w, oauthAuthorizeErrorCode(err), status)
			return
		}
		params := url.Values{}
		params.Set("error", oauthAuthorizeErrorCode(err))
		if state := r.Form.Get("state"); state != "" {
			params.Set("state", state)
		}
		http.Redirect(w, r, safeRedirectURI+"?"+params.Encode(), http.StatusFound)
		return
	}
	params := url.Values{"code": {code}}
	if state := r.Form.Get("state"); state != "" {
		params.Set("state", state)
	}
	http.Redirect(w, r, safeRedirectURI+"?"+params.Encode(), http.StatusFound)
}

func (s *Service) HandleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, "invalid_request", http.StatusBadRequest)
		return
	}
	grantType := r.FormValue("grant_type")
	if grantType == "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		resp, err := s.exchangeAgentAssertion(r.FormValue("assertion"))
		if err != nil {
			writeOAuthError(w, "invalid_grant", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}
	resp, err := s.exchangeToken(
		grantType,
		r.FormValue("client_id"),
		r.FormValue("redirect_uri"),
		r.FormValue("code"),
		r.FormValue("code_verifier"),
	)
	if err != nil {
		writeOAuthError(w, oauthTokenErrorCode(err), oauthTokenErrorStatus(err))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Service) sourceAllowed(ipText string) bool {
	ip := net.ParseIP(strings.TrimSpace(ipText))
	if ip == nil {
		return false
	}
	for _, raw := range s.cfg.TrustedAuthorizeCIDRs {
		_, cidr, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func isAllowedRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	if u.Hostname() == "localhost" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

func requestSourceIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := r.RemoteAddr
	if strings.Contains(host, ":") {
		if splitHost, _, err := net.SplitHostPort(host); err == nil {
			host = splitHost
		}
	}
	return host
}

func writeOAuthError(w http.ResponseWriter, code string, status int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func oauthAuthorizeErrorCode(err error) string {
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "unsupported_response_type"):
		return "unsupported_response_type"
	case strings.HasPrefix(msg, "access_denied"):
		return "access_denied"
	case strings.HasPrefix(msg, "unauthorized_client"):
		return "unauthorized_client"
	default:
		return "invalid_request"
	}
}

func oauthAuthorizeErrorStatus(err error) int {
	if strings.HasPrefix(err.Error(), "access_denied") {
		return http.StatusForbidden
	}
	if strings.HasPrefix(err.Error(), "unauthorized_client") {
		return http.StatusUnauthorized
	}
	return http.StatusBadRequest
}

func oauthTokenErrorCode(err error) string {
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "unsupported_grant_type"):
		return "unsupported_grant_type"
	case strings.HasPrefix(msg, "invalid_client"):
		return "invalid_client"
	case strings.HasPrefix(msg, "invalid_grant"):
		return "invalid_grant"
	default:
		return "invalid_request"
	}
}

func oauthTokenErrorStatus(err error) int {
	if strings.HasPrefix(err.Error(), "invalid_client") {
		return http.StatusUnauthorized
	}
	return http.StatusBadRequest
}

func oauthRegisterErrorCode(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "registration_disabled") || strings.Contains(msg, "dynamic_client_registration_disabled"):
		return "invalid_request"
	case strings.Contains(msg, "redirect_uri"):
		return "invalid_redirect_uri"
	default:
		return "invalid_client_metadata"
	}
}

func CodeChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func ValidatePKCE(challenge, verifier string) bool {
	if challenge == "" || verifier == "" {
		return false
	}
	expected := CodeChallengeS256(verifier)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(challenge)) == 1
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomString(bytesLen int) string {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
