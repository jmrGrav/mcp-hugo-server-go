package toolcontract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const ToolResultVersion = buildinfo.SchemaVersion

type ResponseMeta struct {
	GeneratedAt string `json:"generated_at"`
	// ServerVersion is the deployed server build (internal/buildinfo.Version)
	// — what most callers actually want when they say "version".
	ServerVersion string `json:"server_version"`
	// SchemaVersion is the response *shape* version (ToolResultVersion,
	// currently "v1.0.0") — replaces the old root-level `version` field
	// (#454), which was ambiguous: its name suggested the server version,
	// but it actually meant the schema version, while the real server
	// version lived one level down in server_version. Nesting both under
	// meta with unambiguous names removes that confusion instead of leaving
	// two same-named-sounding fields at different levels.
	SchemaVersion string `json:"schema_version"`
}

type ErrorResolution struct {
	Action          string   `json:"action"`
	Parameter       string   `json:"parameter,omitempty"`
	AllowedValues   []string `json:"allowed_values,omitempty"`
	RecommendedTool string   `json:"recommended_tool,omitempty"`
	// RetryAfterSeconds is populated only for rate_limit_exceeded (#466), so
	// an agent can self-regulate pacing instead of inferring a safe retry
	// delay from the tool description alone.
	RetryAfterSeconds *float64 `json:"retry_after_seconds,omitempty"`
}

type ToolError struct {
	Code       string           `json:"code"`
	Message    string           `json:"message"`
	Field      string           `json:"field,omitempty"`
	Retryable  bool             `json:"retryable"`
	Resolution *ErrorResolution `json:"resolution,omitempty"`
}

type ToolResponse[T any] struct {
	Success  bool         `json:"success"`
	Data     T            `json:"data"`
	Errors   []ToolError  `json:"errors"`
	Warnings []string     `json:"warnings"`
	Meta     ResponseMeta `json:"meta"`
	// GeneratedAt duplicates Meta.GeneratedAt at the root for backward
	// compatibility with existing callers; see #454 for why the analogous
	// root-level `version` field (schema-version, easily confused with the
	// server version) was removed instead of kept — that one was ambiguous
	// naming, not just harmless duplication like this one.
	GeneratedAt string `json:"generated_at,omitempty"`
}

type PaginatedResponse[T any] struct {
	Items         []T  `json:"items"`
	Total         int  `json:"total"`
	Limit         int  `json:"limit"`
	Offset        int  `json:"offset"`
	ReturnedCount int  `json:"returned_count"`
	HasMore       bool `json:"has_more"`
	NextOffset    *int `json:"next_offset,omitempty"`
}

type PaginationMeta struct {
	Total         int
	Limit         int
	Offset        int
	ReturnedCount int
	HasMore       bool
	NextOffset    *int
}

func ComputePagination(total, limit, offset, returned int) PaginationMeta {
	m := PaginationMeta{Total: total, Limit: limit, Offset: offset, ReturnedCount: returned}
	if offset+returned < total {
		m.HasMore = true
		next := offset + returned
		m.NextOffset = &next
	}
	return m
}

func NewMeta(serverVersion string, generatedAt time.Time) ResponseMeta {
	return ResponseMeta{
		GeneratedAt:   generatedAt.UTC().Format(time.RFC3339),
		ServerVersion: serverVersion,
		SchemaVersion: ToolResultVersion,
	}
}

func NewError(code, message string) ToolError {
	return ToolError{
		Code:      code,
		Message:   message,
		Retryable: false,
	}
}

func Success[T any](data T, meta ResponseMeta) ToolResponse[T] {
	return ToolResponse[T]{
		Success:     true,
		Data:        data,
		Errors:      []ToolError{},
		Warnings:    []string{},
		Meta:        meta,
		GeneratedAt: meta.GeneratedAt,
	}
}

// RequestContext echoes the caller's normalized input on a failed mutation
// (#455), independent of whether resolution or the write itself succeeded —
// unlike resolved_lang/resolved_source_path (only meaningful post-resolution
// and correctly omitted on error), slug/requested_lang are known from the
// input alone and should never be silently dropped just because the
// handler's own typed Out struct gets discarded on the error path below.
type RequestContext struct {
	Slug          string `json:"slug"`
	RequestedLang string `json:"requested_lang,omitempty"`
}

// errWithRequestContext wraps an error with the request context that was
// known at the point of failure, so WrapTool can recover it after the
// handler's typed Out value is discarded.
type errWithRequestContext struct {
	err error
	ctx RequestContext
}

func (e *errWithRequestContext) Error() string { return e.err.Error() }
func (e *errWithRequestContext) Unwrap() error { return e.err }

// WithRequestContext annotates err with the caller's normalized request
// context so it survives WrapTool's generic error handling and reaches the
// response as request_context. A nil err returns nil.
func WithRequestContext(err error, ctx RequestContext) error {
	if err == nil {
		return nil
	}
	return &errWithRequestContext{err: err, ctx: ctx}
}

func requestContextFrom(err error) *RequestContext {
	var wrapped *errWithRequestContext
	if errors.As(err, &wrapped) {
		return &wrapped.ctx
	}
	return nil
}

func Failure(meta ResponseMeta, errs ...ToolError) ToolResponse[map[string]any] {
	if errs == nil {
		errs = []ToolError{}
	}
	return ToolResponse[map[string]any]{
		Success:     false,
		Data:        map[string]any{},
		Errors:      errs,
		Warnings:    []string{},
		Meta:        meta,
		GeneratedAt: meta.GeneratedAt,
	}
}

func ParseToolError(err error) ToolError {
	if err == nil {
		return NewError("tool_error", "unknown error")
	}
	code, message := splitErrorPrefix(err.Error())
	out := NewError(code, message)

	switch code {
	case "ambiguous_language":
		out.Field = "lang"
		out.Retryable = true
		out.Resolution = &ErrorResolution{
			Action:        "retry_with_parameter",
			Parameter:     "lang",
			AllowedValues: parseAllowedValues(message),
		}
	case "invalid_params":
		if field := missingRequiredField(message); field != "" {
			out.Code = "missing_required_parameter"
			out.Field = field
			out.Retryable = true
			out.Resolution = &ErrorResolution{
				Action:    "retry_with_parameter",
				Parameter: field,
			}
			return out
		}
		// expected_revision's own message ("expected_revision is required
		// for non-dry-run update_page/delete_page") doesn't match
		// missingRequiredField's "X must not be empty" phrasing, but it's
		// still a missing-required-parameter case (#461) — and specifically
		// one where the caller needs a tool recommendation (get_page_for_edit
		// returns the current revision), not just "retry with this field".
		if strings.HasPrefix(message, "expected_revision is required") {
			out.Code = "missing_required_parameter"
			out.Field = "expected_revision"
			out.Retryable = true
			out.Resolution = &ErrorResolution{
				Action:          "retry_with_parameter",
				Parameter:       "expected_revision",
				RecommendedTool: "get_page_for_edit",
			}
			return out
		}
		out.Retryable = true
		out.Resolution = &ErrorResolution{Action: "retry_with_parameter"}
		if field := inferField(message); field != "" {
			out.Field = field
			out.Resolution.Parameter = field
		}
		if allowed := parseAllowedValues(message); len(allowed) > 0 {
			out.Resolution.AllowedValues = allowed
		}
	case "build_in_progress", "rate_limit_exceeded":
		out.Retryable = true
		out.Resolution = &ErrorResolution{Action: "retry_later"}
		if code == "rate_limit_exceeded" {
			if secs := parseRetryAfterSeconds(message); secs != nil {
				out.Resolution.RetryAfterSeconds = secs
			}
		}
	case "revision_conflict":
		out.Field = "expected_revision"
		out.Retryable = true
		// delete_page_asset's own revision_conflict message (#460) names
		// "asset", not a page — get_page_for_edit doesn't return an asset's
		// hash, so recommending it would misguide the caller;
		// list_page_assets is the tool that actually re-supplies
		// expected_sha256/expected_revision for this case.
		recommendedTool := "get_page_for_edit"
		if strings.Contains(message, "asset") {
			recommendedTool = "list_page_assets"
		}
		out.Resolution = &ErrorResolution{
			Action:          "reread_then_retry",
			Parameter:       "expected_revision",
			RecommendedTool: recommendedTool,
		}
	case "asset_referenced":
		// delete_page_asset's guard against deleting an asset still linked
		// from the page body (#460) — retryable via the documented override,
		// not a caller mistake to fix by changing input shape.
		out.Retryable = true
		out.Resolution = &ErrorResolution{
			Action:    "retry_with_parameter",
			Parameter: "force",
		}
	case "content_not_found", "not_found":
		// not_found is update_page/delete_page's own not-indexed message —
		// same recovery shape as content_not_found (#461): the slug the
		// caller named doesn't resolve, so re-searching is the path
		// forward. content_not_public is deliberately NOT included here:
		// it's overloaded across two different meanings in this codebase
		// (a draft the caller's profile can't see vs. a diagnostics
		// sub-feature unavailable to the reader profile), and only the
		// first would actually benefit from "search again" — a single
		// static hint would misguide the second case, so it's left with
		// no resolution rather than a guess.
		out.Resolution = &ErrorResolution{
			Action:          "search_then_retry",
			RecommendedTool: "search_pages",
		}
	case "already_exists":
		// already_exists is also emitted by upload_page_asset ("asset
		// already exists at ..."), where update_page is not the right
		// recommendation — there's no update path for an existing asset by
		// design (assets are never silently overwritten). Only recommend
		// update_page for create_page's own "page already exists" message
		// (#461); otherwise leave the resolution unset rather than guess.
		if strings.Contains(message, "page already exists") {
			out.Resolution = &ErrorResolution{
				Action:          "use_different_tool",
				RecommendedTool: "update_page",
			}
		}
	}

	return out
}

func WrapTool[In, Out any](handler mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		res, out, err := handler(ctx, req, in)
		if err != nil {
			meta := NewMeta(buildinfo.Version, time.Now())
			reqCtx := requestContextFrom(err)
			return ErrorResult(err, meta, reqCtx), errorOutput[Out](meta, ParseToolError(err), reqCtx), nil
		}
		return res, out, nil
	}
}

func ErrorResult(err error, meta ResponseMeta, reqCtx *RequestContext) *mcp.CallToolResult {
	toolErr := ParseToolError(err)
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("%s: %s", toolErr.Code, toolErr.Message)}},
		StructuredContent: failurePayload(meta, toolErr, reqCtx),
		IsError:           true,
	}
}

func errorOutput[Out any](meta ResponseMeta, toolErr ToolError, reqCtx *RequestContext) Out {
	var out Out
	raw, err := json.Marshal(failurePayload(meta, toolErr, reqCtx))
	if err != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

// failurePayload builds the failure envelope, injecting request_context at
// the root of the JSON object (alongside data/errors/meta) when present, so
// it reaches both a structured-envelope caller reading the raw payload and a
// flat-envelope Out struct that declares its own root-level RequestContext
// field with the matching JSON tag.
func failurePayload(meta ResponseMeta, toolErr ToolError, reqCtx *RequestContext) any {
	base := Failure(meta, toolErr)
	if reqCtx == nil {
		return base
	}
	raw, err := json.Marshal(base)
	if err != nil {
		return base
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return base
	}
	m["request_context"] = reqCtx
	return m
}

func splitErrorPrefix(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "tool_error", "unknown error"
	}
	head, tail, ok := strings.Cut(raw, ":")
	if !ok {
		return "tool_error", raw
	}
	head = strings.TrimSpace(head)
	if !isMachineCode(head) {
		return "tool_error", raw
	}
	return head, strings.TrimSpace(tail)
}

func isMachineCode(raw string) bool {
	if raw == "" {
		return false
	}
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func missingRequiredField(message string) string {
	prefixes := []string{"slug", "title", "query", "lang", "body"}
	for _, field := range prefixes {
		if message == field+" must not be empty" {
			return field
		}
	}
	return ""
}

func inferField(message string) string {
	prefixes := []string{"slug", "title", "query", "lang", "type", "style", "accent"}
	for _, field := range prefixes {
		if strings.HasPrefix(message, field+" ") {
			return field
		}
	}
	return ""
}

func parseAllowedValues(message string) []string {
	if _, tail, ok := strings.Cut(message, "available: "); ok {
		return splitValues(strings.TrimSuffix(tail, ")"))
	}
	if _, tail, ok := strings.Cut(message, "must be one of: "); ok {
		if idx := strings.Index(tail, " ("); idx >= 0 {
			tail = tail[:idx]
		}
		return splitValues(tail)
	}
	return nil
}

// parseRetryAfterSeconds extracts the retry_after_seconds=N.N value embedded
// in a rate_limit_exceeded message by rateLimitExceededErr (#466), mirroring
// the parseAllowedValues message-embedding convention above.
func parseRetryAfterSeconds(message string) *float64 {
	_, tail, ok := strings.Cut(message, "retry_after_seconds=")
	if !ok {
		return nil
	}
	tail = strings.TrimSuffix(strings.TrimSpace(tail), ")")
	if idx := strings.IndexAny(tail, ") "); idx >= 0 {
		tail = tail[:idx]
	}
	secs, err := strconv.ParseFloat(tail, 64)
	if err != nil {
		return nil
	}
	return &secs
}

func splitValues(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.Trim(part, `"'`))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
