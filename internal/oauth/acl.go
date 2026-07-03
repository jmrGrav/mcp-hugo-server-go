package oauth

import (
	"encoding/json"
	"strings"
)

var knownProtectedTools = map[string]bool{
	"get_full_page_markdown":  true,
	"get_page_frontmatter":    true,
	"get_related_content":     true,
	"build_agent_context":     true,
	"export_agent_context":    true,
	"create_page":             true,
	"update_page":             true,
	"delete_page":             true,
	"generate_featured_image": true,
	"build_site":              true,
	"run_post_build_hooks":    true,
	"check_sri_versions":      true,
}

type AnonymousMCPPolicy struct {
	public map[string]bool
}

func NewACLPolicy(publicTools []string) *AnonymousMCPPolicy {
	m := make(map[string]bool, len(publicTools))
	for _, t := range publicTools {
		m[t] = true
	}
	return &AnonymousMCPPolicy{public: m}
}

type rpcEnvelope struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcParams struct {
	Name string `json:"name"`
}

func (p *AnonymousMCPPolicy) checkOne(env rpcEnvelope) (allow bool, reason string) {
	if env.Method != "tools/call" {
		return true, ""
	}
	var params rpcParams
	if err := json.Unmarshal(env.Params, &params); err != nil || params.Name == "" {
		return false, "unknown_tool"
	}
	if p.public[params.Name] {
		return true, ""
	}
	if knownProtectedTools[params.Name] {
		return false, "forbidden_tool"
	}
	return false, "unknown_tool"
}

func (p *AnonymousMCPPolicy) parse(body []byte) ([]rpcEnvelope, bool) {
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return nil, false
	}
	if body[0] == '[' {
		var batch []rpcEnvelope
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, false
		}
		return batch, true
	}
	var single rpcEnvelope
	if err := json.Unmarshal(body, &single); err != nil {
		return nil, false
	}
	return []rpcEnvelope{single}, true
}

func (p *AnonymousMCPPolicy) AllowRequest(body []byte) bool {
	envs, ok := p.parse(body)
	if !ok {
		return false
	}
	for _, env := range envs {
		if allow, _ := p.checkOne(env); !allow {
			return false
		}
	}
	return true
}

func (p *AnonymousMCPPolicy) DenyReason(body []byte) string {
	envs, ok := p.parse(body)
	if !ok {
		return "unknown_tool"
	}
	for _, env := range envs {
		if allow, reason := p.checkOne(env); !allow {
			return reason
		}
	}
	return ""
}
