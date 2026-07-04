package oauth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
)

func registerAgentAnonymous(t *testing.T, svc *oauth.Service) *agentIdentityResp {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"type": "anonymous"})
	req := httptest.NewRequest(http.MethodPost, "/agent/identity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:9999"
	rec := httptest.NewRecorder()
	svc.HandleAgentIdentity(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("agent identity: status = %d body = %q", rec.Code, rec.Body.String())
	}
	var resp agentIdentityResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("agent identity: decode: %v", err)
	}
	return &resp
}

type agentIdentityResp struct {
	ClaimToken string `json:"claim_token"`
	Claim      struct {
		VerificationURI string `json:"verification_uri"`
	} `json:"claim"`
}

func TestHandleAgentVerifyGET(t *testing.T) {
	svc, _ := newTestService(t)
	resp := registerAgentAnonymous(t, svc)

	req := httptest.NewRequest(http.MethodGet, "/agent/identity/verify?claim_token="+resp.ClaimToken, nil)
	rec := httptest.NewRecorder()
	svc.HandleAgentVerify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET verify: status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, resp.ClaimToken) {
		t.Errorf("verify page missing claim_token in form, body = %q", body)
	}
}

func TestHandleAgentVerifyPOST(t *testing.T) {
	svc, store := newTestService(t)
	resp := registerAgentAnonymous(t, svc)

	// Operator must authenticate with a site.admin token.
	adminRaw := "admin-token-site-admin"
	_ = store.AddAccessToken(oauth.HashToken(adminRaw), "site.admin", time.Now().Add(time.Hour))

	form := "claim_token=" + resp.ClaimToken
	req := httptest.NewRequest(http.MethodPost, "/agent/identity/verify", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+adminRaw)
	rec := httptest.NewRecorder()
	svc.HandleAgentVerify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST verify: status = %d body = %q", rec.Code, rec.Body.String())
	}
}

func TestHandleAgentVerifyPOSTNoAuth(t *testing.T) {
	svc, _ := newTestService(t)
	resp := registerAgentAnonymous(t, svc)

	form := "claim_token=" + resp.ClaimToken
	req := httptest.NewRequest(http.MethodPost, "/agent/identity/verify", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	svc.HandleAgentVerify(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST verify unauthenticated: status = %d want 401", rec.Code)
	}
}

func TestHandleAgentVerifyPOSTInsufficientScope(t *testing.T) {
	svc, store := newTestService(t)
	resp := registerAgentAnonymous(t, svc)

	// content.read is not enough — must be site.admin or system.admin.
	lowRaw := "low-scope-token"
	_ = store.AddAccessToken(oauth.HashToken(lowRaw), "content.read", time.Now().Add(time.Hour))

	form := "claim_token=" + resp.ClaimToken
	req := httptest.NewRequest(http.MethodPost, "/agent/identity/verify", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+lowRaw)
	rec := httptest.NewRecorder()
	svc.HandleAgentVerify(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST verify insufficient scope: status = %d want 403", rec.Code)
	}
}

func TestHandleAgentVerifyPOSTInvalidClaimToken(t *testing.T) {
	svc, store := newTestService(t)

	adminRaw := "admin-token-for-bad-claim"
	_ = store.AddAccessToken(oauth.HashToken(adminRaw), "site.admin", time.Now().Add(time.Hour))

	form := "claim_token=invalid_token_xyz"
	req := httptest.NewRequest(http.MethodPost, "/agent/identity/verify", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+adminRaw)
	rec := httptest.NewRecorder()
	svc.HandleAgentVerify(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST verify bad claim token: status = %d want 400", rec.Code)
	}
}

func TestHandleAgentVerifyMethodNotAllowed(t *testing.T) {
	svc, _ := newTestService(t)

	req := httptest.NewRequest(http.MethodDelete, "/agent/identity/verify", nil)
	rec := httptest.NewRecorder()
	svc.HandleAgentVerify(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE verify: status = %d want 405", rec.Code)
	}
}

func TestVerificationURIInResponse(t *testing.T) {
	svc, _ := newTestService(t)
	resp := registerAgentAnonymous(t, svc)

	if resp.Claim.VerificationURI == "" {
		t.Error("verification_uri missing from agent identity response")
	}
	if !strings.Contains(resp.Claim.VerificationURI, "/agent/identity/verify") {
		t.Errorf("verification_uri = %q, want to contain /agent/identity/verify", resp.Claim.VerificationURI)
	}
}
