package admin

import (
	"reflect"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
)

// TestClassifyPendingPages covers #858 AC1/AC3: the changed set is split into
// published (included) vs draft/test-content (excluded_drafts), with stable
// "slug" / "slug:lang" identifiers, and deleted_outputs is always a non-nil
// (documented empty) slice.
func TestClassifyPendingPages(t *testing.T) {
	pending := []hugosite.SourcePage{
		{Slug: "posts/example", Lang: "fr"},                                                      // normal → included
		{Slug: "posts/example", Lang: "en", Draft: true},                                         // draft → excluded
		{Slug: "posts/beta", Lang: "", Draft: false},                                             // default lang, normal
		{Slug: "posts/secret", Lang: "fr", FrontmatterRaw: map[string]any{"test_content": true}}, // test_content → excluded
	}
	got := classifyPendingPages(pending)

	wantIncluded := []string{"posts/beta", "posts/example:fr"}
	wantExcluded := []string{"posts/example:en", "posts/secret:fr"}
	if !reflect.DeepEqual(got.Included, wantIncluded) {
		t.Fatalf("included = %v, want %v", got.Included, wantIncluded)
	}
	if !reflect.DeepEqual(got.ExcludedDrafts, wantExcluded) {
		t.Fatalf("excluded_drafts = %v, want %v", got.ExcludedDrafts, wantExcluded)
	}
	if got.DeletedOutputs == nil || len(got.DeletedOutputs) != 0 {
		t.Fatalf("deleted_outputs = %v, want non-nil empty", got.DeletedOutputs)
	}
}

func TestClassifyPendingPagesEmpty(t *testing.T) {
	got := classifyPendingPages(nil)
	if len(got.Included) != 0 || len(got.ExcludedDrafts) != 0 || got.Included == nil || got.ExcludedDrafts == nil {
		t.Fatalf("empty changed set should yield non-nil empty slices, got %+v", got)
	}
}

// TestBuildStages covers #858 AC2: stage-by-stage status derived from the
// per-callback outcomes, including the callback-free (skipped) and
// required-callback-failure (partial_failure) cases from AC4.
func TestBuildStages(t *testing.T) {
	cases := []struct {
		name       string
		outcomes   map[string]string
		wantReload string
		wantStatus string
	}{
		{"callback-free", map[string]string{}, "skipped", "skipped"},
		{"all ok", map[string]string{"index_reload": "ok", "db_reindex": "ok"}, "ok", "ok"},
		{"required failure", map[string]string{"index_reload": "failed", "db_reindex": "ok"}, "failed", "partial_failure"},
		{"timeout", map[string]string{"index_reload": "timeout"}, "timeout", "partial_failure"},
		{"advisory ok only", map[string]string{"cloudflare_purge": "ok"}, "skipped", "ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := buildStages(tc.outcomes)
			if st.HugoBuild != "ok" {
				t.Errorf("hugo_build = %q, want ok", st.HugoBuild)
			}
			if st.SourceIndexReload != tc.wantReload || st.PublicIndexReload != tc.wantReload {
				t.Errorf("index reload stages = (%q,%q), want %q", st.SourceIndexReload, st.PublicIndexReload, tc.wantReload)
			}
			if st.CallbacksStatus != tc.wantStatus {
				t.Errorf("callbacks_status = %q, want %q", st.CallbacksStatus, tc.wantStatus)
			}
		})
	}
}
