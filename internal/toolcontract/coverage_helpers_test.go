package toolcontract

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type shapeFixture struct {
	ToolResponse[map[string]any]
}

type responseModeInput struct {
	ResponseMode string
}

type noResponseModeInput struct {
	Limit int
}

func TestComputePagination(t *testing.T) {
	t.Run("no next page", func(t *testing.T) {
		got := ComputePagination(10, 5, 5, 5)
		if got.HasMore || got.NextOffset != nil {
			t.Fatalf("ComputePagination(no next page) = %#v, want HasMore=false and nil NextOffset", got)
		}
	})

	t.Run("exact boundary", func(t *testing.T) {
		got := ComputePagination(10, 10, 0, 10)
		if got.HasMore || got.NextOffset != nil {
			t.Fatalf("ComputePagination(exact boundary) = %#v, want HasMore=false and nil NextOffset", got)
		}
	})

	t.Run("has more", func(t *testing.T) {
		got := ComputePagination(10, 3, 3, 3)
		if !got.HasMore || got.NextOffset == nil || *got.NextOffset != 6 {
			t.Fatalf("ComputePagination(has more) = %#v, want HasMore=true NextOffset=6", got)
		}
	})
}

func TestCompactMetaMap(t *testing.T) {
	meta := map[string]any{
		"schema_version":  "v1.0.0",
		"release_version": "v1.6.3",
		"commit":          "abc123",
		"build_channel":   "release",
		"generated_at":    "2026-07-25T10:00:00Z",
		"extra":           "drop-me",
	}
	got := compactMetaMap(meta)
	if len(got) != 4 {
		t.Fatalf("compactMetaMap() len = %d, want 4: %#v", len(got), got)
	}
	if got["schema_version"] != "v1.0.0" || got["release_version"] != "v1.6.3" || got["commit"] != "abc123" || got["build_channel"] != "release" {
		t.Fatalf("compactMetaMap() = %#v, want release identity fields only", got)
	}
	if _, ok := got["generated_at"]; ok {
		t.Fatalf("compactMetaMap() leaked generated_at: %#v", got)
	}
	if _, ok := got["extra"]; ok {
		t.Fatalf("compactMetaMap() leaked extra field: %#v", got)
	}
}

func TestShapeSuccessOutput(t *testing.T) {
	origCommit := buildinfo.Commit
	origChannel := buildinfo.BuildChannel
	buildinfo.Commit = "abc123"
	buildinfo.BuildChannel = "release"
	t.Cleanup(func() {
		buildinfo.Commit = origCommit
		buildinfo.BuildChannel = origChannel
	})

	meta := NewMeta("v1.6.3", time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC))
	out := shapeFixture{
		ToolResponse: Success(map[string]any{"status": "ok"}, meta),
	}

	t.Run("standard unchanged", func(t *testing.T) {
		got := ShapeSuccessOutput(out, ResponseModeStandard)
		if got.Meta.GeneratedAt == "" || got.GeneratedAt == "" {
			t.Fatalf("ShapeSuccessOutput(standard) stripped generated_at: %#v", got)
		}
	})

	t.Run("compact trims meta only", func(t *testing.T) {
		got := ShapeSuccessOutput(out, ResponseModeCompact)
		if got.GeneratedAt == "" {
			t.Fatalf("ShapeSuccessOutput(compact) removed root generated_at: %#v", got)
		}
		if got.Meta.GeneratedAt != "" {
			t.Fatalf("ShapeSuccessOutput(compact) kept meta.generated_at = %q, want empty", got.Meta.GeneratedAt)
		}
		if got.Meta.ReleaseVersion != "v1.6.3" || got.Meta.Commit != "abc123" || got.Meta.BuildChannel != "release" || got.Meta.SchemaVersion != ToolResultVersion {
			t.Fatalf("ShapeSuccessOutput(compact) = %#v, want compact release identity preserved", got.Meta)
		}
		if got.Data["status"] != "ok" {
			t.Fatalf("ShapeSuccessOutput(compact) changed data = %#v", got.Data)
		}
	})
}

func TestWithRequestContextAndExtraction(t *testing.T) {
	ctx := RequestContext{Slug: "posts/demo", RequestedLang: "fr"}
	baseErr := errors.New("boom")
	wrapped := WithRequestContext(baseErr, ctx)
	if wrapped == nil {
		t.Fatal("WithRequestContext() returned nil")
	}
	got := requestContextFrom(wrapped)
	if got == nil || *got != ctx {
		t.Fatalf("requestContextFrom(wrapped) = %#v, want %#v", got, ctx)
	}
	if requestContextFrom(baseErr) != nil {
		t.Fatalf("requestContextFrom(baseErr) = %#v, want nil", requestContextFrom(baseErr))
	}
	if WithRequestContext(nil, ctx) != nil {
		t.Fatal("WithRequestContext(nil, ctx) should return nil")
	}
}

func TestResponseModeFromInput(t *testing.T) {
	tests := []struct {
		name     string
		in       any
		wantMode ResponseMode
		wantSeen bool
		wantErr  bool
	}{
		{name: "nil", in: nil, wantMode: ResponseModeStandard, wantSeen: false},
		{name: "pointer nil", in: (*responseModeInput)(nil), wantMode: ResponseModeStandard, wantSeen: false},
		{name: "non struct", in: "compact", wantMode: ResponseModeStandard, wantSeen: false},
		{name: "missing field", in: noResponseModeInput{Limit: 5}, wantMode: ResponseModeStandard, wantSeen: false},
		{name: "default empty", in: responseModeInput{}, wantMode: ResponseModeStandard, wantSeen: true},
		{name: "explicit compact", in: responseModeInput{ResponseMode: "compact"}, wantMode: ResponseModeCompact, wantSeen: true},
		{name: "pointer explicit standard", in: &responseModeInput{ResponseMode: "standard"}, wantMode: ResponseModeStandard, wantSeen: true},
		{name: "reserved", in: responseModeInput{ResponseMode: "full"}, wantSeen: true, wantErr: true},
		{name: "invalid", in: responseModeInput{ResponseMode: "bogus"}, wantSeen: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, seen, err := ResponseModeFromInput(tt.in)
			if seen != tt.wantSeen {
				t.Fatalf("ResponseModeFromInput(%s) seen = %v, want %v", tt.name, seen, tt.wantSeen)
			}
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ResponseModeFromInput(%s) err = nil, want error", tt.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResponseModeFromInput(%s) unexpected err = %v", tt.name, err)
			}
			if got != tt.wantMode {
				t.Fatalf("ResponseModeFromInput(%s) = %q, want %q", tt.name, got, tt.wantMode)
			}
		})
	}
}

type wrapFixtureInput struct {
	ResponseMode string `json:"response_mode,omitempty"`
}

type wrapFixtureOutput struct {
	ToolResponse[map[string]any]
	RequestContext     *RequestContext `json:"request_context,omitempty"`
	RateLimitRemaining int             `json:"rate_limit_remaining,omitempty"`
}

func TestWithRootFieldsAndWithDataFieldsCloneValues(t *testing.T) {
	baseErr := errors.New("boom")
	root := map[string]any{"rate_limit_remaining": 59}
	data := map[string]any{"rate_limit_remaining": 59}
	wrapped := WithDataFields(WithRootFields(baseErr, root), data)
	root["rate_limit_remaining"] = 0
	data["rate_limit_remaining"] = 0

	gotRoot := rootFieldsFrom(wrapped)
	if gotRoot["rate_limit_remaining"] != 59 {
		t.Fatalf("rootFieldsFrom() = %#v, want cloned original values", gotRoot)
	}
	gotData := dataFieldsFrom(wrapped)
	if gotData["rate_limit_remaining"] != 59 {
		t.Fatalf("dataFieldsFrom() = %#v, want cloned original values", gotData)
	}
}

func TestWrappedErrorUnwrapsToOriginal(t *testing.T) {
	baseErr := errors.New("boom")
	withCtx := WithRequestContext(baseErr, RequestContext{Slug: "posts/demo"})
	withRoot := WithRootFields(baseErr, map[string]any{"rate_limit_remaining": 3})
	withData := WithDataFields(baseErr, map[string]any{"rate_limit_remaining": 3})

	for _, wrapped := range []error{withCtx, withRoot, withData} {
		if !errors.Is(wrapped, baseErr) {
			t.Fatalf("errors.Is(%T, baseErr) = false, want true", wrapped)
		}
	}
}

func TestErrorParsingHelpers(t *testing.T) {
	if code, msg := splitErrorPrefix("invalid_params: slug must not be empty"); code != "invalid_params" || msg != "slug must not be empty" {
		t.Fatalf("splitErrorPrefix(machine) = (%q, %q)", code, msg)
	}
	if code, msg := splitErrorPrefix("Unexpected runtime explosion"); code != "tool_error" || msg != "Unexpected runtime explosion" {
		t.Fatalf("splitErrorPrefix(non-machine) = (%q, %q)", code, msg)
	}
	if !isMachineCode("revision_conflict") || isMachineCode("revision-conflict") {
		t.Fatal("isMachineCode() did not distinguish allowed machine code format")
	}
	if got := missingRequiredField("body must not be empty"); got != "body" {
		t.Fatalf("missingRequiredField(body) = %q, want body", got)
	}
	if got := missingRequiredField("accent must not be empty"); got != "" {
		t.Fatalf("missingRequiredField(unsupported) = %q, want empty", got)
	}
	if got := inferField("style must be one of: tech, geo"); got != "style" {
		t.Fatalf("inferField(style) = %q, want style", got)
	}
	if got := inferField("uploaded content does not match declared extension \".png\""); got != "filename" {
		t.Fatalf("inferField(mime mismatch) = %q, want filename (#688's inferField fix, already merged)", got)
	}
	if got := parseAllowedValues(`type must be one of: "post", "page" (case-insensitive)`); len(got) != 2 || got[0] != "post" || got[1] != "page" {
		t.Fatalf("parseAllowedValues(one-of) = %#v, want [post page]", got)
	}
	if got := parseAllowedValues(`page "posts/x" has multiple language files; specify lang (available: "en", "fr")`); len(got) != 2 || got[0] != "en" || got[1] != "fr" {
		t.Fatalf("parseAllowedValues(available) = %#v, want [en fr]", got)
	}
	if got := parseAllowedValues("no enum here"); got != nil {
		t.Fatalf("parseAllowedValues(no match) = %#v, want nil", got)
	}
	if got := parseRetryAfterSeconds("rate_limit_exceeded: create_page is limited to 5 per minute (retry_after_seconds=1.5)"); got == nil || *got != 1.5 {
		t.Fatalf("parseRetryAfterSeconds(valid) = %#v, want 1.5", got)
	}
	if got := parseRetryAfterSeconds("rate_limit_exceeded: retry_after_seconds=oops"); got != nil {
		t.Fatalf("parseRetryAfterSeconds(invalid) = %#v, want nil", got)
	}
	if got := splitValues(` "en" , 'fr' , de `); len(got) != 3 || got[0] != "en" || got[1] != "fr" || got[2] != "de" {
		t.Fatalf("splitValues() = %#v, want [en fr de]", got)
	}
}

func TestFailureInitializesCanonicalEnvelope(t *testing.T) {
	meta := NewMeta("v1.6.3", time.Date(2026, 7, 25, 21, 0, 0, 0, time.UTC))
	got := Failure(meta, NewError("invalid_params", "bad slug"))
	if got.Success {
		t.Fatal("Failure().Success = true, want false")
	}
	if got.Data == nil {
		t.Fatal("Failure().Data = nil, want empty map")
	}
	if len(got.Errors) != 1 || got.Errors[0].Code != "invalid_params" {
		t.Fatalf("Failure().Errors = %#v, want one invalid_params error", got.Errors)
	}
	if got.GeneratedAt != meta.GeneratedAt {
		t.Fatalf("Failure().GeneratedAt = %q, want %q", got.GeneratedAt, meta.GeneratedAt)
	}
}

func TestWrapToolShapesCompactSuccess(t *testing.T) {
	origCommit := buildinfo.Commit
	origChannel := buildinfo.BuildChannel
	buildinfo.Commit = "abc123"
	buildinfo.BuildChannel = "release"
	t.Cleanup(func() {
		buildinfo.Commit = origCommit
		buildinfo.BuildChannel = origChannel
	})

	handler := WrapTool(func(ctx context.Context, req *mcp.CallToolRequest, in wrapFixtureInput) (*mcp.CallToolResult, wrapFixtureOutput, error) {
		meta := NewMeta("v1.6.3", time.Date(2026, 7, 25, 21, 5, 0, 0, time.UTC))
		out := wrapFixtureOutput{ToolResponse: Success(map[string]any{"status": "ok"}, meta)}
		return &mcp.CallToolResult{}, out, nil
	})

	_, out, err := handler(context.Background(), &mcp.CallToolRequest{}, wrapFixtureInput{ResponseMode: "compact"})
	if err != nil {
		t.Fatalf("WrapTool() error = %v", err)
	}
	if out.Meta.GeneratedAt != "" {
		t.Fatalf("compact success kept meta.generated_at = %q, want empty", out.Meta.GeneratedAt)
	}
	if out.GeneratedAt == "" {
		t.Fatal("compact success removed root generated_at")
	}
	if out.Meta.ReleaseVersion != "v1.6.3" || out.Meta.Commit != "abc123" || out.Meta.BuildChannel != "release" {
		t.Fatalf("compact success meta = %#v, want release identity fields preserved", out.Meta)
	}
}

func TestWrapToolReturnsStructuredErrorForInvalidResponseMode(t *testing.T) {
	handler := WrapTool(func(ctx context.Context, req *mcp.CallToolRequest, in wrapFixtureInput) (*mcp.CallToolResult, wrapFixtureOutput, error) {
		t.Fatal("handler should not run when response_mode is invalid")
		return nil, wrapFixtureOutput{}, nil
	})

	res, out, err := handler(context.Background(), &mcp.CallToolRequest{}, wrapFixtureInput{ResponseMode: "full"})
	if err != nil {
		t.Fatalf("WrapTool() error = %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("WrapTool() result = %#v, want structured error result", res)
	}
	if out.Success {
		t.Fatal("typed error output reported success=true")
	}
	if len(out.Errors) == 0 || out.Errors[0].Code != "invalid_params" {
		t.Fatalf("typed error output = %#v, want invalid_params", out.Errors)
	}
}

func TestWrapToolInvalidResponseModeErrorSnapshot(t *testing.T) {
	handler := WrapTool(func(ctx context.Context, req *mcp.CallToolRequest, in wrapFixtureInput) (*mcp.CallToolResult, wrapFixtureOutput, error) {
		t.Fatal("handler should not run when response_mode is invalid")
		return nil, wrapFixtureOutput{}, nil
	})

	res, out, err := handler(context.Background(), &mcp.CallToolRequest{}, wrapFixtureInput{ResponseMode: "banana"})
	if err != nil {
		t.Fatalf("WrapTool() error = %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("WrapTool() result = %#v, want structured error result", res)
	}

	if len(out.Errors) != 1 {
		t.Fatalf("typed error output = %#v, want one error", out.Errors)
	}
	err0 := out.Errors[0]
	if err0.Code != "invalid_params" || err0.Resolution == nil || err0.Resolution.Action != "retry_with_parameter" {
		t.Fatalf("typed error snapshot drifted: %#v", err0)
	}
	if err0.Message == "" || !strings.Contains(err0.Message, "response_mode must be one of: standard, compact") {
		t.Fatalf("typed error message = %q, want invalid response_mode guidance", err0.Message)
	}
	if len(err0.Resolution.AllowedValues) != 2 || err0.Resolution.AllowedValues[0] != "standard" || err0.Resolution.AllowedValues[1] != "compact" {
		t.Fatalf("resolution allowed_values = %#v, want [standard compact]", err0.Resolution.AllowedValues)
	}
}

func TestWrapToolCarriesRequestContextAndCompatFieldsOnError(t *testing.T) {
	handler := WrapTool(func(ctx context.Context, req *mcp.CallToolRequest, in wrapFixtureInput) (*mcp.CallToolResult, wrapFixtureOutput, error) {
		err := fmt.Errorf("revision_conflict: page changed since it was read; read the latest revision and replan")
		err = WithRequestContext(err, RequestContext{Slug: "posts/demo", RequestedLang: "fr"})
		err = WithRootFields(err, map[string]any{"rate_limit_remaining": 59})
		err = WithDataFields(err, map[string]any{"rate_limit_remaining": 59})
		return nil, wrapFixtureOutput{}, err
	})

	res, out, err := handler(context.Background(), &mcp.CallToolRequest{}, wrapFixtureInput{})
	if err != nil {
		t.Fatalf("WrapTool() error = %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("WrapTool() result = %#v, want error result", res)
	}
	if out.RequestContext == nil || out.RequestContext.Slug != "posts/demo" || out.RequestContext.RequestedLang != "fr" {
		t.Fatalf("typed error request_context = %#v, want slug/lang preserved", out.RequestContext)
	}
	if out.RateLimitRemaining != 59 {
		t.Fatalf("typed error rate_limit_remaining = %d, want 59", out.RateLimitRemaining)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, "revision_conflict") {
		t.Fatalf("error content = %#v, want human-readable revision_conflict text", res.Content)
	}
}
