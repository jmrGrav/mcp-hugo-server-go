package oauth

import (
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

type simpleStore struct {
	scope string
	ok    bool
}

func (s *simpleStore) AddAccessToken(token, scope, principal string, expiresAt time.Time) error {
	return nil
}
func (s *simpleStore) ValidateAccessToken(token string) (string, bool) {
	return s.scope, s.ok
}
func (s *simpleStore) AddRefreshToken(token, clientID, scope string, expiresAt time.Time) error {
	return nil
}
func (s *simpleStore) ValidateRefreshToken(token, clientID string) (string, bool) {
	return "", false
}
func (s *simpleStore) ExchangeRefreshToken(oldToken, clientID, newRefreshToken, newAccessToken string, accessExpiresAt, refreshExpiresAt time.Time) (string, bool, error) {
	return "", false, nil
}
func (s *simpleStore) PurgeExpiredTokens() error { return nil }
func (s *simpleStore) Close() error              { return nil }

func TestValidateBearerInfoWithDetailedStore(t *testing.T) {
	svc, store := newAgentTestService()
	future := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	if err := store.AddAccessToken(HashToken("token-detailed"), "reader", "reader-client", future); err != nil {
		t.Fatalf("AddAccessToken() error = %v", err)
	}

	scope, expiresAt, principal, legacy, ok := svc.ValidateBearerInfo("token-detailed")
	if !ok {
		t.Fatal("ValidateBearerInfo() = not ok, want ok")
	}
	if scope != "read" {
		t.Fatalf("ValidateBearerInfo().scope = %q, want read", scope)
	}
	if !legacy {
		t.Fatal("ValidateBearerInfo().legacy = false, want true for reader alias")
	}
	if !expiresAt.Equal(future) {
		t.Fatalf("ValidateBearerInfo().expiresAt = %v, want %v", expiresAt, future)
	}
	if principal != "reader-client" {
		t.Fatalf("ValidateBearerInfo().principal = %q, want reader-client", principal)
	}
}

func TestValidateBearerInfoWithoutDetailedStore(t *testing.T) {
	svc := NewService(newAgentTestServiceConfig(), &simpleStore{scope: "content.read", ok: true})
	scope, expiresAt, principal, legacy, ok := svc.ValidateBearerInfo("token-no-details")
	if ok || scope != "" || !expiresAt.IsZero() || principal != "" || legacy {
		t.Fatalf("ValidateBearerInfo() = (%q, %v, %q, %v, %v), want zero values and ok=false when store lacks details", scope, expiresAt, principal, legacy, ok)
	}
}

func TestValidateBearerInfoInvalidToken(t *testing.T) {
	svc, _ := newAgentTestService()
	scope, expiresAt, principal, legacy, ok := svc.ValidateBearerInfo("missing")
	if ok || scope != "" || !expiresAt.IsZero() || principal != "" || legacy {
		t.Fatalf("ValidateBearerInfo(missing) = (%q, %v, %q, %v, %v), want zero values and ok=false", scope, expiresAt, principal, legacy, ok)
	}
}

func TestValidateBearerInfoRejectsEmptyPrincipal(t *testing.T) {
	svc, store := newAgentTestService()
	future := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	if err := store.AddAccessToken(HashToken("token-empty-principal"), "write", "", future); err != nil {
		t.Fatalf("AddAccessToken() error = %v", err)
	}

	scope, expiresAt, principal, legacy, ok := svc.ValidateBearerInfo("token-empty-principal")
	if ok || scope != "" || !expiresAt.IsZero() || principal != "" || legacy {
		t.Fatalf("ValidateBearerInfo(empty principal) = (%q, %v, %q, %v, %v), want zero values and ok=false", scope, expiresAt, principal, legacy, ok)
	}
}

func newAgentTestServiceConfig() config.OAuthConfig {
	return config.OAuthConfig{
		Enabled:               true,
		Issuer:                "https://mcp.test",
		Resource:              "https://mcp.test/mcp",
		DynamicClientEnabled:  true,
		TrustedAuthorizeCIDRs: []string{"127.0.0.1/32"},
		AuthCodeTTLSeconds:    300,
		AccessTokenTTLSeconds: 3600,
	}
}
