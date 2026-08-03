package write_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const maxTestContentTTLHours = 24 * 7

// TestCreatePageTestContentForcesDraftAndWritesMetadata is the core
// regression test for #661: create_page's opt-in test_content parameter
// must force draft:true regardless of any other setting, and write
// test_content/test_content_owner/test_content_expires_at into the page's
// frontmatter, echoing the effective expiry back in the response.
func TestCreatePageTestContentForcesDraftAndWritesMetadata(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/audit-run", "title": "Audit Run", "body": "Body.",
		"tags": []any{}, "categories": []any{},
		"test_content": map[string]any{"ttl_hours": 2, "owner": "audit-session-42"},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page returned error: %s", raw)
	}
	data := decodeWriteData(t, res)
	expiresAt, _ := data["test_content_expires_at"].(string)
	if expiresAt == "" {
		t.Fatal("create_page data.test_content_expires_at is empty, want the effective expiry echoed back")
	}
	parsed, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		t.Fatalf("test_content_expires_at = %q not RFC3339: %v", expiresAt, err)
	}
	if wait := time.Until(parsed); wait < 90*time.Minute || wait > 130*time.Minute {
		t.Errorf("test_content_expires_at = %v from now, want ~2h (ttl_hours=2)", wait)
	}

	raw, err := os.ReadFile(filepath.Join(contentRoot, "posts", "audit-run", "index.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, "draft: true") {
		t.Errorf("frontmatter missing draft: true (test_content must force it): %s", content)
	}
	if !strings.Contains(content, "test_content: true") {
		t.Errorf("frontmatter missing test_content: true: %s", content)
	}
	if !strings.Contains(content, "test_content_owner: audit-session-42") {
		t.Errorf("frontmatter missing test_content_owner: %s", content)
	}
	if !strings.Contains(content, "test_content_expires_at:") {
		t.Errorf("frontmatter missing test_content_expires_at: %s", content)
	}
}

// TestCreatePageTestContentDefaultTTL confirms omitting ttl_hours falls
// back to the 24h default rather than failing or defaulting to zero.
func TestCreatePageTestContentDefaultTTL(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/audit-default-ttl", "title": "T", "body": "B",
		"tags": []any{}, "categories": []any{},
		"test_content": map[string]any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page returned error: %s", raw)
	}
	data := decodeWriteData(t, res)
	expiresAt, _ := data["test_content_expires_at"].(string)
	parsed, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		t.Fatalf("test_content_expires_at = %q not RFC3339: %v", expiresAt, err)
	}
	if wait := time.Until(parsed); wait < 23*time.Hour || wait > 25*time.Hour {
		t.Errorf("test_content_expires_at = %v from now, want ~24h default", wait)
	}
}

func TestCreatePageTestContentRejectsInvalidTTLBounds(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	for _, ttl := range []int{-1, 0, maxTestContentTTLHours + 1} {
		res := callTool(t, session, "create_page", map[string]any{
			"slug": "posts/audit-bad-ttl", "title": "T", "body": "B",
			"tags": []any{}, "categories": []any{},
			"test_content": map[string]any{"ttl_hours": ttl},
		})
		if !res.IsError {
			t.Fatalf("create_page with test_content.ttl_hours=%d expected an error, got success", ttl)
		}
	}
}

// TestCreatePageWithoutTestContentDefaultsToNonDraft confirms omitting
// test_content entirely preserves the existing, unaffected default
// behavior — draft:false, no test_content frontmatter at all.
func TestCreatePageWithoutTestContentDefaultsToNonDraft(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/normal-post", "title": "T", "body": "B",
		"tags": []any{}, "categories": []any{},
	})
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("create_page returned error: %s", raw)
	}
	data := decodeWriteData(t, res)
	if _, present := data["test_content_expires_at"]; present {
		t.Errorf("data.test_content_expires_at present without test_content in the request: %#v", data["test_content_expires_at"])
	}
	raw, err := os.ReadFile(filepath.Join(contentRoot, "posts", "normal-post", "index.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, "draft: false") {
		t.Errorf("frontmatter should still be draft: false without test_content: %s", content)
	}
	if strings.Contains(content, "test_content") {
		t.Errorf("frontmatter must not mention test_content at all without opting in: %s", content)
	}
}

// TestUpdatePageRejectsDedraftingTestContent is a regression test for #728:
// once a page carries the explicit test_content marker, later write paths
// must not be allowed to flip draft:false and make disposable audit content
// publishable again.
func TestUpdatePageRejectsDedraftingTestContent(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	createRes := callTool(t, session, "create_page", map[string]any{
		"slug": "posts/audit-guarded", "title": "Audit Guarded", "body": "Body.",
		"tags": []any{}, "categories": []any{},
		"test_content": map[string]any{"ttl_hours": 2, "owner": "audit-session-42"},
	})
	if createRes.IsError {
		raw, _ := json.Marshal(createRes.Content)
		t.Fatalf("create_page returned error: %s", raw)
	}
	createData := decodeWriteData(t, createRes)
	revision, _ := createData["new_revision"].(string)
	if revision == "" {
		t.Fatal("create_page missing data.new_revision")
	}

	res := callTool(t, session, "update_page", map[string]any{
		"slug":              "posts/audit-guarded",
		"draft":             false,
		"expected_revision": revision,
	})
	if !res.IsError {
		t.Fatal("update_page should reject draft:false while test_content is still present")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "test_content") || !strings.Contains(raw, "draft") {
		t.Fatalf("update_page error should explain the test_content/draft invariant, got: %s", raw)
	}

	content := readFileString(t, contentRoot, "posts/audit-guarded/index.md")
	if !strings.Contains(content, "draft: true") {
		t.Fatalf("update_page must not de-draft a test_content page, got: %s", content)
	}
}
