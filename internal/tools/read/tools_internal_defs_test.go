package read

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
)

func TestDefs(t *testing.T) {
	defs := Defs()
	if len(defs) != 18 {
		t.Fatalf("Defs() = %d, want 18", len(defs))
	}
	if defs[0].RequiredScope != "" {
		t.Fatalf("Defs() first scope = %q", defs[0].RequiredScope)
	}
}

func TestRegisterNilServer(t *testing.T) {
	Register(nil, nil, config.Default())
	RegisterWithSourceIndex(nil, nil, nil, config.Default())
}

func TestSourcePageAsPublic(t *testing.T) {
	if got := sourcePageAsPublic(nil); got.Slug != "" {
		t.Fatalf("sourcePageAsPublic(nil) = %#v", got)
	}
	src := &hugosite.SourcePage{
		Slug:       "posts/hello",
		Title:      "Hello",
		Date:       "2026-07-11",
		Tags:       []string{"go"},
		Categories: []string{"blog"},
		Lang:       "en",
	}
	got := sourcePageAsPublic(src)
	if got.Slug != "/posts/hello/" || got.Title != "Hello" || len(got.Tags) != 1 || got.Lang != "en" {
		t.Fatalf("sourcePageAsPublic() = %#v", got)
	}
}
