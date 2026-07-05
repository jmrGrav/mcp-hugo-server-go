package oauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/storage"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
)

func newAgentTestService() (*Service, storage.Store) {
	store := storage.NewMemory()
	svc := NewService(config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		Resource:              "https://mcp.test/mcp",
		DynamicClientEnabled:  true,
		TrustedAuthorizeCIDRs: []string{"127.0.0.1/32"},
		AuthCodeTTLSeconds:    300,
		AccessTokenTTLSeconds: 3600,
	}, store)
	return svc, store
}

func TestAgentAuthHelperBranches(t *testing.T) {
	svc, store := newAgentTestService()
	if !tools.IsAdminScope("site.admin") || tools.IsAdminScope("content.read") {
		t.Fatal("tools.IsAdminScope() returned unexpected result")
	}

	resp, err := svc.registerAgentAnonymous()
	if err != nil {
		t.Fatalf("registerAgentAnonymous() error = %v", err)
	}
	if resp.RegistrationID == "" || resp.ClaimToken == "" {
		t.Fatalf("registerAgentAnonymous() = %#v", resp)
	}

	if _, err := svc.exchangeAgentAssertion("missing"); err == nil {
		t.Fatal("exchangeAgentAssertion should reject unknown assertion")
	}
	reg := svc.agentRegs[resp.IdentityAssertion]
	reg.Claimed = true
	reg.AssertionExpires = time.Now().Add(time.Hour)
	svc.agentRegs[resp.IdentityAssertion] = reg
	if _, err := svc.exchangeAgentAssertion(resp.IdentityAssertion); err != nil {
		t.Fatalf("exchangeAgentAssertion() error = %v", err)
	}

	if _, err := svc.initiateClaim("missing"); err == nil {
		t.Fatal("initiateClaim should reject unknown claim token")
	}
	if err := svc.verifyAgentClaim("missing"); err == nil {
		t.Fatal("verifyAgentClaim should reject unknown claim token")
	}
	_ = store
}

func TestHandleAgentIdentityAndClaimBranches(t *testing.T) {
	svc, _ := newAgentTestService()

	req := httptest.NewRequest(http.MethodGet, "/agent/identity", nil)
	rec := httptest.NewRecorder()
	svc.HandleAgentIdentity(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET identity status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/agent/identity", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	svc.HandleAgentIdentity(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid identity JSON status = %d", rec.Code)
	}

	body := `{"type":"anonymous"}`
	req = httptest.NewRequest(http.MethodPost, "/agent/identity", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	svc.HandleAgentIdentity(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous identity status = %d body = %q", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/agent/identity/claim", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	svc.HandleAgentClaim(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid claim JSON status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/agent/event/notify", strings.NewReader("{}"))
	rec = httptest.NewRecorder()
	svc.HandleAgentEvent(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("agent event status = %d", rec.Code)
	}
}
