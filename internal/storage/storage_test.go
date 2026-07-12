package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMemoryStoreLifecycle(t *testing.T) {
	s := NewMemory()
	future := time.Now().Add(time.Hour)
	expired := time.Now().Add(-time.Hour)

	if err := s.AddAccessToken("tok1", "content.read", future); err != nil {
		t.Fatalf("AddAccessToken() error = %v", err)
	}
	if scope, ok := s.ValidateAccessToken("tok1"); !ok || scope != "content.read" {
		t.Fatalf("ValidateAccessToken() = %q, %v", scope, ok)
	}
	if err := s.AddAccessToken("tok2", "content.write", expired); err != nil {
		t.Fatalf("AddAccessToken(expired) error = %v", err)
	}
	if err := s.PurgeExpiredTokens(); err != nil {
		t.Fatalf("PurgeExpiredTokens() error = %v", err)
	}
	if _, ok := s.ValidateAccessToken("tok2"); ok {
		t.Fatal("expired token should have been purged")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestRefreshTokenLifecycleAllStores(t *testing.T) {
	t.Parallel()

	future := time.Now().Add(time.Hour)
	expired := time.Now().Add(-time.Hour)

	tests := []struct {
		name  string
		open  func(t *testing.T) Store
		close func(Store) error
	}{
		{
			name: "memory",
			open: func(t *testing.T) Store { return NewMemory() },
			close: func(s Store) error {
				return s.Close()
			},
		},
		{
			name: "json",
			open: func(t *testing.T) Store {
				s, err := NewJSON(filepath.Join(t.TempDir(), "tokens.json"))
				if err != nil {
					t.Fatalf("NewJSON() error = %v", err)
				}
				return s
			},
			close: func(s Store) error { return s.Close() },
		},
		{
			name: "sqlite",
			open: func(t *testing.T) Store {
				s, err := NewSQLite(filepath.Join(t.TempDir(), "tokens.sqlite"))
				if err != nil {
					t.Fatalf("NewSQLite() error = %v", err)
				}
				return s
			},
			close: func(s Store) error { return s.Close() },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.open(t)
			if err := s.AddRefreshToken("rtok1", "client-a", "content.read", future); err != nil {
				t.Fatalf("AddRefreshToken() error = %v", err)
			}
			if scope, ok := s.ValidateRefreshToken("rtok1", "client-a"); !ok || scope != "content.read" {
				t.Fatalf("ValidateRefreshToken() = %q, %v", scope, ok)
			}
			if _, ok := s.ValidateRefreshToken("rtok1", "client-b"); ok {
				t.Fatal("refresh token should be bound to its client_id")
			}
			scope, ok, err := s.ExchangeRefreshToken("rtok1", "client-a", "rtok1-next", "atok1-next", future, future)
			if err != nil {
				t.Fatalf("ExchangeRefreshToken() error = %v", err)
			}
			if !ok || scope != "content.read" {
				t.Fatalf("ExchangeRefreshToken() = %q, %v", scope, ok)
			}
			if _, ok := s.ValidateRefreshToken("rtok1", "client-a"); ok {
				t.Fatal("old refresh token should be invalid after exchange")
			}
			if scope, ok := s.ValidateRefreshToken("rtok1-next", "client-a"); !ok || scope != "content.read" {
				t.Fatalf("ValidateRefreshToken(next) = %q, %v", scope, ok)
			}
			if scope, ok := s.ValidateAccessToken("atok1-next"); !ok || scope != "content.read" {
				t.Fatalf("ValidateAccessToken(next) = %q, %v", scope, ok)
			}
			if err := s.AddRefreshToken("rtok2", "client-a", "content.write", expired); err != nil {
				t.Fatalf("AddRefreshToken(expired) error = %v", err)
			}
			if err := s.PurgeExpiredTokens(); err != nil {
				t.Fatalf("PurgeExpiredTokens() error = %v", err)
			}
			if _, ok := s.ValidateRefreshToken("rtok2", "client-a"); ok {
				t.Fatal("expired refresh token should have been purged")
			}
			if err := tc.close(s); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
		})
	}
}

func TestJSONStoreLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	s, err := NewJSON(path)
	if err != nil {
		t.Fatalf("NewJSON() error = %v", err)
	}
	future := time.Now().Add(time.Hour)
	if err := s.AddAccessToken("tok1", "site.admin", future); err != nil {
		t.Fatalf("AddAccessToken() error = %v", err)
	}
	if err := s.AddAccessToken("tok2", "site.admin", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("AddAccessToken(expired) error = %v", err)
	}
	if err := s.AddRefreshToken("rtok1", "client-a", "site.admin", future); err != nil {
		t.Fatalf("AddRefreshToken() error = %v", err)
	}
	if scope, ok := s.ValidateAccessToken("tok1"); !ok || scope != "site.admin" {
		t.Fatalf("ValidateAccessToken() = %q, %v", scope, ok)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := s.PurgeExpiredTokens(); err != nil {
		t.Fatalf("PurgeExpiredTokens() error = %v", err)
	}

	s2, err := NewJSON(path)
	if err != nil {
		t.Fatalf("NewJSON(reopen) error = %v", err)
	}
	if scope, ok := s2.ValidateAccessToken("tok1"); !ok || scope != "site.admin" {
		t.Fatalf("ValidateAccessToken(reopen) = %q, %v", scope, ok)
	}
	if scope, ok := s2.ValidateRefreshToken("rtok1", "client-a"); !ok || scope != "site.admin" {
		t.Fatalf("ValidateRefreshToken(reopen) = %q, %v", scope, ok)
	}
}

func TestJSONStoreLoadsLegacyFlatFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	legacy := map[string]jsonEntry{
		"tok1": {Scope: "content.read", ExpiresAt: time.Now().Add(time.Hour).Unix()},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	s, err := NewJSON(path)
	if err != nil {
		t.Fatalf("NewJSON() error = %v", err)
	}
	if scope, ok := s.ValidateAccessToken("tok1"); !ok || scope != "content.read" {
		t.Fatalf("ValidateAccessToken(legacy) = %q, %v", scope, ok)
	}
}

func TestJSONStoreLoadMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := NewJSON(path); err == nil {
		t.Fatal("expected NewJSON() to fail on malformed JSON")
	}
}

func TestJSONStoreSaveError(t *testing.T) {
	dir := t.TempDir()
	readonly := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(readonly, 0o555); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	defer func() { _ = os.Chmod(readonly, 0o755) }()

	s, err := NewJSON(filepath.Join(readonly, "tokens.json"))
	if err != nil {
		t.Fatalf("NewJSON() error = %v", err)
	}
	if err := s.AddAccessToken("tok", "content.read", time.Now().Add(time.Hour)); err == nil {
		t.Fatal("expected AddAccessToken() to fail when temp file cannot be written")
	}
}

func TestJSONStoreExchangeRefreshTokenSaveErrorKeepsOldTokenValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	sAny, err := NewJSON(path)
	if err != nil {
		t.Fatalf("NewJSON() error = %v", err)
	}
	s := sAny.(*jsonStore)
	future := time.Now().Add(time.Hour)
	if err := s.AddRefreshToken("rtok1", "client-a", "content.read", future); err != nil {
		t.Fatalf("AddRefreshToken() error = %v", err)
	}

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0o755) }()

	if _, _, err := s.ExchangeRefreshToken("rtok1", "client-a", "rtok2", "atok2", future, future); err == nil {
		t.Fatal("expected ExchangeRefreshToken() to fail when save cannot write")
	}
	if scope, ok := s.ValidateRefreshToken("rtok1", "client-a"); !ok || scope != "content.read" {
		t.Fatalf("ValidateRefreshToken(old) = %q, %v", scope, ok)
	}
	if _, ok := s.ValidateRefreshToken("rtok2", "client-a"); ok {
		t.Fatal("new refresh token should not be visible after failed exchange")
	}
	if _, ok := s.ValidateAccessToken("atok2"); ok {
		t.Fatal("new access token should not be visible after failed exchange")
	}
}

func TestSQLiteStoreLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.sqlite")
	s, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("NewSQLite() error = %v", err)
	}
	defer s.Close()
	future := time.Now().Add(time.Hour)
	if err := s.AddAccessToken("tok1", "site.admin", future); err != nil {
		t.Fatalf("AddAccessToken() error = %v", err)
	}
	if err := s.AddRefreshToken("rtok1", "client-a", "site.admin", future); err != nil {
		t.Fatalf("AddRefreshToken() error = %v", err)
	}
	if scope, ok := s.ValidateAccessToken("tok1"); !ok || scope != "site.admin" {
		t.Fatalf("ValidateAccessToken() = %q, %v", scope, ok)
	}
	if scope, ok := s.ValidateRefreshToken("rtok1", "client-a"); !ok || scope != "site.admin" {
		t.Fatalf("ValidateRefreshToken() = %q, %v", scope, ok)
	}
	if err := s.PurgeExpiredTokens(); err != nil {
		t.Fatalf("PurgeExpiredTokens() error = %v", err)
	}
}

func TestSQLiteStoreUpsertOAuthClient(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clients.sqlite")
	storeAny, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("NewSQLite() error = %v", err)
	}
	store := storeAny.(*sqliteStore)
	defer store.db.Close()

	if err := store.UpsertOAuthClient("chatgpt-admin", "hash", true, []string{"https://chatgpt.com/connector/oauth/*"}, []string{"content.read", "content.write", "site.admin"}); err != nil {
		t.Fatalf("UpsertOAuthClient() error = %v", err)
	}
	if err := store.UpsertOAuthClient("chatgpt-read", "hash2", false, nil, nil); err != nil {
		t.Fatalf("UpsertOAuthClient(default scope) error = %v", err)
	}

	db := store.db
	var secretHash, redirectJSON, scopesJSON, effectiveScope string
	var enabled int
	if err := db.QueryRow(`SELECT secret_hash, redirect_uris, scopes, effective_scope, enabled FROM oauth_clients WHERE client_id = ?`, "chatgpt-admin").Scan(&secretHash, &redirectJSON, &scopesJSON, &effectiveScope, &enabled); err != nil {
		t.Fatalf("QueryRow(admin) error = %v", err)
	}
	if secretHash != "hash" {
		t.Fatalf("secret_hash = %q want hash", secretHash)
	}
	if effectiveScope != "site.admin" {
		t.Fatalf("effective_scope = %q want site.admin", effectiveScope)
	}
	if enabled != 1 {
		t.Fatalf("enabled = %d want 1", enabled)
	}
	var redirects []string
	if err := json.Unmarshal([]byte(redirectJSON), &redirects); err != nil {
		t.Fatalf("redirect_uris JSON invalid: %v", err)
	}
	if len(redirects) != 1 || redirects[0] != "https://chatgpt.com/connector/oauth/*" {
		t.Fatalf("redirect_uris = %#v", redirects)
	}
	var scopes []string
	if err := json.Unmarshal([]byte(scopesJSON), &scopes); err != nil {
		t.Fatalf("scopes JSON invalid: %v", err)
	}
	if len(scopes) != 3 || scopes[0] != "content.read" || scopes[2] != "site.admin" {
		t.Fatalf("scopes = %#v", scopes)
	}

	if err := db.QueryRow(`SELECT effective_scope, enabled FROM oauth_clients WHERE client_id = ?`, "chatgpt-read").Scan(&effectiveScope, &enabled); err != nil {
		t.Fatalf("QueryRow(read) error = %v", err)
	}
	if effectiveScope != "content.read" {
		t.Fatalf("default effective_scope = %q want content.read", effectiveScope)
	}
	if enabled != 0 {
		t.Fatalf("enabled = %d want 0", enabled)
	}
}

func TestSQLiteNewInvalidPath(t *testing.T) {
	dir := t.TempDir()
	invalid := filepath.Join(dir, "existing-dir")
	if err := os.MkdirAll(invalid, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if _, err := NewSQLite(invalid); err == nil {
		t.Fatal("expected NewSQLite() to fail when path points to a directory")
	}
}

func TestBoolToInt(t *testing.T) {
	if got := boolToInt(true); got != 1 {
		t.Fatalf("boolToInt(true) = %d want 1", got)
	}
	if got := boolToInt(false); got != 0 {
		t.Fatalf("boolToInt(false) = %d want 0", got)
	}
}
