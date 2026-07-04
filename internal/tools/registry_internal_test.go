package tools

import "testing"

func TestRequiredScopeFor(t *testing.T) {
	r := NewRegistry()
	r.Register(ToolDef{Name: "list_pages", RequiredScope: ""})
	r.Register(ToolDef{Name: "validate_site", RequiredScope: "content.read"})

	if got, ok := r.RequiredScopeFor("list_pages"); !ok || got != "" {
		t.Fatalf("RequiredScopeFor(list_pages) = %q, %v", got, ok)
	}
	if got, ok := r.RequiredScopeFor("validate_site"); !ok || got != "content.read" {
		t.Fatalf("RequiredScopeFor(validate_site) = %q, %v", got, ok)
	}
	if got, ok := r.RequiredScopeFor("missing"); ok || got != "" {
		t.Fatalf("RequiredScopeFor(missing) = %q, %v", got, ok)
	}
}
