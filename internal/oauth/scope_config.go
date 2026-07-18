package oauth

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
)

// normalizeConfiguredScope maps a single space-delimited scope token (from
// config, a client's requested scope, or an /authorize request) to a
// canonical internal scope. "reader" must be accepted here: it's part of
// tools.KnownScopes and is published in scopes_supported by
// /.well-known/oauth-authorization-server (see access_profiles.reader),
// and some clients (observed: Claude.ai) echo the full advertised scope
// list back verbatim as their authorize request's scope parameter. Before
// this case existed, any such client's authorize request failed outright
// with invalid_scope on the "reader" token alone — a real production
// outage, not a hypothetical (#reader-scope-outage, 2026-07-18).
//
// "reader" is deliberately kept as its own distinct canonical value, never
// folded into "content.read" (they share ScopeRank 1, but are not the same
// string): site.IsReaderProfile and the request-time reader-safe gate in
// server.go both key on the literal scope string being exactly "reader".
// Collapsing it into "content.read" here would silently upgrade every
// self-service reader client's granted/stored scope out of the reader-safe
// profile the moment it explicitly requested scope=reader, exposing
// source-only/draft content a reader-safe caller must never see.
func normalizeConfiguredScope(raw string) (string, error) {
	switch CanonicalScope(strings.TrimSpace(raw)) {
	case "", "content.read", "read":
		return "content.read", nil
	case "reader":
		return "reader", nil
	case "content.write", "write":
		return "content.write", nil
	case "site.admin", "site_admin", "siteadmin":
		return "site.admin", nil
	case "system.admin", "admin", "system_admin", "systemadmin":
		return "site.admin", nil
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

// highestMatchedScope returns the highest scope among the given list, returning
// "" (anonymous) when the list is empty or no scope is set. Unlike
// highestConfiguredScope this does NOT fall back to "content.read" — it is
// used for DCR scope resolution where an unmatched client should get anonymous
// access, not read access.
func highestMatchedScope(scopes []string) string {
	if len(scopes) == 0 {
		return ""
	}
	highest := scopes[0]
	highestRank := tools.ScopeRank(highest)
	for _, scope := range scopes[1:] {
		if rank := tools.ScopeRank(scope); rank > highestRank {
			highest = scope
			highestRank = rank
		}
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
