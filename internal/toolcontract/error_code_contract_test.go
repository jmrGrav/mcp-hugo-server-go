package toolcontract

import (
	"fmt"
	"testing"
)

// TestStableErrorCodeContract is the #860 drift-guard: it pins the stable
// machine-readable error code, retryability, and recovery action for the main
// retry and policy branches in one place, so a future change to
// ParseToolError's mapping can't silently reclassify a retryable error as
// terminal (or vice versa) without a test failing. Agents branch on these
// three fields for automated recovery; they are a contract, not incidental.
func TestStableErrorCodeContract(t *testing.T) {
	cases := []struct {
		name          string
		errText       string
		wantCode      string
		wantRetryable bool
		wantAction    string // Resolution.Action; "" means no resolution expected
	}{
		{"ambiguous_language", `ambiguous_language: page has multiple language files; specify lang (available: en, fr)`, "ambiguous_language", true, "retry_with_parameter"},
		{"missing_required", `invalid_params: slug must not be empty`, "missing_required_parameter", true, "retry_with_parameter"},
		{"missing_expected_revision", `invalid_params: expected_revision is required for non-dry-run update_page`, "missing_required_parameter", true, "retry_with_parameter"},
		{"rate_limit_exceeded", `rate_limit_exceeded: create_page is limited to 60 per minute`, "rate_limit_exceeded", true, "retry_later"},
		{"build_in_progress", `build_in_progress: a build is already running`, "build_in_progress", true, "retry_later"},
		// #1001: apply_content_plan/apply_bundle_plan no longer consume the
		// plan on a lock timeout, so every build_in_progress message is now
		// this neutral, plainly-retryable shape — there is no longer a
		// "consumed plan, must replan" build_in_progress variant.
		{"build_in_progress_plan_preserved", `build_in_progress: content lock is held, retry in a moment`, "build_in_progress", true, "retry_later"},
		{"revision_conflict", `revision_conflict: page changed since it was read; read the latest revision and replan`, "revision_conflict", true, "reread_then_retry"},
		// #1001: apply_content_plan's own revision_conflict/apply_bundle_plan's
		// bundle_conflict don't accept expected_revision — they must recommend
		// replanning via the plan tool, not the generic reread_then_retry.
		{"revision_conflict_plan", `revision_conflict: page changed since the plan was created; call plan_content_change again`, "revision_conflict", true, "replan_then_retry"},
		{"bundle_conflict_plan", `bundle_conflict: bundle changed since the plan was created; call plan_bundle_change again`, "bundle_conflict", true, "replan_then_retry"},
		{"asset_referenced", `asset_referenced: asset is still linked from the page body`, "asset_referenced", true, "retry_with_parameter"},
		{"content_not_found", `content_not_found: page not found for slug "posts/x"`, "content_not_found", false, "search_then_retry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseToolError(fmt.Errorf("%s", tc.errText))
			if got.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", got.Code, tc.wantCode)
			}
			if got.Retryable != tc.wantRetryable {
				t.Errorf("retryable = %v, want %v", got.Retryable, tc.wantRetryable)
			}
			if tc.wantAction == "" {
				if got.Resolution != nil {
					t.Errorf("resolution = %+v, want nil", got.Resolution)
				}
				return
			}
			if got.Resolution == nil {
				t.Fatalf("resolution = nil, want action %q", tc.wantAction)
			}
			if got.Resolution.Action != tc.wantAction {
				t.Errorf("resolution.action = %q, want %q", got.Resolution.Action, tc.wantAction)
			}
		})
	}
}
