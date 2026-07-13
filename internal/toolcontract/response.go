package toolcontract

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const ToolResultVersion = "v1.0.0"

type ResponseMeta struct {
	GeneratedAt   string `json:"generated_at"`
	ServerVersion string `json:"server_version"`
}

type ErrorResolution struct {
	Action        string   `json:"action"`
	Parameter     string   `json:"parameter,omitempty"`
	AllowedValues []string `json:"allowed_values,omitempty"`
}

type ToolError struct {
	Code       string           `json:"code"`
	Message    string           `json:"message"`
	Field      string           `json:"field,omitempty"`
	Retryable  bool             `json:"retryable"`
	Resolution *ErrorResolution `json:"resolution,omitempty"`
}

type ToolResponse[T any] struct {
	Success     bool         `json:"success"`
	Data        T            `json:"data"`
	Errors      []ToolError  `json:"errors"`
	Warnings    []string     `json:"warnings"`
	Meta        ResponseMeta `json:"meta"`
	Version     string       `json:"version,omitempty"`
	GeneratedAt string       `json:"generated_at,omitempty"`
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
		Version:     meta.ServerVersion,
		GeneratedAt: meta.GeneratedAt,
	}
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
		Version:     meta.ServerVersion,
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
	}

	return out
}

func WrapTool[In, Out any](handler mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		res, out, err := handler(ctx, req, in)
		if err != nil {
			var zero Out
			return ErrorResult(err, NewMeta(ToolResultVersion, time.Now())), zero, nil
		}
		return res, out, nil
	}
}

func ErrorResult(err error, meta ResponseMeta) *mcp.CallToolResult {
	toolErr := ParseToolError(err)
	payload := Failure(meta, toolErr)
	text := fmt.Sprintf("%s: %s", toolErr.Code, toolErr.Message)
	if raw, marshalErr := json.Marshal(payload); marshalErr == nil {
		text = string(raw)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
		IsError: true,
	}
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
