package storage

import "testing"

func TestNewSQLiteRejectsUnwritablePath(t *testing.T) {
	if _, err := NewSQLite("/nonexistent-dir-for-test/db.sqlite"); err == nil {
		t.Fatal("NewSQLite() error = nil, want an error opening under a nonexistent directory")
	}
}
