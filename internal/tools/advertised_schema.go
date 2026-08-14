package tools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AdvertiseInputEnum decorates tools/list with an enum without making that
// enum part of the SDK's call-time validation schema. The SDK validates a
// tool's InputSchema before the typed handler runs; publishing a finite
// vocabulary there would therefore turn an invalid value into its bare
// schema error instead of the server's structured invalid_params envelope.
//
// The registered tool continues to use its permissive, handler-validated
// schema. Only the returned tools/list copy gains the client-facing enum.
// This is deliberately additive and reusable for any finite vocabulary.
func AdvertiseInputEnum(s *mcp.Server, toolName, field string, values []string) {
	if s == nil || toolName == "" || field == "" || len(values) == 0 {
		return
	}
	enum := append([]string(nil), values...)
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil || method != "tools/list" {
				return result, err
			}
			list, ok := result.(*mcp.ListToolsResult)
			if !ok {
				return result, nil
			}
			for i, tool := range list.Tools {
				if tool == nil || tool.Name != toolName {
					continue
				}
				clone, ok := cloneToolWithInputEnum(tool, field, enum)
				if ok {
					list.Tools[i] = clone
				}
			}
			return list, nil
		}
	})
}

func cloneToolWithInputEnum(tool *mcp.Tool, field string, values []string) (*mcp.Tool, bool) {
	encoded, err := json.Marshal(tool.InputSchema)
	if err != nil {
		return nil, false
	}
	var schema map[string]any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		return nil, false
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil, false
	}
	property, ok := properties[field].(map[string]any)
	if !ok {
		return nil, false
	}
	advertised := make([]any, len(values))
	for i, value := range values {
		advertised[i] = value
	}
	property["enum"] = advertised
	clone := *tool
	clone.InputSchema = schema
	return &clone, true
}
