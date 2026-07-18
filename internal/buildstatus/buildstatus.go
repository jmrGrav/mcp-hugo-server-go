// Package buildstatus tracks the outcome of the most recent build_site
// attempt so other tools (get_runtime_status, create_page, update_page) can
// surface it without re-running a build themselves (#467). State is
// in-memory and process-lifetime only — a restart clears it, which is
// correct: there's no last-attempt history to report until this process has
// tried a build itself.
package buildstatus

import (
	"sync"
	"time"
)

// Snapshot is the last known build_site outcome.
type Snapshot struct {
	// Attempted is false until build_site has been called at least once in
	// this process's lifetime.
	Attempted  bool
	Status     string // "ok" or "failed"
	ErrorClass string // only set when Status == "failed"
	At         time.Time
}

var (
	mu   sync.RWMutex
	last Snapshot
)

// RecordSuccess records a successful build_site completion at at.
func RecordSuccess(at time.Time) {
	mu.Lock()
	defer mu.Unlock()
	last = Snapshot{Attempted: true, Status: "ok", At: at}
}

// RecordFailure records a failed build_site attempt at at, classified by
// errorClass (matching the error_class values build_site itself returns,
// e.g. "permission_denied", "build_error", "build_timeout").
func RecordFailure(errorClass string, at time.Time) {
	mu.Lock()
	defer mu.Unlock()
	last = Snapshot{Attempted: true, Status: "failed", ErrorClass: errorClass, At: at}
}

// Last returns the most recent recorded build_site outcome. Snapshot.Attempted
// is false if build_site has never been called in this process.
func Last() Snapshot {
	mu.RLock()
	defer mu.RUnlock()
	return last
}

// ResetForTest restores the zero state. Test-only — this package's state is
// process-global, so tests across any package that records a build outcome
// must reset it to avoid leaking state into unrelated tests in the same
// test binary.
func ResetForTest() {
	mu.Lock()
	defer mu.Unlock()
	last = Snapshot{}
}
