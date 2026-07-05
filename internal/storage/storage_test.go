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

func TestSQLiteStoreLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.sqlite")
	s, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("NewSQLite() error = %v", err)
	}
	defer s.Close()
	future := time.Now().Add(time.Hour)
	if err := s.AddAccessToken("tok1", "system.admin", future); err != nil {
		t.Fatalf("AddAccessToken() error = %v", err)
	}
	if scope, ok := s.ValidateAccessToken("tok1"); !ok || scope != "system.admin" {
		t.Fatalf("ValidateAccessToken() = %q, %v", scope, ok)
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

	if err := store.UpsertOAuthClient("chatgpt-admin", "hash", true, []string{"https://chatgpt.com/connector/oauth/*"}, []string{"content.read", "content.write", "system.admin"}); err != nil {
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
	if effectiveScope != "system.admin" {
		t.Fatalf("effective_scope = %q want system.admin", effectiveScope)
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
	if len(scopes) != 3 || scopes[0] != "content.read" || scopes[2] != "system.admin" {
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
