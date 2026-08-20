package write

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
)

// consume() is the single-use enforcement point for both plan stores: a
// plan must be readable exactly once and never replayable by a different
// caller. Previously only get() (a non-consuming read) was exercised
// directly against a persistent store; consume()'s own delete-on-read and
// foreign-caller-key branches were untested.

func TestBundlePlanStoreConsumeIsSingleUseAcrossPersistence(t *testing.T) {
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

	got, ok, err := restarted.consume("bundle-plan-1", "caller-a")
	if err != nil || !ok || got.BundleRevision != entry.BundleRevision {
		t.Fatalf("consume() = %+v, %v, %v; want the persisted entry", got, ok, err)
	}
	if _, ok, err := restarted.consume("bundle-plan-1", "caller-a"); err != nil || ok {
		t.Fatalf("consume() replay = ok=%v err=%v, want single-use to reject a second consume", ok, err)
	}
}

func TestBundlePlanStoreConsumeRejectsForeignCallerWithoutDeleting(t *testing.T) {
	store := newBundlePlanStore(time.Hour, 8)
	entry := bundlePlanEntry{CallerKey: "caller-a", Slug: "posts/example", CreatedAt: time.Now().UTC()}
	if err := store.put("bundle-plan-1", entry); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.consume("bundle-plan-1", "caller-b"); err != nil || ok {
		t.Fatalf("consume() by a foreign caller = ok=%v err=%v, want rejected", ok, err)
	}
	// The foreign read must not have consumed the entry — the owning caller
	// can still retrieve it afterward.
	got, ok, err := store.consume("bundle-plan-1", "caller-a")
	if err != nil || !ok || got.Slug != entry.Slug {
		t.Fatalf("consume() by the owning caller after a foreign attempt = %+v, %v, %v; want it still present", got, ok, err)
	}
}

func TestBundlePlanStoreConsumeMissingIDReturnsNotFound(t *testing.T) {
	store := newBundlePlanStore(time.Hour, 8)
	if _, ok, err := store.consume("does-not-exist", "caller-a"); err != nil || ok {
		t.Fatalf("consume() of a missing id = ok=%v err=%v, want not found", ok, err)
	}
}

func TestPlanStoreConsumeIsSingleUseAcrossPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	first, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store := newPlanStore(time.Hour, 8, first)
	entry := planEntry{CallerKey: "caller-a", Slug: "posts/example", CreatedAt: time.Now().UTC()}
	if err := store.put("content-plan-1", entry); err != nil {
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
	restarted := newPlanStore(time.Hour, 8, second)

	got, ok, err := restarted.consume("content-plan-1", "caller-a")
	if err != nil || !ok || got.Slug != entry.Slug {
		t.Fatalf("consume() = %+v, %v, %v; want the persisted entry", got, ok, err)
	}
	if _, ok, err := restarted.consume("content-plan-1", "caller-a"); err != nil || ok {
		t.Fatalf("consume() replay = ok=%v err=%v, want single-use to reject a second consume", ok, err)
	}
}

func TestPlanStoreConsumeRejectsForeignCallerWithoutDeleting(t *testing.T) {
	store := newPlanStore(time.Hour, 8)
	entry := planEntry{CallerKey: "caller-a", Slug: "posts/example", CreatedAt: time.Now().UTC()}
	if err := store.put("content-plan-1", entry); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.consume("content-plan-1", "caller-b"); err != nil || ok {
		t.Fatalf("consume() by a foreign caller = ok=%v err=%v, want rejected", ok, err)
	}
	got, ok, err := store.consume("content-plan-1", "caller-a")
	if err != nil || !ok || got.Slug != entry.Slug {
		t.Fatalf("consume() by the owning caller after a foreign attempt = %+v, %v, %v; want it still present", got, ok, err)
	}
}
