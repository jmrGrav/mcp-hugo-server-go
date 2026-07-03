package oauth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/storage"
)

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
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Scope       string `json:"scope"`
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

func TestAgentTokenExchange(t *testing.T) {
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
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token: status = %d body = %q", tokenRec.Code, tokenRec.Body.String())
	}
	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
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
	if scope != "mcp" {
		t.Fatalf("scope = %q want mcp", scope)
	}

	_ = store.AddAccessToken(oauth.HashToken("expiredtoken"), "mcp", time.Now().Add(-time.Second))
	if _, ok := svc.ValidateBearer("expiredtoken"); ok {
		t.Fatal("expired token must not validate")
	}
}

func TestBearerValidationViaTokenEndpoint(t *testing.T) {
	svc, _ := newTestService(t)

	identityBody := []byte(`{"type":"anonymous"}`)
	identityReq := httptest.NewRequest(http.MethodPost, "/agent/identity", bytes.NewReader(identityBody))
	identityRec := httptest.NewRecorder()
	svc.HandleAgentIdentity(identityRec, identityReq)
	var identity oauth.AgentIdentityResponse
	_ = json.Unmarshal(identityRec.Body.Bytes(), &identity)

	tokenForm := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {identity.IdentityAssertion},
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
	if scope != "mcp" {
		t.Fatalf("scope = %q want mcp", scope)
	}

	if _, ok := svc.ValidateBearer("completely-wrong-token"); ok {
		t.Fatal("wrong token must not validate")
	}
}

func TestAuthorizationServerMetadata(t *testing.T) {
	svc, _ := newTestService(t)
	meta := svc.AuthorizationServerMetadata()
	if meta["issuer"] != "https://mcp.test" {
		t.Fatalf("issuer = %v", meta["issuer"])
	}
	if meta["token_endpoint"] != "https://mcp.test/token" {
		t.Fatalf("token_endpoint = %v", meta["token_endpoint"])
	}
	agentAuth, ok := meta["agent_auth"].(map[string]any)
	if !ok {
		t.Fatalf("agent_auth not a map: %T", meta["agent_auth"])
	}
	if agentAuth["identity_endpoint"] != "https://mcp.test/agent/identity" {
		t.Fatalf("identity_endpoint = %v", agentAuth["identity_endpoint"])
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
