package anonymous

import "testing"

func TestDefs(t *testing.T) {
	defs := Defs()
	if len(defs) != 9 {
		t.Fatalf("Defs() = %d, want 9", len(defs))
	}
	if defs[0].RequiredScope != "" {
		t.Fatalf("Defs() first scope = %q, want empty", defs[0].RequiredScope)
	}
}
