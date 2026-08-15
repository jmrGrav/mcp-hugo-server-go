package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestContentShadowValidationAndEmptyStats(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, invalid := range []contentRepresentation{
		{Representation: "source", Revision: "sha256:x"},
		{SourceKey: "posts/x", Representation: "invalid", Revision: "sha256:x"},
		{SourceKey: "posts/x", Representation: "source"},
	} {
		if err := d.syncContentRepresentation(invalid); err == nil {
			t.Fatalf("syncContentRepresentation accepted %#v", invalid)
		}
	}
	if err := d.syncContentRepresentation(contentRepresentation{SourceKey: "posts/x", Lang: " EN ", Representation: "source", Revision: "sha256:x"}); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteBundleRepresentations("posts/x", "invalid"); err == nil {
		t.Fatal("DeleteBundleRepresentations accepted invalid representation")
	}
	if err := d.DeleteBundleRepresentations("/posts/x/", "source"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`DELETE FROM content_shadow_runs_v1`); err != nil {
		t.Fatal(err)
	}
	if stats, err := d.LatestContentShadowStats(); err != nil || stats != nil {
		t.Fatalf("LatestContentShadowStats empty=%+v err=%v", stats, err)
	}
}

func TestContentShadowMalformedPersistedStatsAreObservable(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	insert := func(id int, languages, observed string) {
		t.Helper()
		if _, err := d.db.Exec(`INSERT INTO content_shadow_runs_v1(id,total_rows,source_rows,public_rows,missing_counterparts,legacy_mismatches,mismatch_digest,language_counts_json,observed_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, 0, 0, 0, 0, 0, "", languages, observed); err != nil {
			t.Fatal(err)
		}
	}
	insert(1, `{`, time.Now().UTC().Format(time.RFC3339Nano))
	if _, err := d.LatestContentShadowStats(); err == nil {
		t.Fatal("malformed language_counts_json was hidden")
	}
	if _, err := d.db.Exec(`DELETE FROM content_shadow_runs_v1`); err != nil {
		t.Fatal(err)
	}
	insert(2, `{}`, "not-a-time")
	if _, err := d.LatestContentShadowStats(); err == nil {
		t.Fatal("malformed observed_at was hidden")
	}
}

func TestContentShadowClosedDatabaseFailuresAreObservable(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RefreshContentShadowStats(); err == nil {
		t.Fatal("RefreshContentShadowStats hid closed database")
	}
	if _, err := d.LatestContentShadowStats(); err == nil {
		t.Fatal("LatestContentShadowStats hid closed database")
	}
	if err := d.DeleteContentRepresentation("posts/x", "en", "source"); err == nil {
		t.Fatal("DeleteContentRepresentation hid closed database")
	}
}
