package write

import (
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
)

func TestContentPlanLockAcquiresReleasesAndTimesOut(t *testing.T) {
	lock := &contentPlanLock{}
	if err := lock.acquire(); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	lock.release()
	if !hugosite.ContentMu.TryLock() {
		t.Fatal("release left ContentMu held")
	}
	hugosite.ContentMu.Unlock()

	hugosite.ContentMu.Lock()
	defer hugosite.ContentMu.Unlock()
	blocked := &contentPlanLock{}
	err := blocked.acquire()
	if err == nil || !strings.Contains(err.Error(), "build_in_progress") {
		t.Fatalf("blocked acquire error = %v", err)
	}
	blocked.release()
}

func TestConsumeAppliedContentPlanReportsSuccessAndMissing(t *testing.T) {
	store := newPlanStore(time.Hour, 8)
	if err := store.put("plan", planEntry{CallerKey: "caller", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	status, warning := consumeAppliedContentPlan(store, "plan", "caller")
	if status != "updated" || warning != "" {
		t.Fatalf("successful consumption = %q, %q", status, warning)
	}
	status, warning = consumeAppliedContentPlan(store, "missing", "caller")
	if status != "partial_success" || warning == "" {
		t.Fatalf("missing consumption = %q, %q", status, warning)
	}
}
