package write

import (
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
)

func TestBundleContentLockIsReentrantAndReleases(t *testing.T) {
	lock := &bundleContentLock{}
	if err := lock.acquire(); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := lock.acquire(); err != nil {
		t.Fatalf("reentrant acquire: %v", err)
	}
	lock.release()
	if !hugosite.ContentMu.TryLock() {
		t.Fatal("release left the global content lock held")
	}
	hugosite.ContentMu.Unlock()
}

func TestBundleContentLockTimesOut(t *testing.T) {
	hugosite.ContentMu.Lock()
	defer hugosite.ContentMu.Unlock()

	lock := &bundleContentLock{}
	err := lock.acquire()
	if err == nil || !strings.Contains(err.Error(), "build_in_progress") {
		t.Fatalf("acquire error = %v, want build_in_progress", err)
	}
	lock.release()
}
