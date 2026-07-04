package site

import (
	"strings"
	"testing"
)

func TestExtractMarkdownCoversCommonElements(t *testing.T) {
	got := ExtractMarkdown(`
<html lang="fr">
  <body>
    <header>ignore me</header>
    <h1>Title</h1>
    <p>Hello <strong>world</strong> and <em>friends</em>.</p>
    <p>Line<br>break</p>
    <p>Inline <code>x</code> and block:</p>
    <pre><code>fmt.Println("hi")</code></pre>
    <p><a href="/posts/hello/">Link</a></p>
    <ul><li>one</li><li>two</li></ul>
    <ol><li>first</li><li>second</li></ol>
    <blockquote>quoted</blockquote>
    <hr>
    <nav>ignore nav</nav>
    <script>ignore script</script>
    <style>ignore style</style>
    <footer>ignore footer</footer>
  </body>
</html>`)

	checks := []string{
		"# Title",
		"Hello **world** and *friends*.",
		"Line\nbreak",
		"Inline `x` and block:",
		"```\nfmt.Println(\"hi\")\n```",
		"[Link](/posts/hello/)",
		"- one",
		"- two",
		"1. first",
		"2. second",
		"> quoted",
		"---",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Fatalf("ExtractMarkdown() missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "ignore") {
		t.Fatalf("ExtractMarkdown() should ignore structural elements, got %q", got)
	}
}
