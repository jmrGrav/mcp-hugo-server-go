package admin

import (
	"os"
	"path/filepath"
	"testing"
)

// #1180: source_revision (hashTree(cfg.ContentRoot)) must be deterministic
// over actual site content. delete_page/delete_page_asset append to
// .mcp-audit.log directly under ContentRoot on every mutation, including
// ones later reversed by the caller — so a create->modify->delete
// round-trip that leaves every real page byte-identical to baseline was
// still changing hashTree's result, because the ever-growing audit log
// itself was part of the hashed tree. This reproduces the reported
// scenario directly against hashTree, without needing the full
// create/modify/delete/build tool round-trip.
func TestHashTreeIgnoresAuditLogGrowth(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("---\ntitle: A\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	before, err := hashTree(root)
	if err != nil {
		t.Fatalf("hashTree (before): %v", err)
	}

	// Simulate delete_page_asset's append-only audit trail growing from
	// mutations that are later fully reversed, leaving real content
	// (index.md) untouched.
	auditLog := filepath.Join(root, ".mcp-audit.log")
	if err := os.WriteFile(auditLog, []byte("2026-01-01T00:00:00Z DELETE_ASSET disposable/hero.png\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	after, err := hashTree(root)
	if err != nil {
		t.Fatalf("hashTree (after audit log write): %v", err)
	}
	if before != after {
		t.Fatalf("hashTree changed from audit log growth alone: before=%q after=%q", before, after)
	}

	// Growing the log further (simulating more mutations) must still be a
	// no-op for the hash.
	f, err := os.OpenFile(auditLog, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("2026-01-01T00:05:00Z DELETE_ASSET disposable/other.png\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	grown, err := hashTree(root)
	if err != nil {
		t.Fatalf("hashTree (after further growth): %v", err)
	}
	if before != grown {
		t.Fatalf("hashTree changed from further audit log growth: before=%q grown=%q", before, grown)
	}

	// A genuine content change (a new real page) must still be detected —
	// the exclusion must not blind hashTree to actual drift.
	if err := os.WriteFile(filepath.Join(root, "other.md"), []byte("---\ntitle: B\n---\nOther body.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := hashTree(root)
	if err != nil {
		t.Fatalf("hashTree (after real content change): %v", err)
	}
	if changed == before {
		t.Fatalf("hashTree did not detect a genuine content change")
	}
}
