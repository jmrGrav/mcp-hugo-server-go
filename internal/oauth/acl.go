package oauth

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
)

// ScopePolicy enforces per-tool scope requirements for all MCP requests.
// It is the single source of truth for tools/call authorization; the registry
// is populated at startup from each tool package's Defs() function.
type ScopePolicy struct {
	reg *tools.Registry
}

// NewScopePolicy returns a ScopePolicy backed by reg.
func NewScopePolicy(reg *tools.Registry) *ScopePolicy {
	return &ScopePolicy{reg: reg}
}

type rpcEnvelope struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcParams struct {
	Name string `json:"name"`
}

func (p *ScopePolicy) checkOne(env rpcEnvelope, callerScope string) (allow bool, reason string) {
	if env.Method != "tools/call" {
		return true, ""
	}
	var params rpcParams
	if err := json.Unmarshal(env.Params, &params); err != nil || params.Name == "" {
		return false, "unknown_tool"
	}
	requiredScope, known := p.reg.RequiredScopeFor(params.Name)
	if !known {
		return false, "unknown_tool"
	}
	if tools.ScopeRank(callerScope) < tools.ScopeRank(requiredScope) {
		return false, "forbidden_tool"
	}
	return true, ""
}

const maxBatchSize = 50

func (p *ScopePolicy) parse(body []byte) ([]rpcEnvelope, string) {
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return nil, "unknown_tool"
	}
	if body[0] == '[' {
		var batch []rpcEnvelope
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, "unknown_tool"
		}
		if len(batch) > maxBatchSize {
			return nil, fmt.Sprintf("batch_too_large: maximum %d requests", maxBatchSize)
		}
		return batch, ""
	}
	var single rpcEnvelope
	if err := json.Unmarshal(body, &single); err != nil {
		return nil, "unknown_tool"
	}
	return []rpcEnvelope{single}, ""
}

// AllowRequest returns true when all tools/call entries in body are permitted for callerScope.
func (p *ScopePolicy) AllowRequest(body []byte, callerScope string) bool {
	envs, parseReason := p.parse(body)
	if parseReason != "" {
		return false
	}
	for _, env := range envs {
		if allow, _ := p.checkOne(env, callerScope); !allow {
			return false
		}
	}
	return true
}

// DenyReason returns the deny reason for the first blocked request entry, or "".
func (p *ScopePolicy) DenyReason(body []byte, callerScope string) string {
	envs, parseReason := p.parse(body)
	if parseReason != "" {
		return parseReason
	}
	for _, env := range envs {
		if allow, reason := p.checkOne(env, callerScope); !allow {
			return reason
		}
	}
	return ""
}
