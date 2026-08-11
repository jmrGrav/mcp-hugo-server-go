package write_test

import "testing"

func TestListPageSnapshotsEnumeratesRestorableContentSnapshots(t *testing.T) {
	root := t.TempDir()
	writeBilingualBundle(t, root, "posts/snapshot-list")
	session, _, done := newTestServer(t, root)
	defer done()
	plan := callTool(t, session, "plan_content_change", map[string]any{
		"slug": "posts/snapshot-list", "lang": "fr",
		"operations": []any{map[string]any{"op": "update_body", "body": "Nouvelle version"}},
	})
	if plan.IsError {
		t.Fatalf("plan failed: %s", marshalContent(t, plan))
	}
	planID := decodeWriteData(t, plan)["plan_id"].(string)
	apply := callTool(t, session, "apply_content_plan", map[string]any{"plan_id": planID})
	if apply.IsError {
		t.Fatalf("apply failed: %s", marshalContent(t, apply))
	}
	applyData := decodeWriteData(t, apply)
	got := callTool(t, session, "list_page_snapshots", map[string]any{"slug": "posts/snapshot-list", "lang": "fr"})
	if got.IsError {
		t.Fatalf("list snapshots failed: %s", marshalContent(t, got))
	}
	data := decodeWriteData(t, got)
	rows := data["snapshots"].([]any)
	if len(rows) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(rows))
	}
	row := rows[0].(map[string]any)
	if row["revision_kind"] != "content_snapshot" {
		t.Fatalf("revision_kind = %v", row["revision_kind"])
	}
	if row["revision"] != applyData["before_revision"] {
		t.Fatalf("snapshot revision = %v, apply before_revision = %v", row["revision"], applyData["before_revision"])
	}
	if row["expires_at"] == nil || row["created_at"] == nil {
		t.Fatal("snapshot timestamps missing")
	}
}
