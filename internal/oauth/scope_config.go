package oauth

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
)

func normalizeConfiguredScope(raw string) (string, error) {
	switch CanonicalScope(strings.TrimSpace(raw)) {
	case "", "content.read", "read":
		return "content.read", nil
	case "content.write", "write":
		return "content.write", nil
	case "site.admin", "site_admin", "siteadmin":
		return "site.admin", nil
	case "system.admin", "admin", "system_admin", "systemadmin":
		return "system.admin", nil
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
		out = append(out, "content.read")
	}
	sort.Slice(out, func(i, j int) bool {
		return tools.ScopeRank(out[i]) < tools.ScopeRank(out[j])
	})
	return out, nil
}

func highestConfiguredScope(scopes []string) string {
	if len(scopes) == 0 {
		return "content.read"
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
		return "content.read"
	}
	return highest
}

func requestedScope(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return "", nil
	}
	highest := "content.read"
	highestRank := 0
	for _, part := range parts {
		scope, err := normalizeConfiguredScope(part)
		if err != nil {
			return "", err
		}
		if rank := tools.ScopeRank(scope); rank > highestRank {
			highest = scope
			highestRank = rank
		}
	}
	return highest, nil
}

func allowedScope(scope string, allowedMax string) bool {
	return tools.ScopeRank(scope) <= tools.ScopeRank(allowedMax)
}
