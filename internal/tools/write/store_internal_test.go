package write

import (
	"testing"
	"time"
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
