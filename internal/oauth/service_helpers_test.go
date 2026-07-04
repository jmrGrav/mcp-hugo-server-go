package oauth

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/storage"
)

func TestServiceHelperValidationBranches(t *testing.T) {
	svc := NewService(config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		Resource:              "https://mcp.test/mcp",
		DynamicClientEnabled:  true,
		RequirePKCE:           true,
		TrustedAuthorizeCIDRs: []string{"127.0.0.1/32"},
		AuthCodeTTLSeconds:    1,
		AccessTokenTTLSeconds: 1,
	}, storage.NewMemory())

	if _, err := svc.registerClient(RegistrationRequest{}); err == nil {
		t.Fatal("registerClient should reject empty redirect URIs")
	}
	if _, err := svc.registerClient(RegistrationRequest{RedirectURIs: []string{"ftp://client.test/callback"}}); err == nil {
		t.Fatal("registerClient should reject invalid redirect URIs")
	}
	resp, err := svc.registerClient(RegistrationRequest{RedirectURIs: []string{"https://client.test/callback"}})
	if err != nil {
		t.Fatalf("registerClient valid request error = %v", err)
	}
	if resp.ClientID == "" {
		t.Fatal("registerClient returned empty client id")
	}

	if _, err := svc.validateClientRedirect("missing", "https://client.test/callback"); err == nil {
		t.Fatal("validateClientRedirect should reject unknown client")
	}
	if _, err := svc.validateClientRedirect(resp.ClientID, "https://other.test/callback"); err == nil {
		t.Fatal("validateClientRedirect should reject unknown redirect uri")
	}
	if _, err := svc.validateClientRedirect(resp.ClientID, "https://client.test/callback"); err != nil {
		t.Fatalf("validateClientRedirect valid redirect error = %v", err)
	}

	if _, err := svc.issueAuthCode("203.0.113.10", "code", resp.ClientID, "https://client.test/callback", "state", "", "", ""); err == nil {
		t.Fatal("issueAuthCode should reject untrusted source")
	}
	if _, err := svc.issueAuthCode("127.0.0.1", "token", resp.ClientID, "https://client.test/callback", "state", "", "", ""); err == nil {
		t.Fatal("issueAuthCode should reject unsupported response type")
	}
	if _, err := svc.issueAuthCode("127.0.0.1", "code", resp.ClientID, "https://client.test/callback", "", "", "", ""); err == nil {
		t.Fatal("issueAuthCode should reject missing state")
	}
	if _, err := svc.issueAuthCode("127.0.0.1", "code", resp.ClientID, "https://client.test/callback", "state", "", "", ""); err == nil {
		t.Fatal("issueAuthCode should require PKCE when configured")
	}
	if _, err := svc.issueAuthCode("127.0.0.1", "code", resp.ClientID, "https://client.test/callback", "state", "", "abc", "plain"); err == nil {
		t.Fatal("issueAuthCode should reject unsupported PKCE method")
	}
	if _, err := svc.issueAuthCode("127.0.0.1", "code", resp.ClientID, "https://client.test/callback", "state", "", "abcd", "S256"); err == nil {
		t.Fatal("issueAuthCode should reject short code challenge")
	}
	code, err := svc.issueAuthCode("127.0.0.1", "code", resp.ClientID, "https://client.test/callback", "state", "", CodeChallengeS256("verifier-verifier-verifier-verifier"), "S256")
	if err != nil {
		t.Fatalf("issueAuthCode valid request error = %v", err)
	}
	if _, err := svc.exchangeToken("password", resp.ClientID, "", "https://client.test/callback", code, "verifier-verifier-verifier-verifier"); err == nil {
		t.Fatal("exchangeToken should reject unsupported grant type")
	}
	if _, err := svc.exchangeToken("authorization_code", "wrong", "", "https://client.test/callback", code, "verifier-verifier-verifier-verifier"); err == nil {
		t.Fatal("exchangeToken should reject wrong client")
	}
}
