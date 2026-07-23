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
	GeneratedAt string `json:"generated_at,omitempty"`
	// ReleaseVersion is the deployed server build (internal/buildinfo.Version)
	// — what most callers actually want when they say "version". It already
	// carries the release identity by itself: on a release build it *is*
	// the release tag (e.g. "v1.5.8"); on a mainline build it's
	// "main-<sha>". This field was named ServerVersion/server_version
	// through v1.5.7; renamed here (#563) at explicit maintainer request —
	// same value, same always-populated semantics, just a different name.
	// A separate release_version field existed briefly in v1.5.5 (#550)
	// with different (sometimes-empty) semantics and was removed in v1.5.7
	// (#560); this is a rename of ServerVersion, not a restore of that
	// earlier field's behavior — build_channel=="release" still tells a
	// release build apart from a mainline one.
	ReleaseVersion string `json:"release_version,omitempty"`
	Commit         string `json:"commit,omitempty"`
	BuildChannel   string `json:"build_channel,omitempty"`
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

func NewMeta(releaseVersion string, generatedAt time.Time) ResponseMeta {
	return ResponseMeta{
		GeneratedAt:    generatedAt.UTC().Format(time.RFC3339),
		ReleaseVersion: releaseVersion,
		Commit:         buildinfo.Commit,
		BuildChannel:   buildinfo.EffectiveBuildChannel(),
		SchemaVersion:  ToolResultVersion,
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

// compactMetaMap trims response_mode=compact's meta object down to only the
// release-identity fields, which are static per-process values (not
// per-request work) with no payload-size justification for trimming (#567).
// Through v1.5.9, compact mode also dropped release_version/commit/
// build_channel, keeping only schema_version — three independent live
// audits flagged that as confusing (an agent in compact mode couldn't tell
// which server build answered it), so as of this change compact only
// narrows data/row-level payload, never meta's release-identity fields.
// meta.generated_at is intentionally still dropped here: the root-level
// generated_at compatibility field (see ShapeSuccessOutput) already carries
// that value in compact mode. This is an explicit field whitelist, not a
// "keep everything except generated_at" rule — a future new ResponseMeta
// field silently disappears in compact mode unless it's added here too.
func compactMetaMap(meta map[string]any) map[string]any {
	return map[string]any{
		"schema_version":  meta["schema_version"],
		"release_version": meta["release_version"],
		"commit":          meta["commit"],
		"build_channel":   meta["build_channel"],
	}
}

// ShapeSuccessOutput trims the success-envelope meta object for compact mode
// while preserving the root generated_at compatibility field. It is a
// JSON-roundtrip helper so every typed Out struct embedding ToolResponse[T]
// can be shaped uniformly without per-tool boilerplate.
func ShapeSuccessOutput[Out any](out Out, mode ResponseMode) Out {
	if mode != ResponseModeCompact {
		return out
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return out
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return out
	}
	meta, ok := m["meta"].(map[string]any)
	if !ok || meta == nil {
		return out
	}
	m["meta"] = compactMetaMap(meta)
	raw, err = json.Marshal(m)
	if err != nil {
		return out
	}
	var shaped Out
	if err := json.Unmarshal(raw, &shaped); err != nil {
		return out
	}
	return shaped
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

// errWithRootFields carries additive root-level fields that should survive a
// tool error path instead of falling back to zero values in the typed Out
// struct. Used for machine-meaningful telemetry like rate_limit_remaining,
// where omitting the field is preferable to silently emitting Go's zero value.
type errWithRootFields struct {
	err    error
	fields map[string]any
}

func (e *errWithRootFields) Error() string { return e.err.Error() }
func (e *errWithRootFields) Unwrap() error { return e.err }

// WithRootFields annotates err with root-level fields that must be injected
// into the structured failure payload. Existing fields are copied so callers
// can safely pass ephemeral maps.
func WithRootFields(err error, fields map[string]any) error {
	if err == nil || len(fields) == 0 {
		return err
	}
	cloned := make(map[string]any, len(fields))
	for k, v := range fields {
		cloned[k] = v
	}
	return &errWithRootFields{err: err, fields: cloned}
}

func rootFieldsFrom(err error) map[string]any {
	var wrapped *errWithRootFields
	if !errors.As(err, &wrapped) || len(wrapped.fields) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(wrapped.fields))
	for k, v := range wrapped.fields {
		cloned[k] = v
	}
	return cloned
}

// errWithDataFields carries additive fields that must also be injected into
// the canonical nested `data` object on failure paths. This exists because
// some v1.x compatibility fields (for example rate_limit_remaining on write
// tool errors, #522) currently need to live in both places: nested under
// `data` for the canonical contract, and at the root for older clients.
type errWithDataFields struct {
	err    error
	fields map[string]any
}

func (e *errWithDataFields) Error() string { return e.err.Error() }
func (e *errWithDataFields) Unwrap() error { return e.err }

// WithDataFields annotates err with fields that must be injected into the
// nested failure `data` object. Existing fields are copied so callers can
// safely pass ephemeral maps.
func WithDataFields(err error, fields map[string]any) error {
	if err == nil || len(fields) == 0 {
		return err
	}
	cloned := make(map[string]any, len(fields))
	for k, v := range fields {
		cloned[k] = v
	}
	return &errWithDataFields{err: err, fields: cloned}
}

func dataFieldsFrom(err error) map[string]any {
	var wrapped *errWithDataFields
	if !errors.As(err, &wrapped) || len(wrapped.fields) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(wrapped.fields))
	for k, v := range wrapped.fields {
		cloned[k] = v
	}
	return cloned
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
		mode, hasMode, err := ResponseModeFromInput(in)
		if err != nil {
			meta := NewMeta(buildinfo.Version, time.Now())
			return ErrorResult(err, meta, nil, nil, nil), errorOutput[Out](meta, ParseToolError(err), nil, nil, nil), nil
		}
		res, out, err := handler(ctx, req, in)
		if err != nil {
			meta := NewMeta(buildinfo.Version, time.Now())
			reqCtx := requestContextFrom(err)
			rootFields := rootFieldsFrom(err)
			dataFields := dataFieldsFrom(err)
			return ErrorResult(err, meta, reqCtx, rootFields, dataFields), errorOutput[Out](meta, ParseToolError(err), reqCtx, rootFields, dataFields), nil
		}
		if hasMode {
			out = ShapeSuccessOutput(out, mode)
		}
		return res, out, nil
	}
}

func ErrorResult(err error, meta ResponseMeta, reqCtx *RequestContext, rootFields, dataFields map[string]any) *mcp.CallToolResult {
	toolErr := ParseToolError(err)
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("%s: %s", toolErr.Code, toolErr.Message)}},
		StructuredContent: failurePayload(meta, toolErr, reqCtx, rootFields, dataFields),
		IsError:           true,
	}
}

func errorOutput[Out any](meta ResponseMeta, toolErr ToolError, reqCtx *RequestContext, rootFields, dataFields map[string]any) Out {
	var out Out
	raw, err := json.Marshal(failurePayload(meta, toolErr, reqCtx, rootFields, dataFields))
	if err != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

// failurePayload builds the failure envelope, injecting additive root-level
// fields (request_context, rate_limit_remaining, etc.) and additive nested
// `data` fields when present, so they reach both raw structured callers and
// typed Out structs with matching JSON tags. Root fields intentionally remain
// additive: they must not overwrite the envelope's core
// success/data/errors/warnings/meta shape.
func failurePayload(meta ResponseMeta, toolErr ToolError, reqCtx *RequestContext, rootFields, dataFields map[string]any) any {
	base := Failure(meta, toolErr)
	return failurePayloadWithData(meta, toolErr, reqCtx, rootFields, dataFields, base)
}

func failurePayloadWithData(meta ResponseMeta, toolErr ToolError, reqCtx *RequestContext, rootFields, dataFields map[string]any, base ToolResponse[map[string]any]) any {
	if reqCtx == nil && len(rootFields) == 0 && len(dataFields) == 0 {
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
	if reqCtx != nil {
		m["request_context"] = reqCtx
	}
	if len(dataFields) > 0 {
		data, ok := m["data"].(map[string]any)
		if !ok || data == nil {
			data = map[string]any{}
			m["data"] = data
		}
		for key, value := range dataFields {
			data[key] = value
		}
	}
	for key, value := range rootFields {
		switch key {
		case "success", "data", "errors", "warnings", "meta":
			continue
		default:
			m[key] = value
		}
	}
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
