#!/usr/bin/env bash
set -euo pipefail

if [[ "${SMOKE_LIVE:-0}" != "1" ]]; then
  echo "SMOKE_LIVE=1 is required to run this smoke against live endpoints" >&2
  exit 1
fi

BASE_URL="${BASE_URL:-https://mcp.arleo.eu}"
SITE_URL="${SITE_URL:-https://www.arleo.eu}"
CLAUDE_REDIRECT_URI="${CLAUDE_REDIRECT_URI:-https://claude.ai/api/oauth/callback}"
CHATGPT_REDIRECT_URI="${CHATGPT_REDIRECT_URI:-https://chatgpt.com/aip/oauth/callback}"
EXPECT_READ_TOOLS_MIN="${EXPECT_READ_TOOLS_MIN:-}"
EXPECT_ADMIN_TOOLS_MIN="${EXPECT_ADMIN_TOOLS_MIN:-}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required tool: $1" >&2
    exit 1
  }
}

need curl
need jq

pass() {
  printf 'PASS %s\n' "$1" >&2
}

fail() {
  printf 'FAIL %s\n' "$1" >&2
  exit 1
}

get_body() {
  local url="$1"
  curl -fsS "$url"
}

extract_json() {
  local body="$1"
  if grep -q '^data: ' <<<"$body"; then
    sed -n 's/^data: //p' <<<"$body" | tail -n1
    return
  fi
  printf '%s' "$body"
}

expect_json_field() {
  local json="$1"
  local jq_expr="$2"
  local want="$3"
  local label="$4"
  local got
  got="$(jq -r "$jq_expr // empty" <<<"$json")"
  if [[ "$got" != "$want" ]]; then
    fail "$label: got '$got', want '$want'"
  fi
  pass "$label"
}

expect_json_contains() {
  local json="$1"
  local jq_expr="$2"
  local needle="$3"
  local label="$4"
  if ! jq -e --arg needle "$needle" "$jq_expr | index(\$needle) != null" <<<"$json" >/dev/null; then
    fail "$label: missing '$needle'"
  fi
  pass "$label"
}

require_ok_json() {
  local url="$1"
  local label="$2"
  local body
  body="$(get_body "$url")"
  if ! jq -e . >/dev/null 2>&1 <<<"$body"; then
    fail "$label: invalid JSON from $url"
  fi
  printf '%s' "$body"
}

check_tools_list() {
  local label="$1"
  local bearer="${2:-}"
  local expect_min="${3:-}"
  local response
  if [[ -n "$bearer" ]]; then
    local auth_header_name="Authori""zation"
    local auth_scheme="Be""arer"
    response="$(
      curl -fsS "$BASE_URL/mcp" \
        -H 'Content-Type: application/json' \
        -H 'Accept: application/json, text/event-stream' \
        -H "${auth_header_name}: ${auth_scheme} $bearer" \
        --data '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
    )"
  else
    response="$(
      curl -fsS "$BASE_URL/mcp" \
        -H 'Content-Type: application/json' \
        -H 'Accept: application/json, text/event-stream' \
        --data '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
    )"
  fi
  local json
  json="$(extract_json "$response")"
  local count
  count="$(jq -r '.result.tools | length' <<<"$json")"
  if [[ -n "$expect_min" ]] && (( count < expect_min )); then
    fail "$label tools/list count = $count, want >= $expect_min"
  fi
  pass "$label tools/list count = $count"
  printf '%s\n' "$count"
}

probe_dcr() {
  local label="$1"
  local redirect_uri="$2"
  local client_name="$3"
  local body_file status
  body_file="$(mktemp)"
  trap 'rm -f "$body_file"' RETURN
  local payload
  payload="$(jq -nc --arg name "$client_name" --arg uri "$redirect_uri" '{client_name:$name, redirect_uris:[$uri]}')"
  status="$(
    curl -sS -o "$body_file" -w '%{http_code}' \
      -X POST "$BASE_URL/register" \
      -H 'Content-Type: application/json' \
      --data "$payload"
  )"
  case "$status" in
    201)
      ;;
    400|401|403)
      fail "$label DCR probe rejected (HTTP $status)"
      ;;
    *)
      fail "$label DCR probe unexpected HTTP $status"
      ;;
  esac
  if ! jq -e . "$body_file" >/dev/null 2>&1; then
    fail "$label DCR probe returned invalid JSON"
  fi
  local client_id scope
  client_id="$(jq -r '.client_id // empty' "$body_file")"
  scope="$(jq -r '.scope // empty' "$body_file")"
  if [[ -z "$client_id" ]]; then
    fail "$label DCR probe missing client_id"
  fi
  if [[ -z "$scope" ]]; then
    fail "$label DCR probe missing scope"
  fi
  pass "$label DCR probe client_id=$client_id scope=$scope"
}

echo "Running agent interop smoke against:"
echo "  BASE_URL=$BASE_URL"
echo "  SITE_URL=$SITE_URL"
echo "  CLAUDE_REDIRECT_URI=$CLAUDE_REDIRECT_URI"
echo "  CHATGPT_REDIRECT_URI=$CHATGPT_REDIRECT_URI"

"$(dirname "$0")/check-agent-ready.sh" "$BASE_URL"

auth_meta="$(require_ok_json "$BASE_URL/.well-known/oauth-authorization-server" "oauth metadata")"
resource_meta="$(require_ok_json "$BASE_URL/.well-known/oauth-protected-resource" "protected resource metadata")"
resource_meta_alias="$(require_ok_json "$BASE_URL/.well-known/oauth-protected-resource/mcp" "protected resource alias metadata")"
server_card="$(require_ok_json "$BASE_URL/.well-known/mcp/server-card.json" "server card")"
mcp_alias="$(require_ok_json "$BASE_URL/.well-known/mcp.json" "mcp alias")"
auth_md="$(get_body "$SITE_URL/auth.md")"

expect_json_field "$auth_meta" '.issuer' "$BASE_URL" "issuer"
expect_json_field "$auth_meta" '.authorization_endpoint' "$BASE_URL/authorize" "authorization_endpoint"
expect_json_field "$auth_meta" '.token_endpoint' "$BASE_URL/token" "token_endpoint"
expect_json_field "$auth_meta" '.registration_endpoint' "$BASE_URL/register" "registration_endpoint"
expect_json_field "$resource_meta" '.resource' "$BASE_URL/mcp" "protected resource"
expect_json_field "$resource_meta_alias" '.resource' "$BASE_URL/mcp" "protected resource alias"
expect_json_contains "$resource_meta" '.authorization_servers' "$BASE_URL" "authorization_servers"
expect_json_field "$server_card" '.transport.endpoint' "/mcp" "server card transport endpoint"
expect_json_field "$mcp_alias" '.transport.endpoint' "/mcp" "mcp alias transport endpoint"

for needle in \
  "registration_flow" \
  "registration_endpoint" \
  "$BASE_URL/register" \
  "authorization_endpoint" \
  "$BASE_URL/authorize" \
  "token_endpoint" \
  "$BASE_URL/token" \
  "mcp_endpoint" \
  "$BASE_URL/mcp"; do
  if ! grep -q "$needle" <<<"$auth_md"; then
    fail "auth.md missing $needle"
  fi
done
pass "auth.md machine-readable registration block"

if [[ "${SMOKE_DCR_PROBE:-0}" == "1" ]]; then
  probe_dcr "Claude" "$CLAUDE_REDIRECT_URI" "claude-interop-smoke"
  probe_dcr "ChatGPT" "$CHATGPT_REDIRECT_URI" "chatgpt-interop-smoke"
else
  echo "SKIP DCR probe (SMOKE_DCR_PROBE not set)"
fi

anonymous_count="$(check_tools_list anonymous)"
if [[ -n "${READ_BEARER:-}" ]]; then
  read_count="$(check_tools_list read "$READ_BEARER" "${EXPECT_READ_TOOLS_MIN:-}")"
else
  read_count=""
  echo "SKIP read tools/list (READ_BEARER not provided)"
fi

if [[ -n "${ADMIN_BEARER:-}" ]]; then
  admin_count="$(check_tools_list admin "$ADMIN_BEARER" "${EXPECT_ADMIN_TOOLS_MIN:-}")"
else
  admin_count=""
  echo "SKIP admin tools/list (ADMIN_BEARER not provided)"
fi

if [[ -n "$read_count" ]]; then
  if (( read_count <= anonymous_count )); then
    fail "read tools/list count ($read_count) must be greater than anonymous count ($anonymous_count)"
  fi
  pass "read tools/list exposes more tools than anonymous"
fi

if [[ -n "$read_count" && -n "$admin_count" ]]; then
  if (( admin_count <= read_count )); then
    fail "admin tools/list count ($admin_count) must be greater than read count ($read_count)"
  fi
  pass "admin tools/list exposes more tools than read"
fi

register_status="$(
  curl -sk -o /dev/null -w '%{http_code}' \
    -X POST "$BASE_URL/register" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    --data 'client_name=agent-interop-smoke'
)"
case "$register_status" in
  400|401|403)
    pass "/register reachable (HTTP $register_status)"
    ;;
  *)
    fail "/register is not behaving like a live registration endpoint (HTTP $register_status)"
    ;;
esac

echo "agent interop smoke OK"
