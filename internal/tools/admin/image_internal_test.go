package admin

import "testing"

func TestDefs(t *testing.T) {
	defs := Defs()
	if len(defs) != 5 {
		t.Fatalf("Defs() = %d, want 5", len(defs))
	}
	if defs[0].RequiredScope != "site.admin" {
		t.Fatalf("Defs() first scope = %q", defs[0].RequiredScope)
	}
}
