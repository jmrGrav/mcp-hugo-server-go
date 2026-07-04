package read

import "testing"

func TestDefs(t *testing.T) {
	defs := Defs()
	if len(defs) != 11 {
		t.Fatalf("Defs() = %d, want 11", len(defs))
	}
	if defs[0].RequiredScope != "content.read" {
		t.Fatalf("Defs() first scope = %q", defs[0].RequiredScope)
	}
}
