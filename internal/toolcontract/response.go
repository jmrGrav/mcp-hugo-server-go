package toolcontract

import "time"

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
	Success  bool         `json:"success"`
	Data     T            `json:"data"`
	Errors   []ToolError  `json:"errors"`
	Warnings []string     `json:"warnings"`
	Meta     ResponseMeta `json:"meta"`
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
		Success:  true,
		Data:     data,
		Errors:   []ToolError{},
		Warnings: []string{},
		Meta:     meta,
	}
}
