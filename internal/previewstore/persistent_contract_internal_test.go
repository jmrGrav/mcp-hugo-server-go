package previewstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
)

func openPersistentStore(t *testing.T) (*Store, *db.DB, string) {
	t.Helper()
	stateDB, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "previews")
	store, _, err := NewPersistent(stateDB, root)
	if err != nil {
		stateDB.Close()
		t.Fatal(err)
	}
	return store, stateDB, root
}

func TestPersistentStoreDirectoryAllocationStaysInsideManagedRoot(t *testing.T) {
	store, stateDB, root := openPersistentStore(t)
	defer stateDB.Close()

	dir, err := store.NewDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dir) != root || !safeDirName(filepath.Base(dir)) {
		t.Fatalf("NewDir() = %q, want safe child of %q", dir, root)
	}
	if got := store.ManagedRoot(); got != root {
		t.Fatalf("ManagedRoot() = %q, want %q", got, root)
	}

	volatile := New()
	if got := volatile.ManagedRoot(); got != os.TempDir() {
		t.Fatalf("volatile ManagedRoot() = %q, want %q", got, os.TempDir())
	}
	dir, err = volatile.NewDir()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if filepath.Dir(dir) != os.TempDir() {
		t.Fatalf("volatile NewDir() = %q, want child of %q", dir, os.TempDir())
	}
}

func TestPersistentStoreRejectsUnsafeLeaseMetadata(t *testing.T) {
	store, stateDB, root := openPersistentStore(t)
	defer stateDB.Close()
	now := time.Now().UTC()

	tests := []struct {
		name  string
		id    string
		entry *Entry
	}{
		{name: "nil entry", id: "id", entry: nil},
		{name: "empty id", id: "", entry: &Entry{Dir: filepath.Join(root, "mcp-preview-valid")}},
		{name: "outside root", id: "id", entry: &Entry{Dir: filepath.Join(t.TempDir(), "mcp-preview-outside")}},
		{name: "unsafe basename", id: "id", entry: &Entry{Dir: filepath.Join(root, "other")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.entry != nil {
				tt.entry.CreatedAt = now
				tt.entry.ExpiresAt = now.Add(time.Hour)
			}
			if err := store.Put(tt.id, tt.entry); err == nil {
				t.Fatal("Put() succeeded for unsafe persistent lease metadata")
			}
		})
	}
}

func TestPersistentStoreSurfacesDatabaseOutageWithoutDroppingLease(t *testing.T) {
	store, stateDB, root := openPersistentStore(t)
	dir := filepath.Join(root, "mcp-preview-kept")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	entry := &Entry{
		Dir: dir, Token: "token", Owner: "owner", BuildStatus: "passed",
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour).UTC(),
	}
	if err := store.Put("kept", entry); err != nil {
		t.Fatal(err)
	}
	if err := stateDB.Close(); err != nil {
		t.Fatal(err)
	}

	if revoked, err := store.RevokeOwnedPersistent("kept", "owner"); err == nil || revoked {
		t.Fatalf("RevokeOwnedPersistent() = (%v, %v), want observable DB error", revoked, err)
	}
	if got, status := store.Lookup("kept"); status != LookupActive || got != entry {
		t.Fatalf("failed revoke dropped live lease: (%#v, %q)", got, status)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("failed revoke removed preview directory: %v", err)
	}
	if count, err := store.RevokeAllOwnedPersistent("owner"); err == nil || count != 0 {
		t.Fatalf("RevokeAllOwnedPersistent() = (%d, %v), want observable DB error", count, err)
	}
	if err := store.SweepPersistent(); err != nil {
		t.Fatalf("SweepPersistent() with no expired entries = %v", err)
	}

	expiredDir := filepath.Join(root, "mcp-preview-expired-db")
	if err := os.MkdirAll(expiredDir, 0o700); err != nil {
		t.Fatal(err)
	}
	store.entries["expired"] = &Entry{Dir: expiredDir, Owner: "owner", ExpiresAt: time.Now().Add(-time.Minute)}
	if err := store.SweepPersistent(); err == nil || !strings.Contains(err.Error(), "expire") {
		t.Fatalf("SweepPersistent() error = %v, want expiry persistence failure", err)
	}
	if _, ok := store.entries["expired"]; !ok {
		t.Fatal("failed persistent expiry removed in-memory lease")
	}
}

func TestNewPersistentValidatesRootAndNilDatabaseFallback(t *testing.T) {
	store, report, err := NewPersistent(nil, "relative")
	if err != nil || store == nil || report != (ReconcileReport{}) {
		t.Fatalf("NewPersistent(nil) = (%#v, %#v, %v)", store, report, err)
	}
	stateDB, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer stateDB.Close()
	if _, _, err := NewPersistent(stateDB, "relative"); err == nil {
		t.Fatal("NewPersistent() accepted a relative managed root")
	}
}

func TestPersistentReconciliationSurfacesDatabaseAndRootFailures(t *testing.T) {
	closedDB, err := db.Open(filepath.Join(t.TempDir(), "closed.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := closedDB.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewPersistent(closedDB, t.TempDir()); err == nil || !strings.Contains(err.Error(), "list leases") {
		t.Fatalf("NewPersistent(closed DB) error = %v", err)
	}

	stateDB, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer stateDB.Close()
	root := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(root, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewPersistent(stateDB, root); err == nil || !strings.Contains(err.Error(), "scan root") {
		t.Fatalf("NewPersistent(file root) error = %v", err)
	}
}

func TestExpiredLeaseCleanupFailureRemainsRecoverable(t *testing.T) {
	store, stateDB, root := openPersistentStore(t)
	dir := filepath.Join(root, "mcp-preview-expired-kept")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	entry := &Entry{
		Dir: dir, Token: "token", SessionToken: "session", Owner: "owner",
		CreatedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(-time.Minute),
	}
	if err := store.Put("expired", entry); err != nil {
		t.Fatal(err)
	}
	if err := stateDB.Close(); err != nil {
		t.Fatal(err)
	}

	if got, status := store.Lookup("expired"); got != nil || status != LookupExpired {
		t.Fatalf("Lookup(expired) = (%#v, %q)", got, status)
	}
	if _, ok := store.GetByToken("expired", "token"); ok {
		t.Fatal("GetByToken() accepted an expired lease")
	}
	if _, ok := store.GetBySession("expired", "session"); ok {
		t.Fatal("GetBySession() accepted an expired lease")
	}
	if got := store.List(); len(got) != 0 {
		t.Fatalf("List() exposed expired lease: %#v", got)
	}
	if got := store.ListOwned("owner"); len(got) != 0 {
		t.Fatalf("ListOwned() exposed expired lease: %#v", got)
	}
	if _, ok := store.entries["expired"]; !ok {
		t.Fatal("failed durable cleanup discarded the recoverable lease")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("failed durable cleanup removed directory: %v", err)
	}
}

func TestCompatibilityRevokeWrappersFailClosedOnDatabaseOutage(t *testing.T) {
	store, stateDB, root := openPersistentStore(t)
	for _, id := range []string{"one", "two"} {
		dir := filepath.Join(root, "mcp-preview-"+id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := store.Put(id, &Entry{Dir: dir, Owner: "owner", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := stateDB.Close(); err != nil {
		t.Fatal(err)
	}
	if store.Revoke("one") || store.RevokeOwned("one", "owner") {
		t.Fatal("single revoke wrapper reported success during DB outage")
	}
	if store.RevokeAll() != 0 || store.RevokeAllOwned("owner") != 0 {
		t.Fatal("bulk revoke wrapper reported success during DB outage")
	}
	if len(store.entries) != 2 {
		t.Fatalf("failed wrapper revokes dropped leases: %#v", store.entries)
	}
}
