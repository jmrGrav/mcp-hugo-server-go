package write

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func newTestSiteIndex(t *testing.T) *site.Index {
	t.Helper()
	idx, err := site.NewIndex(config.Default())
	if err != nil {
		t.Fatalf("site.NewIndex: %v", err)
	}
	return idx
}

func TestBundleHasPublicNilIndex(t *testing.T) {
	if bundleHasPublic(nil, "posts/example") {
		t.Fatal("bundleHasPublic(nil, ...) = true, want false")
	}
}

func TestBundleHasPublicMissingSlug(t *testing.T) {
	idx := newTestSiteIndex(t)
	if bundleHasPublic(idx, "posts/missing") {
		t.Fatal("bundleHasPublic() = true for a slug not in the index, want false")
	}
}

func TestBundleHasPublicKnownSlug(t *testing.T) {
	idx := newTestSiteIndex(t)
	idx.UpsertPage(site.Page{Slug: "posts/example", Title: "Example"})
	if !bundleHasPublic(idx, "posts/example") {
		t.Fatal("bundleHasPublic() = false for a slug present in the index, want true")
	}
}

func TestAcquireContentLockImmediateSuccess(t *testing.T) {
	if !acquireContentLock("t_test") {
		t.Fatal("acquireContentLock() = false on an unlocked mutex, want true")
	}
	releaseContentLock("t_test")
}

func TestAcquireContentLockWaitsForConcurrentHolderThenAcquires(t *testing.T) {
	if !acquireContentLock("t_test_holder") {
		t.Fatal("setup: could not acquire lock")
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		releaseContentLock("t_test_holder")
	}()
	start := time.Now()
	if !acquireContentLock("t_test_waiter") {
		t.Fatal("acquireContentLock() = false, want it to wait then succeed once the holder releases")
	}
	defer releaseContentLock("t_test_waiter")
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Fatalf("acquireContentLock() returned after %v, want it to have actually waited for the concurrent holder", elapsed)
	}
}

func TestDecodeAssetBase64RejectsEmpty(t *testing.T) {
	if _, err := decodeAssetBase64("   "); err == nil {
		t.Fatal("decodeAssetBase64() error = nil, want an error for empty content")
	}
}

func TestDecodeAssetBase64RejectsOversized(t *testing.T) {
	huge := strings.Repeat("A", maxAssetBytes*2+1)
	if _, err := decodeAssetBase64(huge); err == nil {
		t.Fatal("decodeAssetBase64() error = nil, want an error for an oversized encoded payload")
	}
}

func TestDecodeAssetBase64RejectsInvalidBase64(t *testing.T) {
	if _, err := decodeAssetBase64("not-valid-base64!!!"); err == nil {
		t.Fatal("decodeAssetBase64() error = nil, want an error for invalid base64")
	}
}

func TestDecodeAssetBase64DecodesValidPayload(t *testing.T) {
	want := []byte("hello asset bytes")
	got, err := decodeAssetBase64(base64.StdEncoding.EncodeToString(want))
	if err != nil {
		t.Fatalf("decodeAssetBase64() error = %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("decodeAssetBase64() = %q, want %q", got, want)
	}
}
