package oauth

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
)

// normalizeConfiguredScope maps a single space-delimited scope token (from
// config, a client's requested scope, or an /authorize request) to a
// canonical internal scope: "read" or "write". All legacy scope strings
// (the pre-#450 4-tier model, plus the original "mcp" alias) are resolved
// via CanonicalScope, the single source of truth for scope aliasing.
func normalizeConfiguredScope(raw string) (string, error) {
	switch CanonicalScope(strings.TrimSpace(raw)) {
	case "", "read":
		return "read", nil
	case "write":
		return "write", nil
	default:
		return "", fmt.Errorf("invalid scope %q", raw)
	}
}

func normalizeConfiguredScopes(scopes []string, singular string) ([]string, error) {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(scopes)+1)

	add := func(raw string) error {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil
		}
		scope, err := normalizeConfiguredScope(raw)
		if err != nil {
			return err
		}
		if _, ok := seen[scope]; ok {
			return nil
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
		return nil
	}

	for _, raw := range scopes {
		if err := add(raw); err != nil {
			return nil, err
		}
	}
	if err := add(singular); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		out = append(out, "read")
	}
	sort.Slice(out, func(i, j int) bool {
		return tools.ScopeRank(out[i]) < tools.ScopeRank(out[j])
	})
	return out, nil
}

func highestConfiguredScope(scopes []string) string {
	if len(scopes) == 0 {
		return "read"
	}
	highest := scopes[0]
	highestRank := tools.ScopeRank(highest)
	for _, scope := range scopes[1:] {
		if rank := tools.ScopeRank(scope); rank > highestRank {
			highest = scope
			highestRank = rank
		}
	}
	if highest == "" {
		return "read"
	}
	return highest
}

// requestedScope resolves a space-delimited scope string (from an /authorize
// or /token request) to the single highest-ranked recognized scope. Per RFC
// 6749 §3.3, an authorization server MAY ignore scope values it doesn't
// recognize rather than rejecting the whole request: a token that fails
// normalizeConfiguredScope is skipped, not fatal, so a request mixing valid
// and not-yet-recognized tokens still resolves using the valid ones. Only
// erroring when every single token is unrecognized avoids repeating the
// 2026-07-18 "reader" outage class for the next value scopes_supported gains
// before this switch is updated to match it (see normalizeConfiguredScope's
// doc comment for that incident).
func requestedScope(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return "", nil
	}
	highest := ""
	highestRank := -1
	for _, part := range parts {
		scope, err := normalizeConfiguredScope(part)
		if err != nil {
			continue
		}
		if rank := tools.ScopeRank(scope); rank > highestRank {
			highest = scope
			highestRank = rank
		}
	}
	if highestRank < 0 {
		return "", fmt.Errorf("invalid_scope: no recognized scope token in %q", raw)
	}
	return highest, nil
}

func allowedScope(scope string, allowedMax string) bool {
	return tools.ScopeRank(scope) <= tools.ScopeRank(allowedMax)
}
