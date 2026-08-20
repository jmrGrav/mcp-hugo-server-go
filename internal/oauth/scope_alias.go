package oauth

const LegacyScopeAlias = "mcp"

// CanonicalScope maps every deprecated scope string (both the pre-#450
// 4-tier model and the original "mcp" compatibility alias) to the current
// canonical form. This must stay permissive for longer than a typical
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
	case "admin", "site.admin", "site_admin", "siteadmin", "system.admin", "system_admin", "systemadmin":
		return "admin"
	case "write", "content.write":
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

// expandScopeForResponse renders scope's tiered grant as the full space-
// delimited set of canonical scope tokens it implies, for the OAuth token
// response body only (RFC 6749 §3.3's "scope" response parameter) — never
// for anything that gets persisted or re-checked. "write" implies "read" in
// this server's rank model (see tools.ScopeRank), but requestedScope()
// collapses a multi-token request like "content.read content.write" down
// to the single highest-rank grant before it's ever stored, so the token
// response echoed back only "write". Some OAuth clients (observed:
// ChatGPT's custom-connector UI) compare the granted scope string against
// every token they originally requested and flag a false "not all
// permissions granted" warning when a subsumed token like "read" is absent
// from the response, even though the higher tier already covers it.
// Expanding the *response* string to list every implied tier fixes that
// display mismatch without touching what's stored: AddAccessToken/
// AddRefreshToken are always called with the original single-token scope,
// and verification (ScopeRank, an exact-match switch) never sees this
// expanded form — it only ever reads back what was actually persisted.
func expandScopeForResponse(scope string) string {
	switch CanonicalScope(scope) {
	case "write":
		return "read write"
	case "admin":
		return "read write admin"
	case "read":
		return "read"
	default:
		return scope
	}
}
