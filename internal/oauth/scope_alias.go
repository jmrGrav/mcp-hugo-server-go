package oauth

const LegacyScopeAlias = "mcp"

// TODO(v2): remove LegacyScopeAlias after two stable releases once the
// legacy_scope_requests_total counter stays at 0 in production.
// CanonicalScope maps deprecated compatibility aliases to their canonical scope.
// Discovery never advertises aliases; this is only for auth-time normalization.
func CanonicalScope(scope string) string {
	switch scope {
	case LegacyScopeAlias:
		return "content.read"
	default:
		return scope
	}
}

// IsLegacyScope reports whether scope is a deprecated compatibility alias.
func IsLegacyScope(scope string) bool {
	return scope == LegacyScopeAlias
}
