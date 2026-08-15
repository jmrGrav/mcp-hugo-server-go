package write

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRecoveryFallbackResultDecodesForEveryJournaledMutationOutput(t *testing.T) {
	cases := []struct {
		tool string
		out  any
	}{
		{"create_page", &createPageOutput{}},
		{"update_page", &updatePageOutput{}},
		{"delete_page", &deletePageOutput{}},
		{"apply_content_plan", &applyContentPlanOutput{}},
		{"rollback_change", &rollbackChangeOutput{}},
		{"create_bundle", &bundleLifecycleOutput{}},
		{"delete_bundle", &bundleLifecycleOutput{}},
		{"apply_bundle_plan", &applyBundlePlanOutput{}},
		{"rollback_bundle", &rollbackBundleOutput{}},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			idem := recoveryIdempotencyFor(context.Background(), tc.tool, "key", "hash", map[string]any{
				"slug": "/posts/recovered/", "source_key": "posts/recovered",
				"before_revision": "sha256:before", "after_revision": "sha256:after", "new_revision": "sha256:after",
				"languages": []string{"fr", "en"}, "revisions": map[string]string{"fr": "sha256:fr", "en": "sha256:en"},
			})
			if idem == nil || len(idem.ResultJSON) == 0 {
				t.Fatal("missing pre-write recovery result")
			}
			if err := json.Unmarshal(idem.ResultJSON, tc.out); err != nil {
				t.Fatalf("fallback does not decode into %T: %v", tc.out, err)
			}
			raw, err := json.Marshal(tc.out)
			if err != nil {
				t.Fatal(err)
			}
			var envelope map[string]any
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope["success"] != true {
				t.Fatalf("fallback success = %v", envelope["success"])
			}
			data, ok := envelope["data"].(map[string]any)
			if !ok || data["status"] == "" || data["status"] == nil {
				t.Fatalf("fallback data = %#v, want action status", envelope["data"])
			}
			if data["slug"] != "/posts/recovered/" {
				t.Fatalf("fallback lost operation identity: %#v", data)
			}
		})
	}
}
