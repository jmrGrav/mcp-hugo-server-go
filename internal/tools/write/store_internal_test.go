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
	got, ok, err := restarted.get("bundle-plan-1", "caller-a")
	if err != nil || !ok || got.BundleRevision != entry.BundleRevision {
		t.Fatalf("restarted get = %+v, %v, %v; want persisted entry", got, ok, err)
	}
	if _, ok, err := restarted.get("bundle-plan-1", "caller-b"); err != nil || ok {
		t.Fatal("caller-b read caller-a bundle plan")
	}
}

func TestSnapshotStoreSurvivesReopenAndListsOnlyItsCaller(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	first, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store := newSnapshotStore(time.Hour, 8, first)
	store.put("/opaque/page.md", "sha256:before", "caller-a", "before bytes")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	restarted := newSnapshotStore(time.Hour, 8, second)
	if content, ok, err := restarted.get("/opaque/page.md", "sha256:before", "caller-a"); err != nil || !ok || content != "before bytes" {
		t.Fatalf("restarted snapshot = %q, %v, %v", content, ok, err)
	}
	if rows, err := restarted.list("/opaque/page.md", "caller-b"); err != nil || len(rows) != 0 {
		t.Fatalf("caller-b snapshots = %v, want none", rows)
	}
	if rows, err := restarted.list("/opaque/page.md", "caller-a"); err != nil || len(rows) != 1 || rows[0].Revision != "sha256:before" {
		t.Fatalf("caller-a snapshots = %+v", rows)
	}
}

func TestBundleSnapshotStoreSurvivesReopenWithAtomicPayloadAndCallerIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	first, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store := newBundleSnapshotStore(time.Hour, 8, first)
	files := map[string]string{"/opaque/index.fr.md": "FR before", "/opaque/index.en.md": "EN before"}
	if err := store.put("/opaque", "sha256:bundle-before", "caller-a", files); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	restarted := newBundleSnapshotStore(time.Hour, 8, second)
	got, ok, err := restarted.get("/opaque", "sha256:bundle-before", "caller-a")
	if err != nil || !ok || got["/opaque/index.fr.md"] != "FR before" || got["/opaque/index.en.md"] != "EN before" || len(got) != 2 {
		t.Fatalf("restarted bundle snapshot = %#v, %v, %v", got, ok, err)
	}
	if _, ok, err := restarted.get("/opaque", "sha256:bundle-before", "caller-b"); err != nil || ok {
		t.Fatal("caller-b read caller-a bundle snapshot")
	}
}

func TestPersistedSnapshotReadFailureIsNotReportedAsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	journal, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	content := newSnapshotStore(time.Hour, 8, journal)
	bundle := newBundleSnapshotStore(time.Hour, 8, journal)
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := content.get("/opaque/page.md", "sha256:before", "caller-a"); err == nil || found {
		t.Fatalf("content get after DB close = found %v, err %v; want persistence error", found, err)
	}
	if _, found, err := bundle.get("/opaque", "sha256:before", "caller-a"); err == nil || found {
		t.Fatalf("bundle get after DB close = found %v, err %v; want persistence error", found, err)
	}
	if _, err := content.list("/opaque/page.md", "caller-a"); err == nil {
		t.Fatal("list after DB close succeeded, want persistence error")
	}
}

func TestPersistedPlanReadFailureIsNotReportedAsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	journal, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	content := newPlanStore(time.Hour, 8, journal)
	bundle := newBundlePlanStore(time.Hour, 8, journal)
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := content.get("plan-id", "caller-a"); err == nil || found {
		t.Fatalf("content plan get after DB close = found %v, err %v; want persistence error", found, err)
	}
	if _, found, err := bundle.get("plan-id", "caller-a"); err == nil || found {
		t.Fatalf("bundle plan get after DB close = found %v, err %v; want persistence error", found, err)
	}
}

func TestBundlePlanStorePruneAndTrimLocked(t *testing.T) {
	now := time.Date(2026, 7, 25, 23, 40, 0, 0, time.UTC)
	store := &bundlePlanStore{
		ttl:        time.Minute,
		maxEntries: 2,
		entries: map[string]bundlePlanEntry{
			"expired": {CreatedAt: now.Add(-2 * time.Minute)},
			"oldest":  {CreatedAt: now.Add(-30 * time.Second)},
			"middle":  {CreatedAt: now.Add(-20 * time.Second)},
			"newest":  {CreatedAt: now.Add(-10 * time.Second)},
		},
	}

	store.pruneLocked(now)
	if _, ok := store.entries["expired"]; ok {
		t.Fatal("pruneLocked() kept expired bundle plan")
	}

	store.trimLocked()
	if len(store.entries) != 2 {
		t.Fatalf("trimLocked() kept %d entries, want 2", len(store.entries))
	}
	if _, ok := store.entries["oldest"]; ok {
		t.Fatal("trimLocked() kept oldest bundle plan beyond maxEntries")
	}
	if _, ok := store.entries["newest"]; !ok {
		t.Fatal("trimLocked() dropped newest bundle plan, want it retained")
	}
}

func TestBundleSnapshotStorePruneAndTrimLocked(t *testing.T) {
	now := time.Date(2026, 7, 25, 23, 40, 0, 0, time.UTC)
	store := &bundleSnapshotStore{
		ttl:        time.Minute,
		maxEntries: 2,
		entries: map[string]bundleSnapshot{
			"expired": {CreatedAt: now.Add(-2 * time.Minute)},
			"oldest":  {CreatedAt: now.Add(-30 * time.Second)},
			"middle":  {CreatedAt: now.Add(-20 * time.Second)},
			"newest":  {CreatedAt: now.Add(-10 * time.Second)},
		},
	}

	store.pruneLocked(now)
	if _, ok := store.entries["expired"]; ok {
		t.Fatal("pruneLocked() kept expired bundle snapshot")
	}

	store.trimLocked()
	if len(store.entries) != 2 {
		t.Fatalf("trimLocked() kept %d entries, want 2", len(store.entries))
	}
	if _, ok := store.entries["oldest"]; ok {
		t.Fatal("trimLocked() kept oldest bundle snapshot beyond maxEntries")
	}
	if _, ok := store.entries["newest"]; !ok {
		t.Fatal("trimLocked() dropped newest bundle snapshot, want it retained")
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
