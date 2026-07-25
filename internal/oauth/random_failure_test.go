package oauth

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestHandleRegisterFailsClosedWhenRandomSourceFails(t *testing.T) {
	svc, _ := newAgentTestService()
	withCryptoRandFailure(t)

	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewBufferString(`{"redirect_uris":["https://client.test/callback"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	svc.HandleRegister(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("HandleRegister status = %d body = %q, want 500 server_error", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "server_error") {
		t.Fatalf("HandleRegister body = %q, want server_error", rec.Body.String())
	}
}

func TestHandleAuthorizeFailsClosedWhenRandomSourceFails(t *testing.T) {
	svc, _ := newAgentTestService()
	clientID, err := svc.registerClient(RegistrationRequest{RedirectURIs: []string{"https://client.test/callback"}})
	if err != nil {
		t.Fatalf("registerClient: %v", err)
	}
	withCryptoRandFailure(t)

	req := httptest.NewRequest(http.MethodGet, "/authorize?"+url.Values{
		"response_type": {"code"},
		"client_id":     {clientID.ClientID},
		"redirect_uri":  {"https://client.test/callback"},
		"state":         {"authorize-rand-fail"},
	}.Encode(), nil)
	req.RemoteAddr = "127.0.0.1:9999"
	rec := httptest.NewRecorder()
	svc.HandleAuthorize(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("HandleAuthorize status = %d body = %q, want 500 server_error", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "server_error") {
		t.Fatalf("HandleAuthorize body = %q, want server_error", rec.Body.String())
	}
}

func TestHandleTokenAuthorizationCodeFailsClosedWhenRandomSourceFails(t *testing.T) {
	svc, _ := newAgentTestService()
	reg, err := svc.registerClient(RegistrationRequest{RedirectURIs: []string{"https://client.test/callback"}})
	if err != nil {
		t.Fatalf("registerClient: %v", err)
	}
	code, err := svc.issueAuthCode("127.0.0.1", "code", reg.ClientID, "https://client.test/callback", "token-rand-fail", "", "", "")
	if err != nil {
		t.Fatalf("issueAuthCode: %v", err)
	}
	withCryptoRandFailure(t)

	form := url.Values{
		"grant_type":   {"authorization_code"},
		"client_id":    {reg.ClientID},
		"code":         {code},
		"redirect_uri": {"https://client.test/callback"},
	}
	req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	svc.HandleToken(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("HandleToken status = %d body = %q, want 500 server_error", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "server_error") {
		t.Fatalf("HandleToken body = %q, want server_error", rec.Body.String())
	}
}

func TestHandleAgentIdentityFailsClosedWhenRandomSourceFails(t *testing.T) {
	svc, _ := newAgentTestService()
	withCryptoRandFailure(t)

	req := httptest.NewRequest(http.MethodPost, "/agent/identity", strings.NewReader(`{"type":"anonymous"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	svc.HandleAgentIdentity(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("HandleAgentIdentity status = %d body = %q, want 500 server_error", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "server_error") {
		t.Fatalf("HandleAgentIdentity body = %q, want server_error", rec.Body.String())
	}
}
