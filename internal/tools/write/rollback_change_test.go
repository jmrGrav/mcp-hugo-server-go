package write_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestRollbackChangeRestoresApplyContentPlanSnapshot is a regression test
// for the core #379-amended contract: rollback_change restores exactly the
// content apply_content_plan overwrote, guarded by expected_revision.
func TestRollbackChangeRestoresApplyContentPlanSnapshot(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	before := readFileString(t, contentRoot, "posts/article/index.md")

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "update_body", "body": "Changed body."}},
	})
	planID := decodeWriteData(t, planRes)["plan_id"].(string)
	target := decodeWriteData(t, planRes)["target"].(map[string]any)
	beforeRevision := target["revision"].(string)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}
	afterApplyRevision := decodeWriteData(t, applyRes)["after_revision"].(string)

	rollbackRes := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/article",
		"to_revision":       beforeRevision,
		"expected_revision": afterApplyRevision,
	})
	if rollbackRes.IsError {
		t.Fatalf("rollback_change failed: %s", marshalContent(t, rollbackRes))
	}
	rollbackData := decodeWriteData(t, rollbackRes)
	if rollbackData["status"] != "ok" {
		t.Fatalf("rollback_change status = %v, want ok", rollbackData["status"])
	}
	if rollbackData["after_revision"] != beforeRevision {
		t.Fatalf("rollback_change after_revision = %v, want %v", rollbackData["after_revision"], beforeRevision)
	}

	after := readFileString(t, contentRoot, "posts/article/index.md")
	if after != before {
		t.Fatalf("rollback_change did not restore original content:\nbefore=%q\nafter=%q", before, after)
	}
}

// TestRollbackChangeUnknownRevisionIsSnapshotNotFound verifies a revision
// this server never snapshotted (arbitrary git history, or simply never
// produced by apply_content_plan) is rejected, not silently accepted.
func TestRollbackChangeUnknownRevisionIsSnapshotNotFound(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "update_body", "body": "Changed body."}},
	})
	planID := decodeWriteData(t, planRes)["plan_id"].(string)
	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	currentRevision := decodeWriteData(t, applyRes)["after_revision"].(string)

	res := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/article",
		"to_revision":       "sha256:never-existed",
		"expected_revision": currentRevision,
	})
	if !res.IsError {
		t.Fatal("rollback_change with an unknown revision should fail")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "snapshot_not_found") {
		t.Fatalf("rollback_change unknown revision error = %s", raw)
	}
}

// TestRollbackChangeDistinguishesGitCommitFromContentSnapshot is the paired
// regression #1024/#1002 asked for: a git_commit revision (from
// list_page_revisions, real git history this deployment never snapshotted)
// must be rejected as non-restorable, while a content_snapshot revision
// (from apply_content_plan/list_page_snapshots) must actually restore.
func TestRollbackChangeDistinguishesGitCommitFromContentSnapshot(t *testing.T) {
	contentRoot := t.TempDir()
	pagePath := filepath.Join(contentRoot, "posts", "history", "index.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pagePath, []byte("---\ntitle: History\n---\nOriginal body.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, contentRoot, "init")
	runGit(t, contentRoot, "config", "user.email", "test@example.test")
	runGit(t, contentRoot, "config", "user.name", "Test User")
	runGit(t, contentRoot, "add", ".")
	runGit(t, contentRoot, "commit", "-m", "initial")

	session, _, done := newReadWriteTestServer(t, contentRoot)
	defer done()

	revRes := callTool(t, session, "list_page_revisions", map[string]any{"slug": "posts/history"})
	if revRes.IsError {
		t.Fatalf("list_page_revisions failed: %s", marshalContent(t, revRes))
	}
	revisions := decodeWriteData(t, revRes)["revisions"].([]any)
	if len(revisions) == 0 {
		t.Fatal("list_page_revisions returned no revisions")
	}
	gitCommit := revisions[0].(map[string]any)
	if gitCommit["revision_kind"] != "git_commit" {
		t.Fatalf("list_page_revisions revision_kind = %v, want git_commit", gitCommit["revision_kind"])
	}

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/history",
		"operations": []any{map[string]any{"op": "update_body", "body": "Changed body."}},
	})
	planID := decodeWriteData(t, planRes)["plan_id"].(string)
	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}
	applyData := decodeWriteData(t, applyRes)
	if applyData["revision_kind"] != "content_snapshot" {
		t.Fatalf("apply_content_plan revision_kind = %v, want content_snapshot", applyData["revision_kind"])
	}
	beforeRevision := applyData["before_revision"].(string)
	afterRevision := applyData["after_revision"].(string)

	// Non-restorable: real git history this server never snapshotted.
	gitAttempt := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/history",
		"to_revision":       gitCommit["commit"],
		"expected_revision": afterRevision,
	})
	if !gitAttempt.IsError {
		t.Fatal("rollback_change to a git_commit revision must fail as non-restorable")
	}
	if raw := marshalContent(t, gitAttempt); !strings.Contains(raw, "snapshot_not_found") {
		t.Fatalf("rollback_change git_commit attempt error = %s, want snapshot_not_found", raw)
	}

	// Restorable: the content_snapshot apply_content_plan captured.
	snapshotAttempt := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/history",
		"to_revision":       beforeRevision,
		"expected_revision": afterRevision,
	})
	if snapshotAttempt.IsError {
		t.Fatalf("rollback_change to a content_snapshot revision failed: %s", marshalContent(t, snapshotAttempt))
	}
	restored := readFileString(t, contentRoot, "posts/history/index.md")
	if !strings.Contains(restored, "Original body.") {
		t.Fatalf("rollback_change did not restore original content: %q", restored)
	}
}

// TestRollbackChangeRejectsLanguagePrefixedSlugWithExplicitLang is a
// regression test for the ambiguous-input guard added alongside #1002:
// passing both a language-prefixed slug ("fr/posts/bilingual") and an
// explicit lang param double-applies the language during source
// resolution, which previously surfaced as a confusing not_found rather
// than a clear, actionable error.
func TestRollbackChangeRejectsLanguagePrefixedSlugWithExplicitLang(t *testing.T) {
	contentRoot := t.TempDir()
	pageDir := filepath.Join(contentRoot, "posts", "bilingual")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "index.fr.md"), []byte("---\ntitle: Titre\n---\nContenu.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	res := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "fr/posts/bilingual",
		"lang":              "fr",
		"to_revision":       "sha256:irrelevant",
		"expected_revision": "sha256:irrelevant",
	})
	if !res.IsError {
		t.Fatal("rollback_change with a language-prefixed slug and explicit lang should fail")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "language_prefixed_slug_with_explicit_lang") {
		t.Fatalf("rollback_change language-prefixed slug error = %s", raw)
	}
}

// TestRollbackChangeRevisionConflict verifies rollback_change refuses to
// undo a newer, unrelated change — the same optimistic-concurrency guard
// every other write tool uses.
func TestRollbackChangeRevisionConflict(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "update_body", "body": "Changed body."}},
	})
	planData := decodeWriteData(t, planRes)
	planID := planData["plan_id"].(string)
	beforeRevision := planData["target"].(map[string]any)["revision"].(string)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}

	res := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/article",
		"to_revision":       beforeRevision,
		"expected_revision": "sha256:stale",
	})
	if !res.IsError {
		t.Fatal("rollback_change with a stale expected_revision should fail")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "revision_conflict") {
		t.Fatalf("rollback_change stale revision error = %s", raw)
	}
}

// TestRollbackChangeRejectsMalformedIdempotencyKey is a regression test for
// a gap in #888: rollback_change accepts and stores/replays idempotency_key
// exactly like the other eight mutation tools (create_page, update_page,
// delete_page, upload_page_asset, delete_page_asset, apply_content_plan,
// apply_bundle_plan, rollback_bundle) that #888 patched, but was missed from
// that sweep -- it had no validateIdempotencyKey call at handler entry, so a
// path-traversal-shaped key sailed straight into the idempotency store.
func TestRollbackChangeRejectsMalformedIdempotencyKey(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "update_body", "body": "Changed body."}},
	})
	planData := decodeWriteData(t, planRes)
	planID := planData["plan_id"].(string)
	beforeRevision := planData["target"].(map[string]any)["revision"].(string)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}
	afterApplyRevision := decodeWriteData(t, applyRes)["after_revision"].(string)

	res := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/article",
		"to_revision":       beforeRevision,
		"expected_revision": afterApplyRevision,
		"idempotency_key":   "../../etc/passwd",
	})
	if !res.IsError {
		t.Fatal("rollback_change with a path-traversal-shaped idempotency_key should fail")
	}
	raw := marshalContent(t, res)
	if !strings.Contains(raw, "invalid_params") || !strings.Contains(raw, "idempotency_key") {
		t.Fatalf("rollback_change malformed idempotency_key error = %s, want invalid_params mentioning idempotency_key", raw)
	}
}

// TestRollbackChangeDryRunDoesNotWrite verifies dry_run previews the diff
// without touching disk or requiring expected_revision.
func TestRollbackChangeDryRunDoesNotWrite(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "update_body", "body": "Changed body."}},
	})
	planData := decodeWriteData(t, planRes)
	planID := planData["plan_id"].(string)
	beforeRevision := planData["target"].(map[string]any)["revision"].(string)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}
	afterContent := readFileString(t, contentRoot, "posts/article/index.md")

	dryRes := callTool(t, session, "rollback_change", map[string]any{
		"slug":        "posts/article",
		"to_revision": beforeRevision,
		"dry_run":     true,
	})
	if dryRes.IsError {
		t.Fatalf("rollback_change dry_run failed: %s", marshalContent(t, dryRes))
	}
	dryData := decodeWriteData(t, dryRes)
	if dryData["dry_run"] != true {
		t.Fatalf("rollback_change dry_run response data.dry_run = %v, want true", dryData["dry_run"])
	}
	if diff, _ := dryData["diff"].(string); diff == "" {
		t.Fatal("rollback_change dry_run did not return a diff")
	}

	stillApplied := readFileString(t, contentRoot, "posts/article/index.md")
	if stillApplied != afterContent {
		t.Fatalf("rollback_change dry_run wrote to disk: before=%q after=%q", afterContent, stillApplied)
	}
}

// TestRollbackChangeIsRepeatable verifies rollback_change can roll back to
// the same snapshot more than once (IdempotentHint: true) — unlike a plan,
// a snapshot is not consumed on use.
func TestRollbackChangeIsRepeatable(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "update_body", "body": "Changed body."}},
	})
	planData := decodeWriteData(t, planRes)
	planID := planData["plan_id"].(string)
	beforeRevision := planData["target"].(map[string]any)["revision"].(string)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	afterApplyRevision := decodeWriteData(t, applyRes)["after_revision"].(string)

	firstRollback := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/article",
		"to_revision":       beforeRevision,
		"expected_revision": afterApplyRevision,
	})
	if firstRollback.IsError {
		t.Fatalf("first rollback_change failed: %s", marshalContent(t, firstRollback))
	}

	// Re-apply the same plan's change via a fresh plan, then roll back to
	// the same original beforeRevision a second time.
	planRes2 := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "update_body", "body": "Changed body again."}},
	})
	planID2 := decodeWriteData(t, planRes2)["plan_id"].(string)
	applyRes2 := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID2})
	if applyRes2.IsError {
		t.Fatalf("second apply_content_plan failed: %s", marshalContent(t, applyRes2))
	}
	afterApplyRevision2 := decodeWriteData(t, applyRes2)["after_revision"].(string)

	secondRollback := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/article",
		"to_revision":       beforeRevision,
		"expected_revision": afterApplyRevision2,
	})
	if secondRollback.IsError {
		t.Fatalf("second rollback_change to the same snapshot failed: %s", marshalContent(t, secondRollback))
	}
}

// TestRollbackChangeBilingualIsPerLanguage is a regression test for the
// same bug class TestPlanContentChangeBilingualDeltaIsPerLanguage guards:
// rollback_change must never restore the wrong language's file. The
// snapshot store is keyed by the resolved file's own path (not a
// lang-blind slug lookup), so this is expected to pass — pinning it as a
// guard against a future change reintroducing that class of bug.
func TestRollbackChangeBilingualIsPerLanguage(t *testing.T) {
	contentRoot := t.TempDir()
	pageDir := filepath.Join(contentRoot, "posts", "bilingual")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	frFile := filepath.Join(pageDir, "index.fr.md")
	enFile := filepath.Join(pageDir, "index.en.md")
	if err := os.WriteFile(frFile, []byte("---\ntitle: Titre\ntags: [\"francais\"]\n---\nContenu original.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(enFile, []byte("---\ntitle: Title\ntags: [\"english\"]\n---\nOriginal content.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	session, _, done := newTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/bilingual",
		"lang":       "fr",
		"operations": []any{map[string]any{"op": "update_body", "body": "Contenu modifie."}},
	})
	planData := decodeWriteData(t, planRes)
	planID := planData["plan_id"].(string)
	beforeRevision := planData["target"].(map[string]any)["revision"].(string)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}
	afterApplyRevision := decodeWriteData(t, applyRes)["after_revision"].(string)

	enBeforeRollback := readFileString(t, contentRoot, "posts/bilingual/index.en.md")

	rollbackRes := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/bilingual",
		"lang":              "fr",
		"to_revision":       beforeRevision,
		"expected_revision": afterApplyRevision,
	})
	if rollbackRes.IsError {
		t.Fatalf("rollback_change failed: %s", marshalContent(t, rollbackRes))
	}

	frAfterRollback := readFileString(t, contentRoot, "posts/bilingual/index.fr.md")
	if !strings.Contains(frAfterRollback, "Contenu original") {
		t.Fatalf("fr file should be restored to its original body, got: %s", frAfterRollback)
	}
	if strings.Contains(frAfterRollback, "modifie") {
		t.Fatalf("fr file should no longer contain the rolled-back edit, got: %s", frAfterRollback)
	}

	enAfterRollback := readFileString(t, contentRoot, "posts/bilingual/index.en.md")
	if enAfterRollback != enBeforeRollback {
		t.Fatalf("en file must be untouched by a fr-scoped rollback:\nbefore=%q\nafter=%q", enBeforeRollback, enAfterRollback)
	}
}

func TestRollbackChangeBilingualReadToolsMatchRestoredLanguage(t *testing.T) {
	contentRoot := t.TempDir()
	pageDir := filepath.Join(contentRoot, "posts", "bilingual")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	frFile := filepath.Join(pageDir, "index.fr.md")
	enFile := filepath.Join(pageDir, "index.en.md")
	if err := os.WriteFile(frFile, []byte("---\ntitle: Titre FR\ndate: 2026-08-09T16:47:10Z\ndescription: Description FR\ntags: [\"fr-tag\"]\ncategories: [\"fr-cat\"]\nfeaturedImage: /images/fr-featured.jpg\ndraft: true\n---\nContenu original FR.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(enFile, []byte("---\ntitle: Title EN\ndate: 2026-08-09T16:47:14Z\ndescription: Description EN\ntags: [\"en-tag\"]\ncategories: [\"en-cat\"]\nfeaturedImage: /images/en-featured.jpg\ndraft: false\n---\nOriginal content EN.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	session, idx, done := newReadWriteTestServer(t, contentRoot)
	defer done()

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug": "posts/bilingual",
		"lang": "fr",
		"operations": []any{
			map[string]any{"op": "set_field", "field": "description", "value": "Description FR modifiee"},
			map[string]any{"op": "update_body", "body": "Contenu modifie FR."},
		},
	})
	planData := decodeWriteData(t, planRes)
	planID := planData["plan_id"].(string)
	beforeRevision := planData["target"].(map[string]any)["revision"].(string)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}
	afterApplyRevision := decodeWriteData(t, applyRes)["after_revision"].(string)

	rollbackRes := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/bilingual",
		"lang":              "fr",
		"to_revision":       beforeRevision,
		"expected_revision": afterApplyRevision,
	})
	if rollbackRes.IsError {
		t.Fatalf("rollback_change failed: %s", marshalContent(t, rollbackRes))
	}
	rollbackData := decodeWriteData(t, rollbackRes)
	if rollbackData["after_revision"] != beforeRevision {
		t.Fatalf("rollback_change after_revision = %v, want %v", rollbackData["after_revision"], beforeRevision)
	}

	frEntry, ok := idx.GetBySlugLang("posts/bilingual", "fr")
	if !ok {
		t.Fatal("fr entry missing from source index after rollback_change")
	}
	if frEntry.Date != "2026-08-09T16:47:10Z" {
		t.Fatalf("source index fr date = %q, want restored FR date", frEntry.Date)
	}
	if frEntry.Title != "Titre FR" {
		t.Fatalf("source index fr title = %q, want restored FR title", frEntry.Title)
	}
	if got := frEntry.FrontmatterRaw["description"]; got != "Description FR" {
		t.Fatalf("source index fr description = %v, want restored FR description", got)
	}

	fmRes := callTool(t, session, "get_page_frontmatter", map[string]any{
		"slug": "/posts/bilingual/",
		"lang": "fr",
	})
	if fmRes.IsError {
		t.Fatalf("get_page_frontmatter failed: %s", marshalContent(t, fmRes))
	}
	fm := decodeNestedMap(t, fmRes, "data", "frontmatter")
	if got := fm["date"]; got != "2026-08-09T16:47:10Z" {
		t.Fatalf("get_page_frontmatter date = %v, want restored FR date", got)
	}
	if got := fm["title"]; got != "Titre FR" {
		t.Fatalf("get_page_frontmatter title = %v, want restored FR title", got)
	}
	if got := fm["description"]; got != "Description FR" {
		t.Fatalf("get_page_frontmatter description = %v, want restored FR description", got)
	}
	if got := fm["featured_image"]; got != "/images/fr-featured.jpg" {
		t.Fatalf("get_page_frontmatter featured_image = %v, want restored FR featured image", got)
	}
	if got := fm["revision"]; got != beforeRevision {
		t.Fatalf("get_page_frontmatter revision = %v, want %v", got, beforeRevision)
	}

	editRes := callTool(t, session, "get_page_for_edit", map[string]any{
		"slug": "/posts/bilingual/",
		"lang": "fr",
	})
	if editRes.IsError {
		t.Fatalf("get_page_for_edit failed: %s", marshalContent(t, editRes))
	}
	page := decodeNestedMap(t, editRes, "data", "page")
	if got := page["revision"]; got != beforeRevision {
		t.Fatalf("get_page_for_edit revision = %v, want %v", got, beforeRevision)
	}
	editFM, ok := page["frontmatter"].(map[string]any)
	if !ok {
		t.Fatalf("get_page_for_edit frontmatter type = %T, want map[string]any", page["frontmatter"])
	}
	if got := editFM["date"]; got != "2026-08-09T16:47:10Z" {
		t.Fatalf("get_page_for_edit frontmatter.date = %v, want restored FR date", got)
	}
	if got := editFM["description"]; got != "Description FR" {
		t.Fatalf("get_page_for_edit frontmatter.description = %v, want restored FR description", got)
	}
	if got := editFM["featured_image"]; got != "/images/fr-featured.jpg" {
		t.Fatalf("get_page_for_edit frontmatter.featured_image = %v, want restored FR featured image", got)
	}
	if got := editFM["draft"]; got != true {
		t.Fatalf("get_page_for_edit frontmatter.draft = %v, want true", got)
	}
}

func newReadWriteTestServer(t *testing.T, contentRoot string) (*mcp.ClientSession, *hugosite.SourceIndex, func()) {
	t.Helper()
	pg, err := security.New(contentRoot, true)
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}
	idx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("hugosite.NewSourceIndex: %v", err)
	}
	cfg := config.Default()
	cfg.ContentRoot = contentRoot

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	publicIdx := &site.Index{}
	write.Register(s, pg, idx, cfg, nil, nil)
	read.Register(s, publicIdx, cfg, idx)
	read.RegisterWithSourceIndex(s, publicIdx, idx, cfg)

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	session, err := c.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	return session, idx, func() { _ = session.Close() }
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func decodeNestedMap(t *testing.T, res *mcp.CallToolResult, path ...string) map[string]any {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var current any
	if err := json.Unmarshal(raw, &current); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("path %v hit %T, want map[string]any", path, current)
		}
		current, ok = m[key]
		if !ok {
			t.Fatalf("path %v missing key %q", path, key)
		}
	}
	m, ok := current.(map[string]any)
	if !ok {
		t.Fatalf("path %v ended on %T, want map[string]any", path, current)
	}
	return m
}

// TestRollbackChangeBilingualIndexDateSurvivesCrossLanguageRebuild is a
// tighter #911 regression than TestRollbackChangeBilingualReadToolsMatchRestoredLanguage
// above. That test seeds both translations as pre-existing files parsed by a
// single NewSourceIndex walk, where FR happens to land at a higher pages[]
// index than EN — so the pre-fix lang-blind idx.GetBySlug(slug) already
// resolved to FR by accident, and the test passes identically whether or not
// the language-scoped lookup fix is applied (verified: reverting just that
// switch back to GetBySlug(in.Slug) and rerunning that test still passes).
//
// This test forces the trigger condition directly instead: EN is upserted
// into the index after FR is already present, so EN occupies the higher
// index and a lang-blind idx.GetBySlug("posts/date-probe") resolves to EN.
// It asserts on Date rather than Title, because Title is patched
// unconditionally from the restored snapshot regardless of which entry
// rollback started from (see restoredTitle below) and can't distinguish
// correct from corrupted either way — Date was the field the original audit
// actually observed leaking cross-language.
//
// Note on what this test currently proves: rollback_change.go now resyncs
// every SourcePage field (Date/Draft/PublishDate/ExpiryDate included) from
// the restored snapshot's own frontmatter, which alone is now sufficient to
// keep this test green — verified by reverting only the language-scoped
// lookup switch back to bare GetBySlug(in.Slug) while keeping that resync,
// and confirming the test still passes. So this test guards the resync (the
// mechanism that currently prevents the #911 symptom observably), not the
// language-scoped lookup in isolation; the lookup fix is kept as defense in
// depth against a future SourcePage field that isn't part of the resync.
func TestRollbackChangeBilingualIndexDateSurvivesCrossLanguageRebuild(t *testing.T) {
	contentRoot := t.TempDir()
	pageDir := filepath.Join(contentRoot, "posts", "date-probe")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	frFile := filepath.Join(pageDir, "index.fr.md")
	frContent := "---\ntitle: Titre FR\ndate: 2026-01-01T00:00:00Z\n---\nContenu original FR.\n"
	if err := os.WriteFile(frFile, []byte(frContent), 0o644); err != nil {
		t.Fatal(err)
	}
	beforeRevision := contentmodel.SourceRevisionBytes([]byte(frContent))

	session, idx, done := newReadWriteTestServer(t, contentRoot)
	defer done()

	// EN is upserted after FR is already indexed, matching real chronological
	// creation order (FR created, then EN created later) — this is what
	// gives EN the higher pages[] index and makes it win the lang-blind
	// bySlug lookup the pre-fix code used.
	enFile := filepath.Join(pageDir, "index.en.md")
	enContent := "---\ntitle: Title EN\ndate: 2026-06-15T00:00:00Z\n---\nOriginal content EN.\n"
	if err := os.WriteFile(enFile, []byte(enContent), 0o644); err != nil {
		t.Fatal(err)
	}
	idx.Upsert(hugosite.SourcePage{
		Slug: "posts/date-probe", Lang: "en", FilePath: enFile,
		Title: "Title EN", Date: "2026-06-15T00:00:00Z",
	})

	if bare, ok := idx.GetBySlug("posts/date-probe"); !ok || bare.Lang != "en" {
		t.Fatalf("test setup invalid: bare GetBySlug must resolve to the EN entry to reproduce the #911 trigger condition, got lang=%q ok=%v", bare.Lang, ok)
	}

	updateRes := callTool(t, session, "update_page", map[string]any{
		"slug":              "posts/date-probe",
		"lang":              "fr",
		"description":       "Description FR modifiee",
		"expected_revision": beforeRevision,
	})
	if updateRes.IsError {
		t.Fatalf("update_page failed: %s", marshalContent(t, updateRes))
	}
	afterUpdateRevision := decodeWriteData(t, updateRes)["new_revision"].(string)

	rollbackRes := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/date-probe",
		"lang":              "fr",
		"to_revision":       beforeRevision,
		"expected_revision": afterUpdateRevision,
	})
	if rollbackRes.IsError {
		t.Fatalf("rollback_change failed: %s", marshalContent(t, rollbackRes))
	}

	frEntry, ok := idx.GetBySlugLang("posts/date-probe", "fr")
	if !ok {
		t.Fatal("fr entry missing from source index after rollback_change")
	}
	if frEntry.Date != "2026-01-01T00:00:00Z" {
		t.Errorf("source index fr date = %q after rollback, want restored FR date %q (EN date is %q — a match here means the index rebuild pulled from the wrong language)",
			frEntry.Date, "2026-01-01T00:00:00Z", "2026-06-15T00:00:00Z")
	}
}

// TestRollbackChangeRestoresUpdatePageSnapshot is a regression test for #629:
// before this fix, only apply_content_plan captured a snapshot, so a
// revision produced solely by update_page (with no plan_content_change /
// apply_content_plan ever involved) could never be rolled back to —
// rollback_change failed with snapshot_not_found, indistinguishable from
// "this revision never existed". create_page itself is deliberately not
// snapshotted (there's no meaningful pre-create state to restore to); this
// test exercises exactly the scenario the issue calls out: create a page,
// then update it once via update_page, then roll back to the revision
// update_page overwrote.
func TestRollbackChangeRestoresUpdatePageSnapshot(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	createRes := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/first",
		"title":      "First Post",
		"body":       "Original body.",
		"tags":       []any{},
		"categories": []any{},
	})
	if createRes.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, createRes))
	}
	createData := decodeWriteData(t, createRes)
	beforeUpdateRevision := createData["new_revision"].(string)
	before := readFileString(t, contentRoot, "posts/first/index.md")

	updateRes := callTool(t, session, "update_page", map[string]any{
		"slug":              "posts/first",
		"body":              "Updated body.",
		"expected_revision": beforeUpdateRevision,
	})
	if updateRes.IsError {
		t.Fatalf("update_page failed: %s", marshalContent(t, updateRes))
	}
	updateData := decodeWriteData(t, updateRes)
	afterUpdateRevision := updateData["new_revision"].(string)

	updated := readFileString(t, contentRoot, "posts/first/index.md")
	if !strings.Contains(updated, "Updated body.") {
		t.Fatalf("update_page did not apply, got: %s", updated)
	}

	// This is the case the issue's title names directly: to_revision here
	// (beforeUpdateRevision) was produced by create_page and only ever
	// overwritten by update_page — apply_content_plan was never called for
	// this page. Before the fix, this failed with snapshot_not_found.
	rollbackRes := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/first",
		"to_revision":       beforeUpdateRevision,
		"expected_revision": afterUpdateRevision,
	})
	if rollbackRes.IsError {
		t.Fatalf("rollback_change failed: %s", marshalContent(t, rollbackRes))
	}
	rollbackData := decodeWriteData(t, rollbackRes)
	if rollbackData["status"] != "ok" {
		t.Fatalf("rollback_change status = %v, want ok", rollbackData["status"])
	}
	if rollbackData["after_revision"] != beforeUpdateRevision {
		t.Fatalf("rollback_change after_revision = %v, want %v", rollbackData["after_revision"], beforeUpdateRevision)
	}

	after := readFileString(t, contentRoot, "posts/first/index.md")
	if after != before {
		t.Fatalf("rollback_change did not restore pre-update content:\nbefore=%q\nafter=%q", before, after)
	}
}

// TestRollbackChangeRejectsSnapshotWithBlockedShortcode is a regression
// test for a strix-security finding on PR #636: extending snapshot capture
// to update_page's primary write path expanded rollback_change's reach to
// legacy content that predates (or otherwise bypassed) #590's blocked-
// shortcode denylist. A snapshot is a verbatim copy of whatever content the
// page held before the write that produced it — create_page/update_page
// reject a body invoking a blocked shortcode outright, but restoring a
// snapshot of already-existing content must not be a side door around that
// same policy.
func TestRollbackChangeRejectsSnapshotWithBlockedShortcode(t *testing.T) {
	contentRoot := t.TempDir()
	session, _, done := newTestServer(t, contentRoot)
	defer done()

	createRes := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/legacy",
		"title":      "Legacy Post",
		"body":       "Clean original body.",
		"tags":       []any{},
		"categories": []any{},
	})
	if createRes.IsError {
		t.Fatalf("create_page failed: %s", marshalContent(t, createRes))
	}

	// Simulate legacy content that already contains a shortcode this
	// server would now reject on a direct write — e.g. written before
	// #590's denylist existed, or via a path outside this server's own
	// validation. create_page/update_page can't produce this directly
	// (that's the whole point of the check being tested), so it's written
	// straight to disk. The revision is recomputed from the mutated bytes
	// so the following update_page's expected_revision check sees the
	// real on-disk state, not the pre-mutation revision create_page
	// returned.
	pagePath := filepath.Join(contentRoot, "posts/legacy/index.md")
	legacyRaw := readFileString(t, contentRoot, "posts/legacy/index.md")
	legacyRaw = strings.Replace(legacyRaw, "Clean original body.", "Clean original body.\n\n{{< script >}}alert(1){{< /script >}}", 1)
	if err := os.WriteFile(pagePath, []byte(legacyRaw), 0o644); err != nil {
		t.Fatalf("failed to write legacy content directly: %v", err)
	}
	beforeUpdateRevision := contentmodel.SourceRevisionBytes([]byte(legacyRaw))

	updateRes := callTool(t, session, "update_page", map[string]any{
		"slug":              "posts/legacy",
		"body":              "New clean body.",
		"expected_revision": beforeUpdateRevision,
	})
	if updateRes.IsError {
		t.Fatalf("update_page failed: %s", marshalContent(t, updateRes))
	}
	afterUpdateRevision := decodeWriteData(t, updateRes)["new_revision"].(string)

	// beforeUpdateRevision now points at a snapshot containing the blocked
	// shortcode. Restoring it must be rejected, not silently written.
	rollbackRes := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/legacy",
		"to_revision":       beforeUpdateRevision,
		"expected_revision": afterUpdateRevision,
	})
	if !rollbackRes.IsError {
		t.Fatal("rollback_change restoring a snapshot with a blocked shortcode should fail")
	}
	raw := marshalContent(t, rollbackRes)
	if !strings.Contains(raw, "blocked shortcode") {
		t.Fatalf("rollback_change error = %s, want a blocked-shortcode rejection", raw)
	}

	// The rejected rollback must never have touched disk.
	stillNew := readFileString(t, contentRoot, "posts/legacy/index.md")
	if !strings.Contains(stillNew, "New clean body.") {
		t.Fatalf("rejected rollback_change modified the file on disk: %q", stillNew)
	}
}

// TestRollbackChangeUpdatesInMemorySourceIndexBody is a regression test for
// #643: rollback_change wrote the restored content to disk correctly, but
// never reassigned the in-memory SourceIndex entry's Body field — only
// Tags/Categories/Title/Revision were updated on the upserted entry. Every
// tool reading a page's body from the source index before the next full
// index rebuild (get_page_markdown in particular) kept serving the
// pre-rollback body as a result, with no staleness signal (index_state
// still reported "fresh"). This asserts the fix directly against the index
// entry rollback_change updates, the same one get_page_markdown reads from.
func TestRollbackChangeUpdatesInMemorySourceIndexBody(t *testing.T) {
	contentRoot := t.TempDir()
	writeBundle(t, contentRoot, "posts/article")
	session, idx, done := newTestServer(t, contentRoot)
	defer done()

	originalEntry, ok := idx.GetBySlug("posts/article")
	if !ok {
		t.Fatal("posts/article missing from source index after writeBundle")
	}
	originalBody := originalEntry.Body

	planRes := callTool(t, session, "plan_content_change", map[string]any{
		"slug":       "posts/article",
		"operations": []any{map[string]any{"op": "update_body", "body": "Changed body for #643 regression test."}},
	})
	planID := decodeWriteData(t, planRes)["plan_id"].(string)
	target := decodeWriteData(t, planRes)["target"].(map[string]any)
	beforeRevision := target["revision"].(string)

	applyRes := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if applyRes.IsError {
		t.Fatalf("apply_content_plan failed: %s", marshalContent(t, applyRes))
	}
	afterApplyRevision := decodeWriteData(t, applyRes)["after_revision"].(string)

	changedEntry, ok := idx.GetBySlug("posts/article")
	if !ok {
		t.Fatal("posts/article missing from source index after apply_content_plan")
	}
	if !strings.Contains(changedEntry.Body, "Changed body for #643 regression test.") {
		t.Fatalf("source index Body not updated by apply_content_plan: %q", changedEntry.Body)
	}

	rollbackRes := callTool(t, session, "rollback_change", map[string]any{
		"slug":              "posts/article",
		"to_revision":       beforeRevision,
		"expected_revision": afterApplyRevision,
	})
	if rollbackRes.IsError {
		t.Fatalf("rollback_change failed: %s", marshalContent(t, rollbackRes))
	}

	restoredEntry, ok := idx.GetBySlug("posts/article")
	if !ok {
		t.Fatal("posts/article missing from source index after rollback_change")
	}
	if restoredEntry.Body != originalBody {
		t.Fatalf("source index Body not restored by rollback_change:\nwant=%q\ngot=%q", originalBody, restoredEntry.Body)
	}
	if strings.Contains(restoredEntry.Body, "Changed body for #643 regression test.") {
		t.Fatal("source index Body still contains post-apply content after rollback_change — #643 regression")
	}
}
