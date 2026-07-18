package oauth_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/storage"
)

func withDefaultLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func newTestService(t *testing.T, cidrs ...string) (*oauth.Service, storage.Store) {
	t.Helper()
	if len(cidrs) == 0 {
		cidrs = []string{"127.0.0.1/32", "::1/128"}
	}
	cfg := config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		Resource:              "https://mcp.test/mcp",
		DynamicClientEnabled:  true,
		RequirePKCE:           false,
		TrustedAuthorizeCIDRs: cidrs,
		AuthCodeTTLSeconds:    300,
		AccessTokenTTLSeconds: 3600,
	}
	store := storage.NewMemory()
	svc := oauth.NewService(cfg, store)
	return svc, store
}

func newTestServiceWithConfig(t *testing.T, cfg config.OAuthConfig) (*oauth.Service, storage.Store) {
	t.Helper()
	if len(cfg.TrustedAuthorizeCIDRs) == 0 {
		cfg.TrustedAuthorizeCIDRs = []string{"127.0.0.1/32", "::1/128"}
	}
	if cfg.Issuer == "" {
		cfg.Issuer = "https://mcp.test"
	}
	if cfg.Resource == "" {
		cfg.Resource = "https://mcp.test/mcp"
	}
	if cfg.AccessTokenTTLSeconds == 0 {
		cfg.AccessTokenTTLSeconds = 3600
	}
	store := storage.NewMemory()
	svc := oauth.NewService(cfg, store)
	return svc, store
}

func registerClient(t *testing.T, svc *oauth.Service, redirectURIs []string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"redirect_uris": redirectURIs})
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	svc.HandleRegister(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: status = %d body = %q", rec.Code, rec.Body.String())
	}
	var resp struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("register: decode: %v", err)
	}
	if resp.ClientID == "" {
		t.Fatal("register: empty client_id")
	}
	return resp.ClientID
}

func TestDynamicClientRegistration(t *testing.T) {
	svc, _ := newTestService(t)
	clientID := registerClient(t, svc, []string{"https://client.test/callback"})
	if clientID == "" {
		t.Fatal("expected non-empty client_id")
	}
}

func TestDynamicClientRegistrationNeverGrantsPrivilegedScope(t *testing.T) {
	// #497: DCR is unauthenticated (no secret, no proof of redirect_uri
	// ownership) and /authorize is a plain HTTP endpoint, so a caller can read
	// the redirect Location header themselves without controlling the target
	// domain. A public DCR client must therefore always get "read", even when
	// its requested redirect_uri textually matches a privileged pre-registered
	// client's (e.g. claude-admin's or chatgpt-write's) callback URI. Write
	// access is only obtainable via the pre-registered, secret-bearing
	// client_id directly, never inherited through DCR.
	svc, _ := newTestService(t)

	registryYAML := `clients:
- client_id: claude-admin
  client_secret: test-secret-abc
  redirect_uris:
    - https://claude.ai/api/oauth/callback
    - https://claude.ai/oauth/callback
  scope: site.admin
- client_id: chatgpt-write
  client_secret: test-secret-xyz
  redirect_uris:
    - https://chatgpt.com/aip/oauth/callback
  scope: content.write
`
	registryPath := filepath.Join(t.TempDir(), "clients.yaml")
	if err := os.WriteFile(registryPath, []byte(registryYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.LoadClientRegistry(registryPath); err != nil {
		t.Fatalf("LoadClientRegistry: %v", err)
	}

	tests := []struct {
		name          string
		redirectURIs  []string
		expectedScope string
	}{
		{"claude.ai primary callback → read only, not write", []string{"https://claude.ai/api/oauth/callback"}, "read"},
		{"claude.ai alternate callback → read only, not write", []string{"https://claude.ai/oauth/callback"}, "read"},
		{"chatgpt callback → read only, not write", []string{"https://chatgpt.com/aip/oauth/callback"}, "read"},
		{"unknown client → read", []string{"https://unknown.example.com/callback"}, "read"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"redirect_uris": tc.redirectURIs})
			req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			svc.HandleRegister(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
			}
			var resp struct {
				ClientID string `json:"client_id"`
				Scope    string `json:"scope"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Scope != tc.expectedScope {
				t.Errorf("scope = %q want %q", resp.Scope, tc.expectedScope)
			}
		})
	}
}

func TestDynamicClientRegistrationDisabled(t *testing.T) {
	cfg := config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		DynamicClientEnabled:  false,
		AccessTokenTTLSeconds: 3600,
	}
	svc := oauth.NewService(cfg, storage.NewMemory())
	body := []byte(`{"redirect_uris":["https://client.test/callback"]}`)
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	svc.HandleRegister(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d want 400", rec.Code)
	}
	var errResp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.Error != "invalid_request" {
		t.Fatalf("error = %q want invalid_request", errResp.Error)
	}
}

func TestDCRAnonymousScopePreservedThroughTokenExchange(t *testing.T) {
	// A DCR client whose redirect_uri matches no pre-registered client must get
	// read-only scope all the way through: registration → authorize → token.
	// The token exchange must NOT promote "read" to "write" (#249, #497).
	svc, store := newTestService(t)
	clientID := registerClient(t, svc, []string{"https://scanner.example.com/callback"})

	// Registration should return scope "read".
	body, _ := json.Marshal(map[string]any{"redirect_uris": []string{"https://scanner.example.com/callback"}})
	regReq := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	regReq.Header.Set("Content-Type", "application/json")
	regRec := httptest.NewRecorder()
	svc.HandleRegister(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register: status = %d body = %q", regRec.Code, regRec.Body.String())
	}
	var regResp struct {
		Scope string `json:"scope"`
	}
	_ = json.Unmarshal(regRec.Body.Bytes(), &regResp)
	if regResp.Scope != "read" {
		t.Errorf("registration scope = %q want \"read\"", regResp.Scope)
	}

	// Complete PKCE flow.
	verifier := "anon-verifier-anon-verifier-anon-verifier-ano0"
	challenge := oauth.CodeChallengeS256(verifier)
	authURL := "/authorize?" + url.Values{
		"response_type": {"code"}, "client_id": {clientID},
		"redirect_uri": {"https://scanner.example.com/callback"}, "state": {"s1"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}.Encode()
	authReq := httptest.NewRequest(http.MethodGet, authURL, nil)
	authReq.RemoteAddr = "127.0.0.1:9999"
	authRec := httptest.NewRecorder()
	svc.HandleAuthorize(authRec, authReq)
	if authRec.Code != http.StatusFound {
		t.Fatalf("authorize: status = %d body = %q", authRec.Code, authRec.Body.String())
	}
	loc, _ := url.Parse(authRec.Header().Get("Location"))
	code := loc.Query().Get("code")

	tokenForm := url.Values{
		"grant_type": {"authorization_code"}, "client_id": {clientID},
		"code": {code}, "redirect_uri": {"https://scanner.example.com/callback"},
		"code_verifier": {verifier},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	svc.HandleToken(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token: status = %d body = %q", tokenRec.Code, tokenRec.Body.String())
	}
	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
	}
	_ = json.Unmarshal(tokenRec.Body.Bytes(), &tokenResp)
	if tokenResp.Scope != "read" {
		t.Errorf("token scope = %q want \"read\"", tokenResp.Scope)
	}

	// Token must validate as read scope, never write.
	scope, ok := store.ValidateAccessToken(oauth.HashToken(tokenResp.AccessToken))
	if !ok {
		t.Fatal("token: not found in store")
	}
	if scope != "read" {
		t.Errorf("stored scope = %q want \"read\"", scope)
	}
}

// TestPreRegisteredClientStillGetsWriteDirectly proves the #497 fix does not
// break the legitimate path: a pre-registered, secret-bearing client (not
// created via DCR) must still be able to obtain a write-scope token by
// authenticating with its own client_id/secret, since #497's fix only removes
// scope *inheritance* through DCR redirect_uri matching.
func TestPreRegisteredClientStillGetsWriteDirectly(t *testing.T) {
	svc, store := newTestService(t)

	registryYAML := `clients:
- client_id: claude-admin
  client_secret: test-secret-abc
  redirect_uris:
    - https://claude.ai/api/oauth/callback
  scope: site.admin
`
	registryPath := filepath.Join(t.TempDir(), "clients.yaml")
	if err := os.WriteFile(registryPath, []byte(registryYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.LoadClientRegistry(registryPath); err != nil {
		t.Fatalf("LoadClientRegistry: %v", err)
	}

	verifier := "admin-verifier-admin-verifier-admin-verifier-a0"
	challenge := oauth.CodeChallengeS256(verifier)
	authURL := "/authorize?" + url.Values{
		"response_type": {"code"}, "client_id": {"claude-admin"},
		"redirect_uri": {"https://claude.ai/api/oauth/callback"}, "state": {"s1"},
		"scope": {"write"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}.Encode()
	authReq := httptest.NewRequest(http.MethodGet, authURL, nil)
	authReq.RemoteAddr = "127.0.0.1:9999"
	authRec := httptest.NewRecorder()
	svc.HandleAuthorize(authRec, authReq)
	if authRec.Code != http.StatusFound {
		t.Fatalf("authorize: status = %d body = %q", authRec.Code, authRec.Body.String())
	}
	loc, _ := url.Parse(authRec.Header().Get("Location"))
	code := loc.Query().Get("code")

	tokenForm := url.Values{
		"grant_type": {"authorization_code"}, "client_id": {"claude-admin"},
		"client_secret": {"test-secret-abc"},
		"code":          {code}, "redirect_uri": {"https://claude.ai/api/oauth/callback"},
		"code_verifier": {verifier},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	svc.HandleToken(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token: status = %d body = %q", tokenRec.Code, tokenRec.Body.String())
	}
	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
	}
	_ = json.Unmarshal(tokenRec.Body.Bytes(), &tokenResp)
	if tokenResp.Scope != "write" {
		t.Errorf("token scope = %q want \"write\"", tokenResp.Scope)
	}
	scope, ok := store.ValidateAccessToken(oauth.HashToken(tokenResp.AccessToken))
	if !ok {
		t.Fatal("token: not found in store")
	}
	if scope != "write" {
		t.Errorf("stored scope = %q want \"write\"", scope)
	}
}

func TestPKCEFlow(t *testing.T) {
	svc, _ := newTestService(t)
	clientID := registerClient(t, svc, []string{"https://client.test/callback"})

	verifier := "test-verifier-test-verifier-test-verifier-test"
	challenge := oauth.CodeChallengeS256(verifier)

	authURL := "/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"https://client.test/callback"},
		"state":                 {"state-xyz"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()
	authReq := httptest.NewRequest(http.MethodGet, authURL, nil)
	authReq.RemoteAddr = "127.0.0.1:9999"
	authRec := httptest.NewRecorder()
	svc.HandleAuthorize(authRec, authReq)
	if authRec.Code != http.StatusFound {
		t.Fatalf("authorize: status = %d body = %q", authRec.Code, authRec.Body.String())
	}
	location, err := url.Parse(authRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("authorize: parse location: %v", err)
	}
	code := location.Query().Get("code")
	if code == "" {
		t.Fatal("authorize: missing code in redirect")
	}
	if location.Query().Get("state") != "state-xyz" {
		t.Fatalf("authorize: state = %q want state-xyz", location.Query().Get("state"))
	}

	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {"https://client.test/callback"},
		"code_verifier": {verifier},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	svc.HandleToken(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token: status = %d body = %q", tokenRec.Code, tokenRec.Body.String())
	}
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &tokenResp); err != nil {
		t.Fatalf("token: decode: %v", err)
	}
	if tokenResp.AccessToken == "" {
		t.Fatal("token: empty access_token")
	}
	if tokenResp.TokenType != "Bearer" {
		t.Fatalf("token: token_type = %q want Bearer", tokenResp.TokenType)
	}
	if tokenResp.RefreshToken == "" {
		t.Fatal("token: empty refresh_token")
	}
}

func TestPKCEFlowIsObservableInLogs(t *testing.T) {
	// #378/OAuth-observability follow-up: the user needs to reconstruct which
	// OAuth path a live client (Gemini, Le Chat, ...) actually took by
	// grepping server logs after a real connection attempt. Confirm the full
	// DCR + authorize + token sequence is logged, correlatable by client_id,
	// and never leaks the client secret, auth code, PKCE verifier, or issued
	// tokens into the log stream.
	logBuf := withDefaultLogger(t)
	svc, _ := newTestService(t)
	clientID := registerClient(t, svc, []string{"https://client.test/callback"})

	verifier := "test-verifier-test-verifier-test-verifier-test"
	challenge := oauth.CodeChallengeS256(verifier)

	authURL := "/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"https://client.test/callback"},
		"state":                 {"state-xyz"},
		"scope":                 {"content.read"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()
	authReq := httptest.NewRequest(http.MethodGet, authURL, nil)
	authReq.RemoteAddr = "127.0.0.1:9999"
	authRec := httptest.NewRecorder()
	svc.HandleAuthorize(authRec, authReq)
	location, _ := url.Parse(authRec.Header().Get("Location"))
	code := location.Query().Get("code")

	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {"https://client.test/callback"},
		"code_verifier": {verifier},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	svc.HandleToken(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token: status = %d body = %q", tokenRec.Code, tokenRec.Body.String())
	}
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &tokenResp); err != nil {
		t.Fatalf("token: decode: %v", err)
	}

	logs := logBuf.String()
	for _, want := range []string{
		`"msg":"oauth_register"`, `"outcome":"success"`,
		`"msg":"oauth_authorize"`, `"pkce_used":true`,
		`"msg":"oauth_token"`, `"grant_type":"authorization_code"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("oauth flow logs missing %q; logs = %s", want, logs)
		}
	}
	// All three events (register, authorize, token) must be correlatable by
	// client_id — register is the bridge line tying pre-registration
	// (which has no client_id yet) to the client_id used by every
	// subsequent call in this client's flow.
	if got := strings.Count(logs, `"client_id":"`+clientID+`"`); got < 3 {
		t.Fatalf("expected client_id %q to appear in register+authorize+token log lines, got %d occurrences; logs = %s", clientID, got, logs)
	}
	// Secrets must never reach the log stream.
	for _, secret := range []string{code, verifier, tokenResp.AccessToken, tokenResp.RefreshToken} {
		if secret == "" {
			t.Fatal("test setup produced an empty secret value, assertion would be vacuous")
		}
		if strings.Contains(logs, secret) {
			t.Fatalf("oauth flow logs leaked a secret value %q; logs = %s", secret, logs)
		}
	}
}

func TestAuthorizeRedirectMismatchIsObservableInLogs(t *testing.T) {
	// Redirect-URI mismatch is the most common real-world DCR failure and,
	// before this fix, left no diagnostic log line at all — only a generic
	// 400 in the request log. Confirm the failure is now attributable to a
	// client_id and an attempted host without echoing the raw rejected URI.
	logBuf := withDefaultLogger(t)
	svc, _ := newTestService(t)
	clientID := registerClient(t, svc, []string{"https://client.test/callback"})

	authURL := "/authorize?" + url.Values{
		"response_type": {"code"},
		"client_id":     {clientID},
		"redirect_uri":  {"https://attacker.test/callback"},
		"state":         {"state-xyz"},
	}.Encode()
	authReq := httptest.NewRequest(http.MethodGet, authURL, nil)
	authReq.RemoteAddr = "127.0.0.1:9999"
	authRec := httptest.NewRecorder()
	svc.HandleAuthorize(authRec, authReq)
	if authRec.Code != http.StatusBadRequest {
		t.Fatalf("authorize: status = %d, want 400", authRec.Code)
	}

	logs := logBuf.String()
	for _, want := range []string{
		`"msg":"oauth_authorize"`, `"outcome":"error"`,
		`"client_id":"` + clientID + `"`, `"attempted_redirect_uri_host":"attacker.test"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("redirect-mismatch logs missing %q; logs = %s", want, logs)
		}
	}
}

func TestRefreshTokenGrant(t *testing.T) {
	svc, _ := newTestService(t)
	clientID := registerClient(t, svc, []string{"https://client.test/callback"})

	verifier := "test-verifier-test-verifier-test-verifier-test"
	challenge := oauth.CodeChallengeS256(verifier)

	authURL := "/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"https://client.test/callback"},
		"state":                 {"state-refresh"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()
	authReq := httptest.NewRequest(http.MethodGet, authURL, nil)
	authReq.RemoteAddr = "127.0.0.1:9999"
	authRec := httptest.NewRecorder()
	svc.HandleAuthorize(authRec, authReq)
	if authRec.Code != http.StatusFound {
		t.Fatalf("authorize: status = %d body = %q", authRec.Code, authRec.Body.String())
	}
	location, err := url.Parse(authRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("authorize: parse location: %v", err)
	}
	code := location.Query().Get("code")
	if code == "" {
		t.Fatal("authorize: missing code in redirect")
	}

	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {"https://client.test/callback"},
		"code_verifier": {verifier},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	svc.HandleToken(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token: status = %d body = %q", tokenRec.Code, tokenRec.Body.String())
	}
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &tokenResp); err != nil {
		t.Fatalf("token: decode: %v", err)
	}
	if tokenResp.RefreshToken == "" {
		t.Fatal("token: empty refresh_token")
	}

	refreshForm := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {tokenResp.RefreshToken},
	}
	refreshReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(refreshForm.Encode()))
	refreshReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	refreshRec := httptest.NewRecorder()
	svc.HandleToken(refreshRec, refreshReq)
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh token: status = %d body = %q", refreshRec.Code, refreshRec.Body.String())
	}
	var refreshResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(refreshRec.Body.Bytes(), &refreshResp); err != nil {
		t.Fatalf("refresh token: decode: %v", err)
	}
	if refreshResp.AccessToken == "" {
		t.Fatal("refresh token: empty access_token")
	}
	if refreshResp.RefreshToken == "" {
		t.Fatal("refresh token: empty refresh_token in response")
	}
	if refreshResp.Scope != tokenResp.Scope {
		t.Fatalf("refresh token: scope = %q want %q", refreshResp.Scope, tokenResp.Scope)
	}
	if refreshResp.AccessToken == tokenResp.AccessToken {
		t.Fatal("refresh token: access_token should rotate")
	}

	reuseReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(refreshForm.Encode()))
	reuseReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reuseRec := httptest.NewRecorder()
	svc.HandleToken(reuseRec, reuseReq)
	if reuseRec.Code != http.StatusBadRequest {
		t.Fatalf("refresh token reuse: status = %d body = %q", reuseRec.Code, reuseRec.Body.String())
	}
	if !strings.Contains(reuseRec.Body.String(), "invalid_grant") {
		t.Fatalf("refresh token reuse body = %q", reuseRec.Body.String())
	}
}

func TestRefreshTokenGrantRejectsWrongClient(t *testing.T) {
	svc, _ := newTestService(t)
	clientA := registerClient(t, svc, []string{"https://client-a.test/callback"})
	clientB := registerClient(t, svc, []string{"https://client-b.test/callback"})

	verifier := "refresh-client-bound-refresh-client-bound-refresh"
	challenge := oauth.CodeChallengeS256(verifier)
	authURL := "/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {clientA},
		"redirect_uri":          {"https://client-a.test/callback"},
		"state":                 {"state-refresh-client"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()
	authReq := httptest.NewRequest(http.MethodGet, authURL, nil)
	authReq.RemoteAddr = "127.0.0.1:9999"
	authRec := httptest.NewRecorder()
	svc.HandleAuthorize(authRec, authReq)
	if authRec.Code != http.StatusFound {
		t.Fatalf("authorize: status = %d body = %q", authRec.Code, authRec.Body.String())
	}
	location, _ := url.Parse(authRec.Header().Get("Location"))
	code := location.Query().Get("code")
	if code == "" {
		t.Fatal("authorize: missing code in redirect")
	}

	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientA},
		"code":          {code},
		"redirect_uri":  {"https://client-a.test/callback"},
		"code_verifier": {verifier},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	svc.HandleToken(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token: status = %d body = %q", tokenRec.Code, tokenRec.Body.String())
	}
	var tokenResp struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &tokenResp); err != nil {
		t.Fatalf("token: decode: %v", err)
	}
	if tokenResp.RefreshToken == "" {
		t.Fatal("token: expected refresh_token in response")
	}

	refreshForm := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientB},
		"refresh_token": {tokenResp.RefreshToken},
	}
	refreshReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(refreshForm.Encode()))
	refreshReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	refreshRec := httptest.NewRecorder()
	svc.HandleToken(refreshRec, refreshReq)
	if refreshRec.Code != http.StatusBadRequest && refreshRec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh token wrong client: status = %d body = %q", refreshRec.Code, refreshRec.Body.String())
	}
	if !strings.Contains(refreshRec.Body.String(), "invalid_grant") && !strings.Contains(refreshRec.Body.String(), "invalid_client") {
		t.Fatalf("refresh token wrong client body = %q", refreshRec.Body.String())
	}
}

func TestPKCEFlowWrongVerifier(t *testing.T) {
	svc, _ := newTestService(t)
	clientID := registerClient(t, svc, []string{"https://client.test/callback"})

	verifier := "correct-verifier-correct-verifier-correct-veri"
	challenge := oauth.CodeChallengeS256(verifier)

	authURL := "/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"https://client.test/callback"},
		"state":                 {"s"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()
	authReq := httptest.NewRequest(http.MethodGet, authURL, nil)
	authReq.RemoteAddr = "127.0.0.1:9999"
	authRec := httptest.NewRecorder()
	svc.HandleAuthorize(authRec, authReq)
	if authRec.Code != http.StatusFound {
		t.Fatalf("authorize: status = %d", authRec.Code)
	}
	location, _ := url.Parse(authRec.Header().Get("Location"))
	code := location.Query().Get("code")

	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {"https://client.test/callback"},
		"code_verifier": {"wrong-verifier-wrong-verifier-wrong-verifier-wr"},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	svc.HandleToken(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusBadRequest {
		t.Fatalf("token: status = %d want 400", tokenRec.Code)
	}
}

func TestTokenEndpointSupportsBasicAuth(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "oauth-clients.yaml")
	if err := os.WriteFile(registryPath, []byte(`
clients:
  - id: basic-client
    secret: super-secret
    scopes: ["read"]
    redirect_uris:
      - https://client.test/callback
    enabled: true
`), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	svc := oauth.NewService(config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		Resource:              "https://mcp.test/mcp",
		DynamicClientEnabled:  true,
		TrustedAuthorizeCIDRs: []string{"127.0.0.1/32"},
		AuthCodeTTLSeconds:    300,
		AccessTokenTTLSeconds: 3600,
	}, storage.NewMemory())
	if err := svc.LoadClientRegistry(registryPath); err != nil {
		t.Fatalf("LoadClientRegistry() error = %v", err)
	}
	clientID := "basic-client"
	verifier := "verifier-verifier-verifier-verifier"

	authReq := httptest.NewRequest(http.MethodGet, "/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"https://client.test/callback"},
		"state":                 {"state-basic"},
		"code_challenge":        {oauth.CodeChallengeS256(verifier)},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	authReq.RemoteAddr = "127.0.0.1:9999"
	authRec := httptest.NewRecorder()
	svc.HandleAuthorize(authRec, authReq)
	if authRec.Code != http.StatusFound {
		t.Fatalf("authorize: status = %d body = %q", authRec.Code, authRec.Body.String())
	}
	location, _ := url.Parse(authRec.Header().Get("Location"))
	code := location.Query().Get("code")

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {"https://client.test/callback"},
		"code":          {code},
		"code_verifier": {verifier},
	}
	req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, "super-secret")
	rec := httptest.NewRecorder()
	svc.HandleToken(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("token basic auth: status = %d body = %q", rec.Code, rec.Body.String())
	}
}

func TestAuthorizeMissingStateReturnsError(t *testing.T) {
	svc, _ := newTestService(t)
	clientID := registerClient(t, svc, []string{"https://client.test/callback"})

	authReq := httptest.NewRequest(http.MethodGet, "/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"https://client.test/callback"},
		"code_challenge":        {oauth.CodeChallengeS256("verifier-verifier-verifier-verifier")},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	authReq.RemoteAddr = "127.0.0.1:9999"
	authRec := httptest.NewRecorder()
	svc.HandleAuthorize(authRec, authReq)
	if authRec.Code != http.StatusFound {
		t.Fatalf("authorize missing state: status = %d body = %q", authRec.Code, authRec.Body.String())
	}
	location, err := url.Parse(authRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	if got := location.Query().Get("error"); got != "invalid_request" {
		t.Fatalf("authorize missing state error = %q", got)
	}
}

func TestAuthorizeRequiresPKCE(t *testing.T) {
	svc, _ := newTestService(t)
	svc2 := oauth.NewService(config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		Resource:              "https://mcp.test/mcp",
		DynamicClientEnabled:  true,
		RequirePKCE:           true,
		TrustedAuthorizeCIDRs: []string{"127.0.0.1/32"},
		AuthCodeTTLSeconds:    300,
		AccessTokenTTLSeconds: 3600,
	}, storage.NewMemory())
	clientID := registerClient(t, svc2, []string{"https://client.test/callback"})
	_ = svc

	authReq := httptest.NewRequest(http.MethodGet, "/authorize?"+url.Values{
		"response_type": {"code"},
		"client_id":     {clientID},
		"redirect_uri":  {"https://client.test/callback"},
		"state":         {"state-pkce"},
	}.Encode(), nil)
	authReq.RemoteAddr = "127.0.0.1:9999"
	authRec := httptest.NewRecorder()
	svc2.HandleAuthorize(authRec, authReq)
	if authRec.Code != http.StatusFound {
		t.Fatalf("authorize require pkce: status = %d body = %q", authRec.Code, authRec.Body.String())
	}
	location, err := url.Parse(authRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	if got := location.Query().Get("error"); got != "invalid_request" {
		t.Fatalf("authorize require pkce error = %q", got)
	}
}

func TestTokenEndpointRejectsUnsupportedGrantType(t *testing.T) {
	svc, _ := newTestService(t)
	req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(url.Values{
		"grant_type": {"client_credentials"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	svc.HandleToken(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("token unsupported grant: status = %d body = %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unsupported_grant_type") {
		t.Fatalf("token unsupported grant body = %q", rec.Body.String())
	}
}

func TestAgentIdentityAnonymous(t *testing.T) {
	svc, _ := newTestService(t)
	body := []byte(`{"type":"anonymous"}`)
	req := httptest.NewRequest(http.MethodPost, "/agent/identity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	svc.HandleAgentIdentity(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
	}
	var resp oauth.AgentIdentityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.IdentityAssertion == "" {
		t.Fatal("missing identity_assertion")
	}
	if resp.RegistrationID == "" {
		t.Fatal("missing registration_id")
	}
	if resp.RegistrationType != "anonymous" {
		t.Fatalf("registration_type = %q want anonymous", resp.RegistrationType)
	}
	if resp.ClaimToken == "" {
		t.Fatal("missing claim_token")
	}
}

func TestAgentIdentityUnknownType(t *testing.T) {
	svc, _ := newTestService(t)
	body := []byte(`{"type":"email"}`)
	req := httptest.NewRequest(http.MethodPost, "/agent/identity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	svc.HandleAgentIdentity(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d want 400", rec.Code)
	}
}

// TestAgentTokenExchangeRequiresClaim verifies that an unclaimed assertion
// cannot be exchanged for a privileged token (issue #27).
func TestAgentTokenExchangeRequiresClaim(t *testing.T) {
	svc, _ := newTestService(t)

	identityBody := []byte(`{"type":"anonymous"}`)
	identityReq := httptest.NewRequest(http.MethodPost, "/agent/identity", bytes.NewReader(identityBody))
	identityReq.Header.Set("Content-Type", "application/json")
	identityRec := httptest.NewRecorder()
	svc.HandleAgentIdentity(identityRec, identityReq)
	if identityRec.Code != http.StatusOK {
		t.Fatalf("identity: status = %d", identityRec.Code)
	}
	var identity oauth.AgentIdentityResponse
	if err := json.Unmarshal(identityRec.Body.Bytes(), &identity); err != nil {
		t.Fatalf("identity: decode: %v", err)
	}

	tokenForm := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {identity.IdentityAssertion},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	svc.HandleToken(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusBadRequest {
		t.Fatalf("unclaimed assertion must be rejected: status = %d body = %q", tokenRec.Code, tokenRec.Body.String())
	}
	var errBody struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(tokenRec.Body.Bytes(), &errBody)
	if errBody.Error != "invalid_grant" {
		t.Fatalf("expected invalid_grant error, got: %q", errBody.Error)
	}
}

func TestAgentTokenExchangeAllowsReaderSelfRegistrationWhenEnabled(t *testing.T) {
	svc, store := newTestServiceWithConfig(t, config.OAuthConfig{
		Enabled:                     true,
		Issuer:                      "https://mcp.test",
		Resource:                    "https://mcp.test/mcp",
		DynamicClientEnabled:        true,
		TrustedAuthorizeCIDRs:       []string{"127.0.0.1/32", "::1/128"},
		AuthCodeTTLSeconds:          300,
		AccessTokenTTLSeconds:       3600,
		AllowReaderSelfRegistration: true,
	})

	identityBody := []byte(`{"type":"anonymous"}`)
	identityReq := httptest.NewRequest(http.MethodPost, "/agent/identity", bytes.NewReader(identityBody))
	identityReq.Header.Set("Content-Type", "application/json")
	identityRec := httptest.NewRecorder()
	svc.HandleAgentIdentity(identityRec, identityReq)
	if identityRec.Code != http.StatusOK {
		t.Fatalf("identity: status = %d body = %q", identityRec.Code, identityRec.Body.String())
	}
	var identity oauth.AgentIdentityResponse
	if err := json.Unmarshal(identityRec.Body.Bytes(), &identity); err != nil {
		t.Fatalf("identity: decode: %v", err)
	}
	if len(identity.PreClaimScopes) != 1 || identity.PreClaimScopes[0] != "read" {
		t.Fatalf("identity pre_claim_scopes = %#v want [read]", identity.PreClaimScopes)
	}
	if len(identity.PostClaimScopes) != 1 || identity.PostClaimScopes[0] != "read" {
		t.Fatalf("identity post_claim_scopes = %#v want [read]", identity.PostClaimScopes)
	}
	if identity.ClaimToken != "" || identity.ClaimURL != "" || identity.Claim != nil {
		t.Fatalf("reader self-registration must not advertise claim flow: %#v", identity)
	}

	tokenForm := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {identity.IdentityAssertion},
		"scope":      {"site.admin"},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	svc.HandleToken(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("reader self-registration token exchange: status = %d body = %q", tokenRec.Code, tokenRec.Body.String())
	}
	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &tokenResp); err != nil {
		t.Fatalf("token: decode: %v", err)
	}
	if tokenResp.Scope != "read" {
		t.Fatalf("token scope = %q want read", tokenResp.Scope)
	}

	scope, legacy, ok := svc.ValidateBearerDetails(tokenResp.AccessToken)
	if !ok {
		t.Fatal("reader token must validate")
	}
	if scope != "read" || legacy {
		t.Fatalf("ValidateBearerDetails returned scope=%q legacy=%v; want read false", scope, legacy)
	}

	storedScope, ok := store.ValidateAccessToken(oauth.HashToken(tokenResp.AccessToken))
	if !ok {
		t.Fatal("reader token missing from store")
	}
	if storedScope != "read" {
		t.Fatalf("stored scope = %q want read", storedScope)
	}
}

// TestAgentTokenExchange kept for backward-compat reference; now documents that
// unclaimed assertions are rejected. The old behaviour (immediate exchange) was
// removed by the #27 fix.
func TestAgentTokenExchange(t *testing.T) {
	TestAgentTokenExchangeRequiresClaim(t)
}

func TestAgentTokenExchangeInvalidAssertion(t *testing.T) {
	svc, _ := newTestService(t)
	tokenForm := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {"invalid-assertion"},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	svc.HandleToken(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d want 400", tokenRec.Code)
	}
}

func TestAgentClaim(t *testing.T) {
	svc, _ := newTestService(t)

	identityBody := []byte(`{"type":"anonymous"}`)
	identityReq := httptest.NewRequest(http.MethodPost, "/agent/identity", bytes.NewReader(identityBody))
	identityReq.Header.Set("Content-Type", "application/json")
	identityRec := httptest.NewRecorder()
	svc.HandleAgentIdentity(identityRec, identityReq)
	var identity oauth.AgentIdentityResponse
	_ = json.Unmarshal(identityRec.Body.Bytes(), &identity)

	claimBody, _ := json.Marshal(map[string]string{"claim_token": identity.ClaimToken})
	claimReq := httptest.NewRequest(http.MethodPost, "/agent/identity/claim", bytes.NewReader(claimBody))
	claimReq.Header.Set("Content-Type", "application/json")
	claimRec := httptest.NewRecorder()
	svc.HandleAgentClaim(claimRec, claimReq)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("claim: status = %d body = %q", claimRec.Code, claimRec.Body.String())
	}
	var claimResp oauth.AgentClaimResponse
	if err := json.Unmarshal(claimRec.Body.Bytes(), &claimResp); err != nil {
		t.Fatalf("claim: decode: %v", err)
	}
	if claimResp.ClaimAttemptID == "" {
		t.Fatal("claim: missing claim_attempt_id")
	}
	if claimResp.Status != "initiated" {
		t.Fatalf("claim: status = %q want initiated", claimResp.Status)
	}
}

func TestAgentClaimInvalidToken(t *testing.T) {
	svc, _ := newTestService(t)
	body, _ := json.Marshal(map[string]string{"claim_token": "invalid-token"})
	req := httptest.NewRequest(http.MethodPost, "/agent/identity/claim", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	svc.HandleAgentClaim(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d want 400", rec.Code)
	}
}

func TestAgentEvent(t *testing.T) {
	svc, _ := newTestService(t)
	body := []byte(`{"event":"test"}`)
	req := httptest.NewRequest(http.MethodPost, "/agent/event/notify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	svc.HandleAgentEvent(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
}

func TestBearerValidation(t *testing.T) {
	svc, store := newTestService(t)

	if _, ok := svc.ValidateBearer("nonexistent"); ok {
		t.Fatal("unknown token must not validate")
	}

	_ = store.AddAccessToken(oauth.HashToken("validtoken"), "mcp", time.Now().Add(time.Hour))
	scope, ok := svc.ValidateBearer("validtoken")
	if !ok {
		t.Fatal("valid token must validate")
	}
	if scope != "read" {
		t.Fatalf("scope = %q want read", scope)
	}
	scope, legacy, ok := svc.ValidateBearerDetails("validtoken")
	if !ok {
		t.Fatal("valid token must validate via ValidateBearerDetails")
	}
	if scope != "read" || !legacy {
		t.Fatalf("ValidateBearerDetails returned scope=%q legacy=%v; want read true", scope, legacy)
	}

	_ = store.AddAccessToken(oauth.HashToken("expiredtoken"), "mcp", time.Now().Add(-time.Second))
	if _, ok := svc.ValidateBearer("expiredtoken"); ok {
		t.Fatal("expired token must not validate")
	}
}

// TestBearerValidationViaTokenEndpoint verifies bearer validation using a token
// obtained via the authorization_code flow (agent assertions require claim
// verification per issue #27 and can no longer issue tokens directly).
func TestBearerValidationViaTokenEndpoint(t *testing.T) {
	svc, _ := newTestService(t)
	clientID := registerClient(t, svc, []string{"https://client.test/callback"})

	authURL := "/authorize?" + url.Values{
		"response_type": {"code"},
		"client_id":     {clientID},
		"redirect_uri":  {"https://client.test/callback"},
		"state":         {"s"},
	}.Encode()
	authReq := httptest.NewRequest(http.MethodGet, authURL, nil)
	authReq.RemoteAddr = "127.0.0.1:9999"
	authRec := httptest.NewRecorder()
	svc.HandleAuthorize(authRec, authReq)
	location, _ := url.Parse(authRec.Header().Get("Location"))
	code := location.Query().Get("code")

	tokenForm := url.Values{
		"grant_type":   {"authorization_code"},
		"client_id":    {clientID},
		"code":         {code},
		"redirect_uri": {"https://client.test/callback"},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	svc.HandleToken(tokenRec, tokenReq)
	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(tokenRec.Body.Bytes(), &tokenResp)

	scope, ok := svc.ValidateBearer(tokenResp.AccessToken)
	if !ok {
		t.Fatal("freshly issued token must validate")
	}
	// DCR clients always get "read", never more, per #497.
	if scope != "read" {
		t.Fatalf("scope = %q want \"read\" (DCR redirect URI never grants more than read)", scope)
	}

	if _, ok := svc.ValidateBearer("completely-wrong-token"); ok {
		t.Fatal("wrong token must not validate")
	}
}

func TestAuthorizeUntrustedSource(t *testing.T) {
	svc, _ := newTestService(t, "10.0.0.1/32")
	clientID := registerClient(t, svc, []string{"https://client.test/callback"})

	authURL := "/authorize?" + url.Values{
		"response_type": {"code"},
		"client_id":     {clientID},
		"redirect_uri":  {"https://client.test/callback"},
		"state":         {"s"},
	}.Encode()
	authReq := httptest.NewRequest(http.MethodGet, authURL, nil)
	authReq.RemoteAddr = "127.0.0.1:9999"
	authRec := httptest.NewRecorder()
	svc.HandleAuthorize(authRec, authReq)
	if authRec.Code == http.StatusFound {
		location, _ := url.Parse(authRec.Header().Get("Location"))
		if errParam := location.Query().Get("error"); errParam != "access_denied" {
			t.Fatalf("expected access_denied error, got location = %s", authRec.Header().Get("Location"))
		}
	} else if authRec.Code != http.StatusForbidden {
		t.Fatalf("status = %d want 302 with error=access_denied or 403", authRec.Code)
	}
}

func TestAuthorizeRedirectPreservesExistingQuery(t *testing.T) {
	svc, _ := newTestService(t)
	redirectURI := "https://client.test/callback?existing=1"
	clientID := registerClient(t, svc, []string{redirectURI})

	authURL := "/authorize?" + url.Values{
		"response_type": {"code"},
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"state":         {"state-query"},
	}.Encode()
	authReq := httptest.NewRequest(http.MethodGet, authURL, nil)
	authReq.RemoteAddr = "127.0.0.1:9999"
	authRec := httptest.NewRecorder()
	svc.HandleAuthorize(authRec, authReq)

	if authRec.Code != http.StatusFound {
		t.Fatalf("authorize status = %d body = %q", authRec.Code, authRec.Body.String())
	}
	location, err := url.Parse(authRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	query := location.Query()
	if got := query.Get("existing"); got != "1" {
		t.Fatalf("existing query = %q want 1; location = %s", got, authRec.Header().Get("Location"))
	}
	if query.Get("code") == "" {
		t.Fatalf("redirect missing code; location = %s", authRec.Header().Get("Location"))
	}
	if got := query.Get("state"); got != "state-query" {
		t.Fatalf("state = %q want state-query", got)
	}
}
