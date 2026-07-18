package oauth

const LegacyScopeAlias = "mcp"

// CanonicalScope maps every deprecated scope string (both the pre-#450
// 4-tier model and the original "mcp" compatibility alias) to the current
// 2-tier canonical form. This must stay permissive for longer than a typical
// deprecation window: already-issued access tokens (up to AccessTokenTTLSeconds
// old) and OAuth clients with a stale cached copy of scopes_supported may
// present these old strings for a while after the #450 migration ships, and
// rejecting them outright would repeat the exact "reader" outage class from
// #448/#449 — a request/token carrying a scope string the server no longer
// recognizes must still resolve, not fail.
func CanonicalScope(scope string) string {
	switch scope {
	case LegacyScopeAlias, "read", "content.read", "reader":
		return "read"
	case "write", "content.write", "site.admin", "site_admin", "siteadmin", "system.admin", "admin", "system_admin", "systemadmin":
		return "write"
	default:
		return scope
	}
}

// IsLegacyScope reports whether scope is a deprecated compatibility alias
// (anything other than the current canonical "read"/"write" strings).
func IsLegacyScope(scope string) bool {
	switch scope {
	case "read", "write":
		return false
	default:
		return CanonicalScope(scope) != scope
	}
}
