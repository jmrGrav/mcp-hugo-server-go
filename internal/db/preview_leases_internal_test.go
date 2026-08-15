package db

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openPreviewLeaseDB(t *testing.T) *DB {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func TestPreviewLeaseCRUDDefaultsUpdatesAndScopesDeletion(t *testing.T) {
	database := openPreviewLeaseDB(t)
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Microsecond)
	leases := []PreviewLease{
		{ID: "one", Owner: "owner-a", DirName: "mcp-preview-one", BuildStatus: "building", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		{ID: "two", Owner: "owner-b", DirName: "mcp-preview-two", BuildStatus: "passed", State: "active", CreatedAt: now.Add(time.Second), ExpiresAt: now.Add(time.Hour)},
		{ID: "three", Owner: "owner-a", DirName: "mcp-preview-three", BuildStatus: "passed", CreatedAt: now.Add(2 * time.Second), ExpiresAt: now.Add(time.Hour)},
	}
	for _, lease := range leases {
		if err := database.PutPreviewLease(lease); err != nil {
			t.Fatal(err)
		}
	}
	leases[0].BuildStatus = "passed"
	leases[0].State = "ready"
	if err := database.PutPreviewLease(leases[0]); err != nil {
		t.Fatal(err)
	}

	got, err := database.ListPreviewLeases()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != "one" || got[0].State != "ready" || got[0].BuildStatus != "passed" {
		t.Fatalf("ListPreviewLeases() = %#v", got)
	}
	if got[1].ID != "two" || got[2].ID != "three" {
		t.Fatalf("lease order = %#v, want creation order", got)
	}
	if !got[0].CreatedAt.Equal(now) || !got[0].ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("timestamps changed across persistence: %#v", got[0])
	}

	if err := database.DeletePreviewLeasesByOwner("owner-a"); err != nil {
		t.Fatal(err)
	}
	got, err = database.ListPreviewLeases()
	if err != nil || len(got) != 1 || got[0].ID != "two" {
		t.Fatalf("owner-scoped deletion left %#v, err=%v", got, err)
	}
	if err := database.DeletePreviewLease("missing"); err != nil {
		t.Fatalf("idempotent single delete: %v", err)
	}
	if err := database.DeletePreviewLeasesByOwner(""); err != nil {
		t.Fatal(err)
	}
	got, err = database.ListPreviewLeases()
	if err != nil || len(got) != 0 {
		t.Fatalf("global lease deletion left %#v, err=%v", got, err)
	}
}

func TestPutPreviewLeaseValidatesIdentityAndTimestamps(t *testing.T) {
	database := openPreviewLeaseDB(t)
	defer database.Close()
	now := time.Now().UTC()
	tests := []PreviewLease{
		{DirName: "mcp-preview-x", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		{ID: "id", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		{ID: "id", DirName: "mcp-preview-x", ExpiresAt: now.Add(time.Hour)},
		{ID: "id", DirName: "mcp-preview-x", CreatedAt: now},
	}
	for _, lease := range tests {
		if err := database.PutPreviewLease(lease); err == nil {
			t.Fatalf("PutPreviewLease(%#v) succeeded", lease)
		}
	}
}

func TestListPreviewLeasesSurfacesCorruptTimestamps(t *testing.T) {
	tests := []struct {
		name      string
		createdAt string
		expiresAt string
		want      string
	}{
		{name: "created", createdAt: "invalid", expiresAt: time.Now().UTC().Format(time.RFC3339Nano), want: "created_at"},
		{name: "expires", createdAt: time.Now().UTC().Format(time.RFC3339Nano), expiresAt: "invalid", want: "expires_at"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database := openPreviewLeaseDB(t)
			defer database.Close()
			_, err := database.db.Exec(`INSERT INTO preview_leases(preview_id,owner,dir_name,build_status,state,created_at,expires_at) VALUES(?,?,?,?,?,?,?)`,
				"corrupt", "owner", "mcp-preview-corrupt", "passed", "active", tt.createdAt, tt.expiresAt)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.ListPreviewLeases(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ListPreviewLeases() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestPreviewLeaseOperationsSurfaceClosedDatabase(t *testing.T) {
	database := openPreviewLeaseDB(t)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	lease := PreviewLease{ID: "id", DirName: "mcp-preview-id", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := database.PutPreviewLease(lease); err == nil {
		t.Fatal("PutPreviewLease() hid closed database")
	}
	if _, err := database.ListPreviewLeases(); err == nil {
		t.Fatal("ListPreviewLeases() hid closed database")
	}
	if err := database.DeletePreviewLease("id"); err == nil {
		t.Fatal("DeletePreviewLease() hid closed database")
	}
	if err := database.DeletePreviewLeasesByOwner("owner"); err == nil {
		t.Fatal("owner-scoped deletion hid closed database")
	}
	if err := database.DeletePreviewLeasesByOwner(""); err == nil {
		t.Fatal("global deletion hid closed database")
	}
}
