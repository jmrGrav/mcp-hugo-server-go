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
	"log/slog"
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

// CtxCallerIP carries the caller's remote IP so tool handlers can maintain
// per-caller state (e.g. per-caller rate limiters) without access to the
// underlying http.Request.
const CtxCallerIP ctxKey = "caller_ip"

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

type oauthClientPersister interface {
	UpsertOAuthClient(clientID, secretHash string, enabled bool, redirectURIs, scopes []string) error
}

type client struct {
	RedirectURIs []string
	SecretHash   string
	Scope        string
	Enabled      bool
}

type authCode struct {
	ClientID            string
	RedirectURI         string
	Scope               string
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

func (s *Service) LoadClientRegistry(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	registry, err := loadClientRegistry(path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range registry.Clients {
		clientID := firstNonEmpty(entry.ID, entry.ClientID)
		if clientID == "" {
			return fmt.Errorf("invalid_client_registry: client_id missing")
		}
		if _, exists := s.clients[clientID]; exists {
			return fmt.Errorf("invalid_client_registry: duplicate client_id %q", clientID)
		}
		redirects := make([]string, 0, len(entry.RedirectURIs))
		for _, uri := range entry.RedirectURIs {
			if err := validateRegisteredRedirectURI(uri); err != nil {
				return fmt.Errorf("invalid_client_registry: invalid redirect_uri for %q", clientID)
			}
			redirects = append(redirects, uri)
		}
		scopes, err := normalizeConfiguredScopes(entry.Scopes, entry.Scope)
		if err != nil {
			return fmt.Errorf("invalid_client_registry: %w", err)
		}
		scope := highestConfiguredScope(scopes)
		enabled := true
		if entry.Enabled != nil {
			enabled = *entry.Enabled
		}
		secretHash := strings.TrimSpace(entry.SecretHash)
		secret := firstNonEmpty(entry.Secret, entry.ClientSecret)
		if secretHash == "" && secret != "" {
			secretHash = HashToken(secret)
		}
		if secretHash == "" {
			return fmt.Errorf("invalid_client_registry: client_secret or client_secret_hash required for %q", clientID)
		}
		s.clients[clientID] = client{
			RedirectURIs: redirects,
			SecretHash:   secretHash,
			Scope:        scope,
			Enabled:      enabled,
		}
		if persister, ok := s.store.(oauthClientPersister); ok {
			if err := persister.UpsertOAuthClient(clientID, secretHash, enabled, redirects, scopes); err != nil {
				return fmt.Errorf("invalid_client_registry: persist client %q: %w", clientID, err)
			}
		}
	}
	return nil
}

// PurgeExpired removes expired auth codes and agent state.
// Call periodically (e.g., every 5 minutes) to prevent unbounded map growth.
func (s *Service) PurgeExpired() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for code, data := range s.codes {
		if data.ExpiresAt.Before(now) {
			delete(s.codes, code)
		}
	}
	for assertion, reg := range s.agentRegs {
		if reg.AssertionExpires.Before(now) {
			delete(s.agentClaimTokens, reg.ClaimToken)
			delete(s.agentRegs, assertion)
		}
	}
	for attemptID, claim := range s.agentClaims {
		if claim.ExpiresAt.Before(now) {
			delete(s.agentClaims, attemptID)
		}
	}
}

func (s *Service) ValidateBearer(token string) (string, bool) {
	scope, _, ok := s.ValidateBearerDetails(token)
	return scope, ok
}

// ValidateBearerDetails validates a bearer token and returns the canonical
// scope, whether the stored scope was a deprecated alias, and whether the token
// was accepted.
func (s *Service) ValidateBearerDetails(token string) (string, bool, bool) {
	scope, ok := s.store.ValidateAccessToken(HashToken(token))
	if !ok {
		return "", false, false
	}
	return CanonicalScope(scope), IsLegacyScope(scope), true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *Service) resolveClientScope(clientID, requested string) (string, error) {
	c, ok := s.lookupClient(clientID)
	if !ok {
		return "", fmt.Errorf("unauthorized_client")
	}
	scope, err := requestedScope(requested)
	if err != nil {
		return "", fmt.Errorf("invalid_scope")
	}
	if scope == "" {
		scope = c.Scope
	}
	// Clamp to the client's maximum allowed scope (RFC 6749 §3.3: server MAY
	// grant a subset when the requested scope exceeds what the client is
	// permitted). Returning invalid_scope would break clients like Claude.ai
	// that always request all scopes and rely on the server to downscope.
	if !allowedScope(scope, c.Scope) {
		slog.Info("OAuth scope clamped",
			"client", clientID,
			"requested", scope,
			"granted", c.Scope,
			"reason", "client_max_scope",
		)
		scope = c.Scope
	}
	return scope, nil
}

func (s *Service) registerClient(req RegistrationRequest) (*RegistrationResponse, error) {
	if !s.cfg.DynamicClientEnabled {
		return nil, fmt.Errorf("invalid_request: dynamic_client_registration_disabled")
	}
	if len(req.RedirectURIs) == 0 {
		return nil, fmt.Errorf("invalid_request: redirect_uris missing or empty")
	}
	for _, uri := range req.RedirectURIs {
		if err := validateRegisteredRedirectURI(uri); err != nil {
			return nil, fmt.Errorf("invalid_redirect_uri")
		}
	}
	id := randomString(24)
	scope := s.resolveRegistrationScope(req.RedirectURIs)
	s.mu.Lock()
	s.clients[id] = client{RedirectURIs: append([]string(nil), req.RedirectURIs...), Scope: scope, Enabled: true}
	s.mu.Unlock()
	return &RegistrationResponse{
		ClientID:                      id,
		ClientIDIssuedAt:              time.Now().Unix(),
		RedirectURIs:                  append([]string(nil), req.RedirectURIs...),
		GrantTypes:                    []string{"authorization_code"},
		ResponseTypes:                 []string{"code"},
		TokenEndpointAuthMethod:       "none",
		CodeChallengeMethodsSupported: []string{"S256"},
		Scope:                         scope,
	}, nil
}

// resolveRegistrationScope returns the highest scope among all pre-registered
// (secret-bearing) clients whose redirect URIs overlap with the DCR request.
// This lets Claude.ai and ChatGPT inherit the scope of their pre-registered
// counterparts when they do Dynamic Client Registration. Falls back to
// "content.read" when no pre-registered client matches.
func (s *Service) resolveRegistrationScope(redirectURIs []string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	scopes := []string{"content.read"}
clientLoop:
	for _, c := range s.clients {
		if c.SecretHash == "" {
			continue // public DCR client — skip, only match pre-registered clients
		}
		for _, reqURI := range redirectURIs {
			for _, regURI := range c.RedirectURIs {
				if matchRedirectURI(regURI, reqURI) {
					scopes = append(scopes, c.Scope)
					continue clientLoop
				}
			}
		}
	}
	return highestConfiguredScope(scopes)
}

func (s *Service) validateClientRedirect(clientID, uri string) (string, error) {
	u, err := s.validateClientRedirectURL(clientID, uri)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *Service) validateClientRedirectURL(clientID, uri string) (*url.URL, error) {
	c, ok := s.lookupClient(clientID)
	if !ok {
		return nil, fmt.Errorf("unauthorized_client")
	}
	parsed, err := url.Parse(strings.TrimSpace(uri))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid_redirect_uri")
	}
	for _, r := range c.RedirectURIs {
		if matchRedirectURI(r, parsed.String()) {
			return parsed, nil
		}
	}
	return nil, fmt.Errorf("invalid_redirect_uri")
}

func oauthRedirectLocation(redirectURI *url.URL, params url.Values) string {
	if redirectURI == nil {
		return ""
	}
	target := *redirectURI
	query := target.Query()
	for key, values := range params {
		query.Del(key)
		for _, value := range values {
			query.Add(key, value)
		}
	}
	target.RawQuery = query.Encode()
	return target.String()
}

func redirectToRegisteredClient(w http.ResponseWriter, r *http.Request, redirectURI *url.URL, params url.Values) {
	if redirectURI == nil || redirectURI.Scheme == "" || redirectURI.Host == "" {
		http.Error(w, "invalid_redirect_uri", http.StatusBadRequest)
		return
	}
	if redirectURI.Scheme != "https" && !(redirectURI.Scheme == "http" && isLoopbackHost(redirectURI.Hostname())) {
		http.Error(w, "invalid_redirect_uri", http.StatusBadRequest)
		return
	}
	// redirectURI is allowlist-validated by validateClientRedirectURL before
	// this helper is called, and this final guard rejects non-HTTPS targets
	// except loopback HTTP used for local OAuth clients.

	// codeql[go/unvalidated-url-redirection]
	w.Header().Set("Location", oauthRedirectLocation(redirectURI, params))
	w.WriteHeader(http.StatusFound)
}

func (s *Service) lookupClient(clientID string) (client, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.clients[clientID]
	if !ok || !c.Enabled {
		return client{}, false
	}
	return c, true
}

func (s *Service) issueAuthCode(sourceIP, responseType, clientID, redirectURI, state, requestedScope, codeChallenge, codeChallengeMethod string) (string, error) {
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
	scope, err := s.resolveClientScope(clientID, requestedScope)
	if err != nil {
		return "", err
	}
	code := randomString(32)
	s.mu.Lock()
	s.codes[code] = authCode{
		ClientID:            clientID,
		RedirectURI:         redirectURI,
		Scope:               scope,
		ExpiresAt:           time.Now().Add(time.Duration(s.cfg.AuthCodeTTLSeconds) * time.Second),
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
	}
	s.mu.Unlock()
	return code, nil
}

func (s *Service) exchangeToken(grantType, clientID, clientSecret, redirectURI, code, codeVerifier string) (*TokenResponse, error) {
	if grantType != "authorization_code" {
		return nil, fmt.Errorf("unsupported_grant_type")
	}
	c, ok := s.lookupClient(clientID)
	if !ok {
		return nil, fmt.Errorf("invalid_client")
	}
	if c.SecretHash != "" {
		if clientSecret == "" || subtle.ConstantTimeCompare([]byte(HashToken(clientSecret)), []byte(c.SecretHash)) != 1 {
			return nil, fmt.Errorf("invalid_client")
		}
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
	scope := CanonicalScope(data.Scope)
	if scope == "" {
		scope = "content.read"
	}
	if err := s.store.AddAccessToken(HashToken(token), scope, time.Now().Add(ttl)); err != nil {
		return nil, fmt.Errorf("server_error: store token: %w", err)
	}
	return &TokenResponse{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   s.cfg.AccessTokenTTLSeconds,
		Scope:       scope,
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
	safeRedirectURI, err := s.validateClientRedirectURL(clientID, rawRedirectURI)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	code, err := s.issueAuthCode(
		requestSourceIP(r),
		r.Form.Get("response_type"),
		clientID,
		safeRedirectURI.String(),
		r.Form.Get("state"),
		r.Form.Get("scope"),
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
		redirectToRegisteredClient(w, r, safeRedirectURI, params)
		return
	}
	params := url.Values{"code": {code}}
	if state := r.Form.Get("state"); state != "" {
		params.Set("state", state)
	}
	redirectToRegisteredClient(w, r, safeRedirectURI, params)
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
			errCode := oauthTokenErrorCode(err)
			if strings.Contains(err.Error(), "assertion_not_found") {
				// In-memory assertion state was lost (server restart). Signal
				// that the client should re-register immediately.
				w.Header().Set("Retry-After", "0")
			}
			writeOAuthError(w, errCode, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}
	clientID, clientSecret := tokenClientCredentials(r)
	resp, err := s.exchangeToken(
		grantType,
		clientID,
		clientSecret,
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

// requestSourceIP returns the network-level source IP from RemoteAddr only.
// This is used for the trusted-CIDR authorization check; proxy headers are
// intentionally ignored to prevent CIDR bypass via header injection (#54).
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

// tokenClientCredentials extracts client_id and client_secret per RFC 6749 §2.3.1.
// HTTP Basic Auth takes precedence: username = client_id, password = client_secret.
// Falls back to form parameters when Basic Auth is absent.
func tokenClientCredentials(r *http.Request) (clientID, clientSecret string) {
	if r == nil {
		return "", ""
	}
	if user, pass, ok := r.BasicAuth(); ok && user != "" {
		return user, pass
	}
	return r.FormValue("client_id"), r.FormValue("client_secret")
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
	case strings.HasPrefix(msg, "invalid_scope"):
		return "invalid_scope"
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
	case strings.HasPrefix(msg, "invalid_scope"):
		return "invalid_scope"
	case strings.HasPrefix(msg, "server_error"):
		return "server_error"
	default:
		return "invalid_request"
	}
}

func oauthTokenErrorStatus(err error) int {
	if strings.HasPrefix(err.Error(), "server_error") {
		return http.StatusInternalServerError
	}
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
