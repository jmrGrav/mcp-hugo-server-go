package contentmodel

import "testing"

func TestSourceKeyFromLogicalPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"single-file leaf", "content/posts/hello.md", "posts/hello"},
		{"bundle default lang", "content/posts/hello/index.md", "posts/hello"},
		{"bundle other lang", "content/posts/hello/index.fr.md", "posts/hello"},
		{"section index default lang", "content/posts/_index.md", "posts"},
		{"section index other lang", "content/posts/_index.en.md", "posts"},
		{"root index default lang", "content/_index.md", ""},
		{"root index other lang", "content/_index.en.md", ""},
		{"empty input", "", ""},
		{"already stripped of content prefix", "posts/hello.md", "posts/hello"},
		{"nested section leaf", "content/docs/guides/setup.md", "docs/guides/setup"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SourceKeyFromLogicalPath(tc.in); got != tc.want {
				t.Fatalf("SourceKeyFromLogicalPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
