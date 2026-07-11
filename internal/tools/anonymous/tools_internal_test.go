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

func TestIsTaxonomyURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"/tags/hugo/", true},
		{"/tags/", true},
		{"/categories/infrastructure/", true},
		{"/categories/", true},
		{"/authors/jm/", true},
		{"/posts/my-article/", false},
		{"/", false},
		{"/about/", false},
		{"/tagsnot/", false},
	}
	for _, c := range cases {
		if got := isTaxonomyURL(c.url); got != c.want {
			t.Errorf("isTaxonomyURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}
