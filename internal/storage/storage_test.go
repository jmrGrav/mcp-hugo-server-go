package storage_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/storage"
)

func testStore(t *testing.T, s storage.Store) {
	t.Helper()
	defer s.Close()

	future := time.Now().Add(time.Hour)
	if err := s.AddAccessToken("tok-valid", "read write", future); err != nil {
		t.Fatalf("AddAccessToken: %v", err)
	}

	scope, ok := s.ValidateAccessToken("tok-valid")
	if !ok {
		t.Fatal("expected tok-valid to be valid")
	}
	if scope != "read write" {
		t.Fatalf("expected scope %q, got %q", "read write", scope)
	}

	past := time.Now().Add(-time.Second)
	if err := s.AddAccessToken("tok-expired", "admin", past); err != nil {
		t.Fatalf("AddAccessToken expired: %v", err)
	}

	_, ok = s.ValidateAccessToken("tok-expired")
	if ok {
		t.Fatal("expected tok-expired to be invalid")
	}

	// token expires exactly now — should be invalid
	boundaryPast := time.Now().Add(-1 * time.Millisecond)
	s.AddAccessToken("boundary-token", "test", boundaryPast)
	_, ok = s.ValidateAccessToken("boundary-token")
	if ok {
		t.Fatal("token expired at past should not be valid")
	}

	if err := s.PurgeExpiredTokens(); err != nil {
		t.Fatalf("PurgeExpiredTokens: %v", err)
	}

	_, ok = s.ValidateAccessToken("tok-unknown")
	if ok {
		t.Fatal("expected tok-unknown to be invalid")
	}

	scope, ok = s.ValidateAccessToken("tok-valid")
	if !ok {
		t.Fatal("tok-valid should still be valid after purge")
	}
	if scope != "read write" {
		t.Fatalf("scope after purge: got %q", scope)
	}
}

func TestMemoryStore(t *testing.T) {
	testStore(t, storage.NewMemory())
}

func TestSQLiteStore(t *testing.T) {
	s, err := storage.NewSQLite(filepath.Join(t.TempDir(), "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}
	testStore(t, s)
}

func TestJSONStore(t *testing.T) {
	s, err := storage.NewJSON(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	testStore(t, s)
}
