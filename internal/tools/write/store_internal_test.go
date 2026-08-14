package write

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
)

func TestPlanStorePruneAndTrimLocked(t *testing.T) {
	now := time.Date(2026, 7, 25, 23, 35, 0, 0, time.UTC)
	store := &planStore{
		ttl:        time.Minute,
		maxEntries: 2,
		entries: map[string]planEntry{
			"expired": {CreatedAt: now.Add(-2 * time.Minute)},
			"oldest":  {CreatedAt: now.Add(-30 * time.Second)},
			"middle":  {CreatedAt: now.Add(-20 * time.Second)},
			"newest":  {CreatedAt: now.Add(-10 * time.Second)},
		},
	}

	store.pruneLocked(now)
	if _, ok := store.entries["expired"]; ok {
		t.Fatal("pruneLocked() kept expired plan")
	}

	store.trimLocked()
	if len(store.entries) != 2 {
		t.Fatalf("trimLocked() kept %d entries, want 2", len(store.entries))
	}
	if _, ok := store.entries["oldest"]; ok {
		t.Fatal("trimLocked() kept oldest plan beyond maxEntries")
	}
	if _, ok := store.entries["middle"]; !ok {
		t.Fatal("trimLocked() dropped middle plan, want it retained")
	}
	if _, ok := store.entries["newest"]; !ok {
		t.Fatal("trimLocked() dropped newest plan, want it retained")
	}
}

func TestBundlePlanStoreSurvivesReopenWithCallerIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	first, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store := newBundlePlanStore(time.Hour, 8, first)
	entry := bundlePlanEntry{CallerKey: "caller-a", Slug: "posts/example", BundleDir: "/opaque", BundleRevision: "sha256:before", CreatedAt: time.Now().UTC()}
	if err := store.put("bundle-plan-1", entry); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	restarted := newBundlePlanStore(time.Hour, 8, second)
	got, ok := restarted.get("bundle-plan-1", "caller-a")
	if !ok || got.BundleRevision != entry.BundleRevision {
		t.Fatalf("restarted get = %+v, %v; want persisted entry", got, ok)
	}
	if _, ok := restarted.get("bundle-plan-1", "caller-b"); ok {
		t.Fatal("caller-b read caller-a bundle plan")
	}
}

func TestSnapshotStorePruneAndTrimLocked(t *testing.T) {
	now := time.Date(2026, 7, 25, 23, 40, 0, 0, time.UTC)
	store := &snapshotStore{
		ttl:        time.Minute,
		maxEntries: 2,
		entries: map[string]snapshotEntry{
			"expired": {CreatedAt: now.Add(-2 * time.Minute)},
			"oldest":  {CreatedAt: now.Add(-30 * time.Second)},
			"middle":  {CreatedAt: now.Add(-20 * time.Second)},
			"newest":  {CreatedAt: now.Add(-10 * time.Second)},
		},
	}

	store.pruneLocked(now)
	if _, ok := store.entries["expired"]; ok {
		t.Fatal("pruneLocked() kept expired snapshot")
	}

	store.trimLocked()
	if len(store.entries) != 2 {
		t.Fatalf("trimLocked() kept %d entries, want 2", len(store.entries))
	}
	if _, ok := store.entries["oldest"]; ok {
		t.Fatal("trimLocked() kept oldest snapshot beyond maxEntries")
	}
	if _, ok := store.entries["middle"]; !ok {
		t.Fatal("trimLocked() dropped middle snapshot, want it retained")
	}
	if _, ok := store.entries["newest"]; !ok {
		t.Fatal("trimLocked() dropped newest snapshot, want it retained")
	}
}
