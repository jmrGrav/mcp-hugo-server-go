package storage

import (
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
