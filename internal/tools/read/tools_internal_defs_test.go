package read

import "testing"

func TestDefs(t *testing.T) {
	defs := Defs()
	if len(defs) != 12 {
		t.Fatalf("Defs() = %d, want 12", len(defs))
	}
	if defs[0].RequiredScope != "content.read" {
		t.Fatalf("Defs() first scope = %q", defs[0].RequiredScope)
	}
}
