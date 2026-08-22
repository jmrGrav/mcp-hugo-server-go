#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${1:-${BASE_URL:-https://mcp.arleo.eu}}"
WWW_URL="${WWW_URL:-https://www.arleo.eu}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required tool: $1" >&2
    exit 1
  }
}

need curl
need jq

json_get() {
  local url="$1"
  curl -fsS "$url"
}

expect_eq() {
  local got="$1"
  local want="$2"
  local label="$3"
  if [[ "$got" != "$want" ]]; then
    echo "$label: got '$got', want '$want'" >&2
    exit 1
  fi
}

expect_contains() {
  local json="$1"
  local needle="$2"
  local label="$3"
  if ! jq -e --arg needle "$needle" 'index($needle) != null' <<<"$json" >/dev/null; then
    echo "$label: missing '$needle'" >&2
    exit 1
  fi
}

expect_not_contains() {
  local json="$1"
  local needle="$2"
  local label="$3"
  if jq -e --arg needle "$needle" 'index($needle) != null' <<<"$json" >/dev/null; then
    echo "$label: unexpected '$needle'" >&2
    exit 1
  fi
}

auth_meta="$(json_get "$BASE_URL/.well-known/oauth-authorization-server")"
resource_meta="$(json_get "$BASE_URL/.well-known/oauth-protected-resource")"
resource_meta_alias="$(json_get "$BASE_URL/.well-known/oauth-protected-resource/mcp")"
server_card="$(json_get "$BASE_URL/.well-known/mcp/server-card.json")"
mcp_alias="$(json_get "$BASE_URL/.well-known/mcp.json")"
auth_md="$(curl -fsS "$WWW_URL/auth.md")"

# www.arleo.eu's own oauth-protected-resource is a DISTINCT document (its
# `resource` names the website, not the MCP endpoint) that has historically
# been a hardcoded literal in the OpenResty vhost rather than derived from
# this server at request time — it silently drifted to the pre-#450 legacy
# scope strings for weeks before the 2026-08-22 regression was caught, and
# nothing in this script would have caught it, because this script never
# checked www's copy at all. See docs/agent-ready-howto.md's invariant table
# and the wiki's "2026-08-22 Regression" entry on AgentReady Pitfalls.
www_resource_meta="$(json_get "$WWW_URL/.well-known/oauth-protected-resource")"
www_ai_catalog="$(json_get "$WWW_URL/.well-known/ai-catalog.json")"
www_agent_skills="$(json_get "$WWW_URL/.well-known/agent-skills/index.json")"

issuer="$(jq -r '.issuer // empty' <<<"$auth_meta")"
auth_endpoint="$(jq -r '.authorization_endpoint // empty' <<<"$auth_meta")"
token_endpoint="$(jq -r '.token_endpoint // empty' <<<"$auth_meta")"
registration_endpoint="$(jq -r '.registration_endpoint // empty' <<<"$auth_meta")"
resource="$(jq -r '.resource // empty' <<<"$resource_meta")"
resource_alias="$(jq -r '.resource // empty' <<<"$resource_meta_alias")"
authorization_servers="$(jq -c '.authorization_servers // []' <<<"$resource_meta")"
transport_endpoint="$(jq -r '.transport.endpoint // empty' <<<"$server_card")"
alias_transport_endpoint="$(jq -r '.transport.endpoint // empty' <<<"$mcp_alias")"

expect_eq "$issuer" "$BASE_URL" "issuer"
expect_eq "$auth_endpoint" "$BASE_URL/authorize" "authorization_endpoint"
expect_eq "$token_endpoint" "$BASE_URL/token" "token_endpoint"
expect_eq "$registration_endpoint" "$BASE_URL/register" "registration_endpoint"
expect_eq "$resource" "$BASE_URL/mcp" "resource"
expect_eq "$resource_alias" "$BASE_URL/mcp" "resource alias"
expect_contains "$authorization_servers" "$BASE_URL" "authorization_servers"
expect_eq "$transport_endpoint" "/mcp" "server_card transport.endpoint"
expect_eq "$alias_transport_endpoint" "/mcp" "mcp alias transport.endpoint"

expect_contains "$(jq -c '.scopes_supported // []' <<<"$auth_meta")" "read" "auth scopes"
expect_contains "$(jq -c '.scopes_supported // []' <<<"$auth_meta")" "write" "auth scopes"
expect_not_contains "$(jq -c '.scopes_supported // []' <<<"$auth_meta")" "site.admin" "auth scopes"
expect_not_contains "$(jq -c '.scopes_supported // []' <<<"$auth_meta")" "system.admin" "auth scopes"

www_resource="$(jq -r '.resource // empty' <<<"$www_resource_meta")"
expect_eq "$www_resource" "$WWW_URL" "www oauth-protected-resource resource"
www_scopes="$(jq -c '.scopes_supported // []' <<<"$www_resource_meta")"
expect_contains "$www_scopes" "read" "www oauth-protected-resource scopes"
expect_contains "$www_scopes" "write" "www oauth-protected-resource scopes"
expect_contains "$www_scopes" "admin" "www oauth-protected-resource scopes"
expect_not_contains "$www_scopes" "site.admin" "www oauth-protected-resource scopes"
expect_not_contains "$www_scopes" "system.admin" "www oauth-protected-resource scopes"
expect_not_contains "$www_scopes" "content.read" "www oauth-protected-resource scopes"
expect_not_contains "$www_scopes" "content.write" "www oauth-protected-resource scopes"

# The two oauth-protected-resource documents share every field except
# `resource` (host-specific by design, see docs/agent-ready-howto.md). Diff
# everything else so a future edit to one copy without the other is caught
# here instead of silently drifting for weeks, which is exactly what
# happened before the 2026-08-22 regression.
resource_scopes="$(jq -c '.scopes_supported // []' <<<"$resource_meta")"
if [[ "$www_scopes" != "$resource_scopes" ]]; then
  echo "scopes_supported drift: www='$www_scopes' mcp='$resource_scopes'" >&2
  exit 1
fi
www_bearer="$(jq -c '.bearer_methods_supported // []' <<<"$www_resource_meta")"
mcp_bearer="$(jq -c '.bearer_methods_supported // []' <<<"$resource_meta")"
if [[ "$www_bearer" != "$mcp_bearer" ]]; then
  echo "bearer_methods_supported drift: www='$www_bearer' mcp='$mcp_bearer'" >&2
  exit 1
fi

ai_catalog_entries="$(jq -r '.entries // [] | length' <<<"$www_ai_catalog")"
if [[ "$ai_catalog_entries" -lt 1 ]]; then
  echo "www ai-catalog.json: entries array is empty or missing" >&2
  exit 1
fi

agent_skills_count="$(jq -r '.skills // [] | length' <<<"$www_agent_skills")"
if [[ "$agent_skills_count" -lt 1 ]]; then
  echo "www agent-skills/index.json: skills array is empty or missing" >&2
  exit 1
fi

for needle in \
  "registration_flow" \
  "registration_endpoint" \
  "$BASE_URL/register" \
  "authorization_endpoint" \
  "$BASE_URL/authorize" \
  "token_endpoint" \
  "$BASE_URL/token" \
  "mcp_endpoint" \
  "$BASE_URL/mcp" \
  "agent_auth_metadata" \
  "credential_types_supported" \
  "urn:ietf:params:oauth:token-type:id-jag" \
  "claim_uri" \
  "identity_assertion"; do
  if ! grep -q "$needle" <<<"$auth_md"; then
    echo "auth.md: missing $needle" >&2
    exit 1
  fi
done

register_status="$(
  curl -sk -o /dev/null -w '%{http_code}' \
    -X POST "$BASE_URL/register" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    --data 'client_name=agent-ready-check'
)"
case "$register_status" in
  400|401|403)
    ;;
  *)
    echo "/register is not behaving like a live registration endpoint (HTTP $register_status)" >&2
    exit 1
    ;;
esac

echo "agent-ready preflight OK"
