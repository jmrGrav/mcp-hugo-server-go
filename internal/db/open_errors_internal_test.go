package db

import (
	"strings"
	"testing"
)

// Open is the single startup gate every mutation-tracking table passes
// through; a broken migration here fails the whole process at boot rather
// than surfacing later as a subtle data-loss bug. Its happy path is
// exercised throughout this package's other tests, but its error-return
// branches (pragma/migration failures) were previously untested.

func TestOpenRejectsUnwritablePath(t *testing.T) {
	// A path inside a nonexistent directory lets sql.Open succeed (the
	// modernc.org/sqlite driver opens lazily) but fails on the first
	// PRAGMA exec, exercising Open's "pragmas" error-return branch.
	_, err := Open("/nonexistent-dir-for-test/db.sqlite")
	if err == nil {
		t.Fatal("Open() error = nil, want an error opening under a nonexistent directory")
	}
	if !strings.Contains(err.Error(), "db:") {
		t.Fatalf("Open() error = %v, want it wrapped with the db: prefix", err)
	}
}
