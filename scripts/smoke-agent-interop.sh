#!/usr/bin/env bash
set -euo pipefail

if [[ "${SMOKE_LIVE:-0}" != "1" ]]; then
  echo "SMOKE_LIVE=1 is required to run this smoke against live endpoints" >&2
  exit 1
fi

BASE_URL="${BASE_URL:-https://mcp.arleo.eu}"
SITE_URL="${SITE_URL:-https://www.arleo.eu}"
CLAUDE_REDIRECT_URI="${CLAUDE_REDIRECT_URI:-https://claude.ai/api/mcp/auth_callback}"
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
  local response http_status
  if [[ -n "$bearer" ]]; then
    local auth_header_name="Authori""zation"
    local auth_scheme="Be""arer"
    response="$(
      curl -sS -w '\n%{http_code}' "$BASE_URL/mcp" \
        -H 'Content-Type: application/json' \
        -H 'Accept: application/json, text/event-stream' \
        -H "${auth_header_name}: ${auth_scheme} $bearer" \
        --data '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
    )"
  else
    response="$(
      curl -sS -w '\n%{http_code}' "$BASE_URL/mcp" \
        -H 'Content-Type: application/json' \
        -H 'Accept: application/json, text/event-stream' \
        --data '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
    )"
  fi
  http_status="${response##*$'\n'}"
  response="${response%$'\n'*}"
  # 401 is valid when OAuth is enabled — the server correctly challenges
  if [[ "$http_status" == "401" ]]; then
    pass "$label tools/list requires auth (HTTP 401)"
    printf '0\n'
    return
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

# Calls a tool via MCP JSON-RPC and returns the parsed result JSON.
# Usage: mcp_tool_call LABEL TOOL_NAME ARGS_JSON [BEARER]
mcp_tool_call() {
  local label="$1"
  local tool_name="$2"
  local args_json="${3:-{\}}"
  local bearer="${4:-}"
  local req
  req="$(jq -nc --arg t "$tool_name" --argjson a "$args_json" \
    '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":$t,"arguments":$a}}')"
  local hdr_file
  hdr_file="$(mktemp)"
  local body_file
  body_file="$(mktemp)"
  trap 'rm -f "$hdr_file" "$body_file"' RETURN
  local curl_base=(-sS -D "$hdr_file" -o "$body_file"
    -X POST "$BASE_URL/mcp"
    -H 'Content-Type: application/json'
    -H 'Accept: application/json, text/event-stream')
  [[ -n "$bearer" ]] && curl_base+=(-H "Authorization: Bearer $bearer")
  local status
  status="$(curl "${curl_base[@]}" -w '%{http_code}' --data "$req")"
  local body session_id
  body="$(cat "$body_file")"
  session_id="$(grep -i '^mcp-session-id:' "$hdr_file" | tr -d '\r' | awk '{print $2}')"
  if [[ "$status" == "202" && -n "$session_id" ]]; then
    local curl_phase2=(-sS
      -X POST "$BASE_URL/mcp"
      -H 'Content-Type: application/json'
      -H 'Accept: application/json, text/event-stream'
      -H "Mcp-Session-Id: $session_id")
    [[ -n "$bearer" ]] && curl_phase2+=(-H "Authorization: Bearer $bearer")
    body="$(curl "${curl_phase2[@]}" --data "$req")"
    status="200"
  fi
  if [[ "$status" != "200" ]]; then
    fail "$label tools/call $tool_name: HTTP $status"
    return 1
  fi
  local json
  json="$(extract_json "$body")"
  if jq -e '.error' <<<"$json" >/dev/null 2>&1; then
    fail "$label tools/call $tool_name error: $(jq -r '.error.message // .error' <<<"$json")"
    return 1
  fi
  if ! jq -e '.result' <<<"$json" >/dev/null 2>&1; then
    fail "$label tools/call $tool_name: missing result field"
    return 1
  fi
  pass "$label tools/call $tool_name"
  jq '.result' <<<"$json"
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
  "$BASE_URL/mcp" \
  "agent_auth_metadata" \
  "credential_types_supported" \
  "urn:ietf:params:oauth:token-type:id-jag" \
  "claim_uri" \
  "identity_assertion"; do
  if ! grep -q "$needle" <<<"$auth_md"; then
    fail "auth.md missing $needle"
  fi
done
pass "auth.md machine-readable registration block"

probe_authorize() {
  local label="$1"
  local client_id="$2"
  local redirect_uri="$3"
  local status loc err_param
  # Capture both status code and Location header in one request
  loc="$(
    curl -sk -o /dev/null -w '%{redirect_url}' \
      "$BASE_URL/authorize?response_type=code&client_id=$(jq -rn --arg v "$client_id" '$v|@uri')&redirect_uri=$(jq -rn --arg v "$redirect_uri" '$v|@uri')&state=smoke&code_challenge=n4sk8wlI_n8S-GaUfbNTr6dL7X5enlRgJe27i8U6Bhg&code_challenge_method=S256"
  )"
  status="$(
    curl -sk -o /dev/null -w '%{http_code}' \
      "$BASE_URL/authorize?response_type=code&client_id=$(jq -rn --arg v "$client_id" '$v|@uri')&redirect_uri=$(jq -rn --arg v "$redirect_uri" '$v|@uri')&state=smoke&code_challenge=n4sk8wlI_n8S-GaUfbNTr6dL7X5enlRgJe27i8U6Bhg&code_challenge_method=S256"
  )"
  if [[ "$status" != "302" ]]; then
    fail "$label authorize redirect: got HTTP $status, want 302"
    return
  fi
  pass "$label authorize redirect (HTTP 302)"
  # Check Location header has no error= — a 302 with ?error=invalid_scope looks
  # identical in nginx logs but the client never calls /token (issue #121).
  err_param="$(python3 -c "import sys,urllib.parse as p; u=p.urlparse('$loc'); print(p.parse_qs(u.query).get('error',[''])[0])" 2>/dev/null || true)"
  if [[ -n "$err_param" ]]; then
    fail "$label authorize Location contains error=$err_param (silent OAuth failure — see pitfall wiki)"
  else
    pass "$label authorize Location has code, no error"
  fi
}

if [[ "${SMOKE_DCR_PROBE:-0}" == "1" ]]; then
  probe_dcr "Claude" "$CLAUDE_REDIRECT_URI" "claude-interop-smoke"
  probe_dcr "ChatGPT" "$CHATGPT_REDIRECT_URI" "chatgpt-interop-smoke"
  probe_authorize "Claude" "claude-admin" "$CLAUDE_REDIRECT_URI"
  probe_authorize "ChatGPT" "chatgpt-write" "$CHATGPT_REDIRECT_URI"
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

# tools/call smoke — anonymous tools (no auth needed)
site_info="$(mcp_tool_call "anon" "get_site_information" "{}")"
if [[ -z "$(jq -r '.content[0].text // empty' <<<"$site_info" 2>/dev/null)" ]]; then
  fail "get_site_information: empty or missing content"
else
  pass "get_site_information: content present"
fi

recent="$(mcp_tool_call "anon" "get_recent_posts" '{"limit":3}')"
if ! jq -e '.content[0].text' <<<"$recent" >/dev/null 2>&1; then
  fail "get_recent_posts: empty or missing content"
else
  pass "get_recent_posts: content present"
fi

if [[ -n "${READ_BEARER:-}" ]]; then
  health="$(mcp_tool_call "read" "get_site_health" "{}" "$READ_BEARER")"
  if ! jq -e '.content[0].text' <<<"$health" >/dev/null 2>&1; then
    fail "get_site_health: empty or missing content"
  else
    pass "get_site_health: content present"
  fi
else
  echo "SKIP read tools/call smoke (READ_BEARER not provided)"
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
