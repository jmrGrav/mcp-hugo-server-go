package oauth_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
)

// withDefaultAuditLogger temporarily replaces the process-wide default slog
// logger so audit.Info/Warn calls (which always log through slog.Default())
// can be captured and asserted on. Global process state — do not run in
// parallel with other tests that also swap the default logger.
func withDefaultAuditLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

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

func TestHandleAgentVerifyGETEscapesClaimToken(t *testing.T) {
	svc, _ := newTestService(t)
	rawClaimToken := `"><script>alert(1)</script>`

	req := httptest.NewRequest(http.MethodGet, "/agent/identity/verify?claim_token="+rawClaimToken, nil)
	rec := httptest.NewRecorder()
	svc.HandleAgentVerify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET verify: status = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, rawClaimToken) {
		t.Fatalf("verify page rendered raw claim_token, body = %q", body)
	}
	if strings.Contains(body, "<script>") {
		t.Fatalf("verify page contains executable script markup, body = %q", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Fatalf("verify page missing escaped claim_token, body = %q", body)
	}
}

func TestHandleAgentVerifyPOST(t *testing.T) {
	svc, store := newTestService(t)
	logBuf := withDefaultAuditLogger(t)
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

	// #371: a successful operator claim approval must be a distinguishable
	// audit milestone, and must never leak the raw admin bearer token.
	raw := logBuf.String()
	if !strings.Contains(raw, `"event_type":"operator_milestone"`) {
		t.Fatalf("missing operator_milestone audit event: %s", raw)
	}
	if !strings.Contains(raw, `"result":"claim_approved"`) {
		t.Fatalf("missing claim_approved result: %s", raw)
	}
	if strings.Contains(raw, adminRaw) {
		t.Fatalf("audit log must never contain the raw operator bearer token: %s", raw)
	}
}

// TestRegisterAgentAnonymousEmitsOperatorMilestone proves #371's requirement
// that reader self-registration and pending-claim registration are each a
// distinguishable operator_milestone audit event.
func TestRegisterAgentAnonymousEmitsOperatorMilestone(t *testing.T) {
	svc, _ := newTestService(t)
	logBuf := withDefaultAuditLogger(t)

	registerAgentAnonymous(t, svc)

	raw := logBuf.String()
	if !strings.Contains(raw, `"event_type":"operator_milestone"`) {
		t.Fatalf("missing operator_milestone audit event: %s", raw)
	}
	if !strings.Contains(raw, `"result":"pending_operator_claim"`) {
		t.Fatalf("missing pending_operator_claim result (default config requires operator approval): %s", raw)
	}
}

func TestRegisterAgentAnonymousReaderSelfRegistrationEmitsDistinctMilestone(t *testing.T) {
	cfg := config.OAuthConfig{
		Enabled:                     true,
		Issuer:                      "https://mcp.test",
		Resource:                    "https://mcp.test/mcp",
		AllowReaderSelfRegistration: true,
		AccessTokenTTLSeconds:       3600,
	}
	svc, _ := newTestServiceWithConfig(t, cfg)
	logBuf := withDefaultAuditLogger(t)

	registerAgentAnonymous(t, svc)

	raw := logBuf.String()
	if !strings.Contains(raw, `"result":"reader_self_registered"`) {
		t.Fatalf("missing reader_self_registered result: %s", raw)
	}
	if strings.Contains(raw, `"result":"pending_operator_claim"`) {
		t.Fatalf("reader self-registration must not also report pending_operator_claim: %s", raw)
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
