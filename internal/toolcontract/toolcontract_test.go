package toolcontract

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewError(t *testing.T) {
	got := NewError("ambiguous_language", "Multiple language variants exist.")
	if got.Code != "ambiguous_language" {
		t.Fatalf("NewError().Code = %q", got.Code)
	}
	if got.Message != "Multiple language variants exist." {
		t.Fatalf("NewError().Message = %q", got.Message)
	}
	if got.Retryable {
		t.Fatal("NewError().Retryable = true, want false")
	}
}

func TestSuccessInitializesSlicesAndMeta(t *testing.T) {
	meta := NewMeta("1.4.0", time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC))
	got := Success(map[string]string{"status": "ok"}, meta)

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded["success"] != true {
		t.Fatalf("success = %v, want true", decoded["success"])
	}
	if _, ok := decoded["errors"].([]any); !ok {
		t.Fatalf("errors = %#v, want []", decoded["errors"])
	}
	if _, ok := decoded["warnings"].([]any); !ok {
		t.Fatalf("warnings = %#v, want []", decoded["warnings"])
	}
	metaMap, ok := decoded["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta = %T, want map", decoded["meta"])
	}
	if metaMap["server_version"] != "1.4.0" {
		t.Fatalf("meta.server_version = %v, want 1.4.0", metaMap["server_version"])
	}
	if metaMap["schema_version"] != ToolResultVersion {
		t.Fatalf("meta.schema_version = %v, want %q", metaMap["schema_version"], ToolResultVersion)
	}
	if _, ok := decoded["version"]; ok {
		t.Fatalf("root-level version should be removed (#454), got %v", decoded["version"])
	}
}

func TestParseToolErrorAmbiguousLanguage(t *testing.T) {
	got := ParseToolError(fmt.Errorf("ambiguous_language: page %q has multiple language files; specify lang (available: en, fr)", "posts/hello"))
	if got.Code != "ambiguous_language" {
		t.Fatalf("Code = %q", got.Code)
	}
	if got.Field != "lang" || !got.Retryable {
		t.Fatalf("Field/Retryable = %q/%v", got.Field, got.Retryable)
	}
	if got.Resolution == nil || got.Resolution.Action != "retry_with_parameter" || got.Resolution.Parameter != "lang" {
		t.Fatalf("Resolution = %#v", got.Resolution)
	}
	if len(got.Resolution.AllowedValues) != 2 || got.Resolution.AllowedValues[0] != "en" || got.Resolution.AllowedValues[1] != "fr" {
		t.Fatalf("AllowedValues = %#v", got.Resolution.AllowedValues)
	}
}

func TestParseToolErrorMissingRequiredParameter(t *testing.T) {
	got := ParseToolError(fmt.Errorf("invalid_params: slug must not be empty"))
	if got.Code != "missing_required_parameter" {
		t.Fatalf("Code = %q, want missing_required_parameter", got.Code)
	}
	if got.Field != "slug" || !got.Retryable {
		t.Fatalf("Field/Retryable = %q/%v", got.Field, got.Retryable)
	}
	if got.Resolution == nil || got.Resolution.Parameter != "slug" {
		t.Fatalf("Resolution = %#v", got.Resolution)
	}
}

func TestParseToolErrorRevisionConflict(t *testing.T) {
	got := ParseToolError(fmt.Errorf("revision_conflict: page changed since it was read; read the latest revision and replan"))
	if got.Code != "revision_conflict" {
		t.Fatalf("Code = %q", got.Code)
	}
	if got.Field != "expected_revision" || !got.Retryable {
		t.Fatalf("Field/Retryable = %q/%v", got.Field, got.Retryable)
	}
	if got.Resolution == nil || got.Resolution.Action != "reread_then_retry" || got.Resolution.Parameter != "expected_revision" {
		t.Fatalf("Resolution = %#v", got.Resolution)
	}
	if got.Resolution.RecommendedTool != "get_page_for_edit" {
		t.Fatalf("RecommendedTool = %q, want get_page_for_edit", got.Resolution.RecommendedTool)
	}
}

// TestParseToolErrorRevisionConflictAssetRecommendsListPageAssets is a
// regression test for #460: delete_page_asset's own revision_conflict
// message names "asset", not a page, so get_page_for_edit (which doesn't
// return an asset's hash) would misguide the caller — list_page_assets is
// the tool that actually re-supplies expected_sha256/expected_revision.
func TestParseToolErrorRevisionConflictAssetRecommendsListPageAssets(t *testing.T) {
	got := ParseToolError(fmt.Errorf("revision_conflict: asset changed since it was read; call list_page_assets to get the current hash and retry"))
	if got.Resolution == nil || got.Resolution.RecommendedTool != "list_page_assets" {
		t.Fatalf("Resolution = %#v, want RecommendedTool=list_page_assets", got.Resolution)
	}
}

// TestParseToolErrorAssetReferencedRecommendsForce is a regression test for
// #460: delete_page_asset's asset_referenced guard is retryable via the
// documented force=true override, not a caller mistake to fix by changing
// input shape.
func TestParseToolErrorAssetReferencedRecommendsForce(t *testing.T) {
	got := ParseToolError(fmt.Errorf("asset_referenced: %q is referenced in %v; pass force=true to delete anyway", "cover.png", []string{"index.md"}))
	if !got.Retryable {
		t.Fatal("Retryable = false, want true")
	}
	if got.Resolution == nil || got.Resolution.Action != "retry_with_parameter" || got.Resolution.Parameter != "force" {
		t.Fatalf("Resolution = %#v, want retry_with_parameter on force", got.Resolution)
	}
}

func TestParseToolErrorContentNotFound(t *testing.T) {
	got := ParseToolError(fmt.Errorf("content_not_found: no source or public page found for slug %q", "posts/gone"))
	if got.Code != "content_not_found" {
		t.Fatalf("Code = %q", got.Code)
	}
	if got.Resolution == nil || got.Resolution.RecommendedTool != "search_pages" {
		t.Fatalf("Resolution = %#v", got.Resolution)
	}
}

// TestParseToolErrorMissingExpectedRevision is a regression test for #461's
// concrete acceptance criterion: expected_revision's own message shape
// ("expected_revision is required for non-dry-run update_page") doesn't
// match missingRequiredField's generic "X must not be empty" phrasing, so it
// needs its own branch — verified against the exact string update_page/
// delete_page actually emit, not a synthetic one.
func TestParseToolErrorMissingExpectedRevision(t *testing.T) {
	for _, msg := range []string{
		"invalid_params: expected_revision is required for non-dry-run update_page",
		"invalid_params: expected_revision is required for non-dry-run delete_page",
	} {
		got := ParseToolError(fmt.Errorf("%s", msg))
		if got.Code != "missing_required_parameter" {
			t.Fatalf("%s: Code = %q, want missing_required_parameter", msg, got.Code)
		}
		if got.Field != "expected_revision" || !got.Retryable {
			t.Fatalf("%s: Field/Retryable = %q/%v", msg, got.Field, got.Retryable)
		}
		if got.Resolution == nil || got.Resolution.RecommendedTool != "get_page_for_edit" {
			t.Fatalf("%s: Resolution = %#v, want RecommendedTool=get_page_for_edit", msg, got.Resolution)
		}
	}
}

func TestParseToolErrorNotFoundMatchesContentNotFound(t *testing.T) {
	got := ParseToolError(fmt.Errorf("not_found: page not found for slug %q", "posts/gone"))
	if got.Code != "not_found" {
		t.Fatalf("Code = %q", got.Code)
	}
	if got.Resolution == nil || got.Resolution.Action != "search_then_retry" || got.Resolution.RecommendedTool != "search_pages" {
		t.Fatalf("Resolution = %#v", got.Resolution)
	}
}

// TestParseToolErrorContentNotPublicHasNoResolution pins the deliberate
// non-hint: content_not_public is overloaded across a draft-visibility case
// and a diagnostics-unavailable case, and only the first would benefit from
// "search again" — a single static hint would misguide the second, so #461
// leaves it with no resolution rather than guessing.
func TestParseToolErrorContentNotPublicHasNoResolution(t *testing.T) {
	got := ParseToolError(fmt.Errorf("content_not_public: reader profile cannot access source validation diagnostics"))
	if got.Resolution != nil {
		t.Fatalf("Resolution = %#v, want nil (deliberately not hinted, see #461)", got.Resolution)
	}
}

func TestParseToolErrorAlreadyExistsRecommendsUpdatePage(t *testing.T) {
	got := ParseToolError(fmt.Errorf("already_exists: page already exists at slug %q", "posts/dup"))
	if got.Resolution == nil || got.Resolution.RecommendedTool != "update_page" {
		t.Fatalf("Resolution = %#v, want RecommendedTool=update_page", got.Resolution)
	}
}

// TestParseToolErrorAlreadyExistsAssetHasNoResolution pins the deliberate
// non-hint for upload_page_asset's own already_exists message: there's no
// "update an existing asset" tool, so recommending update_page would
// misguide the caller (#461).
func TestParseToolErrorAlreadyExistsAssetHasNoResolution(t *testing.T) {
	got := ParseToolError(fmt.Errorf("already_exists: asset already exists at %q", "hero.png"))
	if got.Resolution != nil {
		t.Fatalf("Resolution = %#v, want nil (no update path for an existing asset)", got.Resolution)
	}
}

// TestParseToolErrorRateLimitExceededIncludesRetryAfterSeconds is a
// regression test for #466: rateLimitExceededErr's embedded
// retry_after_seconds must be parsed into the structured resolution, not
// just left in the free-text message.
func TestParseToolErrorRateLimitExceededIncludesRetryAfterSeconds(t *testing.T) {
	got := ParseToolError(fmt.Errorf("rate_limit_exceeded: delete_page is limited to 5 per minute (retry_after_seconds=3.2)"))
	if got.Resolution == nil {
		t.Fatal("Resolution = nil, want present")
	}
	if got.Resolution.Action != "retry_later" {
		t.Fatalf("Resolution.Action = %q, want retry_later", got.Resolution.Action)
	}
	if got.Resolution.RetryAfterSeconds == nil || *got.Resolution.RetryAfterSeconds != 3.2 {
		t.Fatalf("Resolution.RetryAfterSeconds = %#v, want 3.2", got.Resolution.RetryAfterSeconds)
	}
}

// TestParseToolErrorBuildInProgressHasNoRetryAfterSeconds pins that
// build_in_progress (which shares the "retry_later" action but has no
// numeric retry hint) doesn't spuriously get a retry_after_seconds value.
func TestParseToolErrorBuildInProgressHasNoRetryAfterSeconds(t *testing.T) {
	got := ParseToolError(fmt.Errorf("build_in_progress: content lock is held, retry in a moment"))
	if got.Resolution == nil || got.Resolution.Action != "retry_later" {
		t.Fatalf("Resolution = %#v, want retry_later action", got.Resolution)
	}
	if got.Resolution.RetryAfterSeconds != nil {
		t.Fatalf("Resolution.RetryAfterSeconds = %#v, want nil", got.Resolution.RetryAfterSeconds)
	}
}

func TestParseToolErrorRejectsNonMachinePrefix(t *testing.T) {
	got := ParseToolError(fmt.Errorf("unexpected content-type: text/html"))
	if got.Code != "tool_error" {
		t.Fatalf("Code = %q, want tool_error", got.Code)
	}
	if got.Message != "unexpected content-type: text/html" {
		t.Fatalf("Message = %q", got.Message)
	}
}

func TestErrorResultPopulatesStructuredContent(t *testing.T) {
	meta := NewMeta("1.4.0", time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC))
	res := ErrorResult(fmt.Errorf("invalid_params: slug must not be empty"), meta, nil)
	if res == nil {
		t.Fatal("ErrorResult() = nil")
	}
	if !res.IsError {
		t.Fatal("IsError = false, want true")
	}
	if res.StructuredContent == nil {
		t.Fatal("StructuredContent = nil, want structured error envelope")
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal StructuredContent: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal StructuredContent: %v", err)
	}
	if got := decoded["success"]; got != false {
		t.Fatalf("structured success = %v, want false", got)
	}
	errors, ok := decoded["errors"].([]any)
	if !ok || len(errors) == 0 {
		t.Fatalf("structured errors = %#v, want non-empty []any", decoded["errors"])
	}
	first, ok := errors[0].(map[string]any)
	if !ok {
		t.Fatalf("structured errors[0] type = %T", errors[0])
	}
	if got := first["code"]; got != "missing_required_parameter" {
		t.Fatalf("structured errors[0].code = %v, want missing_required_parameter", got)
	}
}

func TestErrorResultUsesHumanReadableTextContent(t *testing.T) {
	meta := NewMeta("1.4.0", time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC))
	res := ErrorResult(fmt.Errorf("not_found: page not found"), meta, nil)
	if len(res.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(res.Content))
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] type = %T, want *mcp.TextContent", res.Content[0])
	}
	if text.Text != "not_found: page not found" {
		t.Fatalf("text = %q, want concise human-readable error", text.Text)
	}
	if json.Valid([]byte(text.Text)) {
		t.Fatalf("text = %q, want human-readable text not serialized JSON blob", text.Text)
	}
}
