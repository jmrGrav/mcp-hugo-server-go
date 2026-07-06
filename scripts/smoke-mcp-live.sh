#!/usr/bin/env bash
set -euo pipefail

if [[ "${MCP_SMOKE_LIVE:-0}" != "1" ]]; then
  echo "MCP_SMOKE_LIVE=1 is required to run this smoke against live or staging endpoints" >&2
  exit 1
fi

BASE_URL="${MCP_BASE_URL:-https://mcp.arleo.eu}"
ACCESS_TOKEN="${MCP_ACCESS_TOKEN:-}"
SMOKE_DELAY="${MCP_SMOKE_DELAY:-6}"
BURST_COUNT="${MCP_SMOKE_BURST_COUNT:-10}"
ENABLE_WRITES="${MCP_SMOKE_ENABLE_WRITES:-0}"
WRITE_SLUG="${MCP_SMOKE_WRITE_SLUG:-codex-mcp-live-audit-$(date -u +%Y%m%d-%H%M%S)}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required tool: $1" >&2
    exit 1
  }
}

need curl
need jq

TMPDIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMPDIR"
}
trap cleanup EXIT

pass() { printf 'PASS %s\n' "$1"; }
warn() { printf 'WARN %s\n' "$1"; }
fail() { printf 'FAIL %s\n' "$1" >&2; exit 1; }

redact() {
  sed -E \
    -e 's/(Authorization: Bearer )[A-Za-z0-9._~+\/-]+/\1<redacted>/g' \
    -e 's/(access_token|refresh_token|client_secret)(["=: ]+)[^" ,}]+/\1\2<redacted>/Ig'
}

extract_json_file() {
  local body_file="$1"
  if grep -q '^data: ' "$body_file"; then
    sed -n 's/^data: //p' "$body_file" | tail -n1
    return
  fi
  cat "$body_file"
}

write_curl_config() {
  local cfg="$1"
  {
    printf '%s\n' 'header = "Content-Type: application/json"'
    printf '%s\n' 'header = "Accept: application/json, text/event-stream"'
    if [[ -n "$ACCESS_TOKEN" ]]; then
      printf 'header = "Authorization: Bearer %s"\n' "$ACCESS_TOKEN"
    fi
    if [[ -n "${MCP_SESSION_ID:-}" ]]; then
      printf 'header = "Mcp-Session-Id: %s"\n' "$MCP_SESSION_ID"
    fi
  } > "$cfg"
  chmod 600 "$cfg"
}

HTTP_STATUS=""
RETRY_AFTER=""
CONTENT_TYPE=""
BODY_FILE=""
MCP_SESSION_ID="${MCP_SESSION_ID:-}"

post_mcp() {
  local request_file="$1"
  local cfg="$TMPDIR/curl.conf"
  local headers="$TMPDIR/headers.$RANDOM"
  local body="$TMPDIR/body.$RANDOM"
  write_curl_config "$cfg"
  HTTP_STATUS="$(
    curl -sS -D "$headers" -o "$body" -w '%{http_code}' \
      --max-time 30 --connect-timeout 10 \
      -X POST "$BASE_URL/mcp" \
      --config "$cfg" \
      --data-binary "@$request_file"
  )"
  # tolower($1) is POSIX awk; avoids gawk-only BEGIN{IGNORECASE=1}
  RETRY_AFTER="$(awk 'tolower($1) == "retry-after:" {gsub(/\r/,""); print $2}' "$headers" | tail -1)"
  CONTENT_TYPE="$(awk 'tolower($1) == "content-type:" {line=$0; gsub(/\r/,"",line); sub(/^[^:]*:[[:space:]]*/,"",line); print line}' "$headers" | tail -1)"
  local session
  session="$(awk 'tolower($1) == "mcp-session-id:" {gsub(/\r/,""); print $2}' "$headers" | tail -1)"
  if [[ -n "$session" ]]; then
    MCP_SESSION_ID="$session"
  fi
  BODY_FILE="$body"
}

classify_response() {
  local label="$1"
  local expect_success="${2:-1}"
  local json=""

  case "$HTTP_STATUS" in
    200|202)
      ;;
    401|403)
      json="$(extract_json_file "$BODY_FILE" 2>/dev/null || true)"
      if jq -e '.error' >/dev/null 2>&1 <<<"$json"; then
        local code msg
        code="$(jq -r '.error.code // empty' <<<"$json")"
        msg="$(jq -r '.error.message // .error' <<<"$json")"
        printf 'RPC_FAIL %s http=%s code=%s message=%s\n' "$label" "$HTTP_STATUS" "${code:-none}" "$msg"
        [[ "$expect_success" == "0" ]] && return 0
        return 1
      fi
      printf 'AUTH_FAIL %s http=%s content_type=%s\n' "$label" "$HTTP_STATUS" "$CONTENT_TYPE"
      [[ "$expect_success" == "0" ]] && return 0
      return 1
      ;;
    429)
      json="$(extract_json_file "$BODY_FILE" 2>/dev/null || true)"
      local code retry_json msg
      code="$(jq -r '.error.code // empty' <<<"$json" 2>/dev/null || true)"
      retry_json="$(jq -r '.error.data.retry_after_seconds // .retry_after_seconds // empty' <<<"$json" 2>/dev/null || true)"
      msg="$(jq -r '.error.message // .message // empty' <<<"$json" 2>/dev/null || true)"
      printf 'RATE_LIMIT %s http=429 code=%s retry_header=%s retry_json=%s message=%s\n' \
        "$label" "${code:-none}" "${RETRY_AFTER:-none}" "${retry_json:-none}" "${msg:-none}"
      if [[ "$code" != "-32029" ]]; then
        printf 'FAIL %s rate-limit body is not JSON-RPC 2.0 code -32029\n' "$label" >&2
        return 1
      fi
      return 0
      ;;
    503)
      if grep -qi '<html' "$BODY_FILE"; then
        printf 'PROXY_FAIL %s http=503 html=true content_type=%s\n' "$label" "$CONTENT_TYPE"
      else
        printf 'HTTP_FAIL %s http=503 html=false content_type=%s body=%s\n' "$label" "$CONTENT_TYPE" "$(head -c 160 "$BODY_FILE" | redact)"
      fi
      return 1
      ;;
    *)
      printf 'HTTP_FAIL %s http=%s content_type=%s body=%s\n' "$label" "$HTTP_STATUS" "$CONTENT_TYPE" "$(head -c 160 "$BODY_FILE" | redact)"
      return 1
      ;;
  esac

  if [[ "$HTTP_STATUS" == "202" ]]; then
    printf 'OK %s http=202 accepted\n' "$label"
    return 0
  fi

  json="$(extract_json_file "$BODY_FILE" 2>/dev/null || true)"
  if ! jq -e . >/dev/null 2>&1 <<<"$json"; then
    printf 'HTTP_FAIL %s invalid_json body=%s\n' "$label" "$(printf '%s' "$json" | head -c 160 | redact)"
    return 1
  fi
  if jq -e '.error' >/dev/null 2>&1 <<<"$json"; then
    local code msg
    code="$(jq -r '.error.code // empty' <<<"$json")"
    msg="$(jq -r '.error.message // .error' <<<"$json")"
    printf 'RPC_FAIL %s code=%s message=%s\n' "$label" "${code:-none}" "$msg"
    [[ "$expect_success" == "0" ]] && return 0
    return 1
  fi
  if jq -e '.result.isError == true' >/dev/null 2>&1 <<<"$json"; then
    local text
    text="$(jq -r '.result.content[0].text // empty' <<<"$json" | head -c 180 | tr '\n' ' ')"
    printf 'TOOL_FAIL %s result.isError=true text=%s\n' "$label" "$text"
    [[ "$expect_success" == "0" ]] && return 0
    return 1
  fi
  if ! jq -e '.result' >/dev/null 2>&1 <<<"$json"; then
    printf 'RPC_FAIL %s missing result\n' "$label"
    return 1
  fi
  printf 'OK %s http=200 bytes=%s\n' "$label" "$(printf '%s' "$json" | wc -c | tr -d ' ')"
  printf '%s' "$json" > "$TMPDIR/last-$label.json"
}

mcp_request() {
	local label="$1"
	local method="$2"
	local params="${3-}"
	local expect_success="${4:-1}"
	local req="$TMPDIR/request-$label.json"
	if [[ -z "$params" ]]; then
		params="null"
	fi
  if [[ "$params" == "null" ]]; then
    jq -nc --arg m "$method" '{jsonrpc:"2.0",id:1,method:$m}' > "$req"
  else
    jq -nc --arg m "$method" --argjson p "$params" '{jsonrpc:"2.0",id:1,method:$m,params:$p}' > "$req"
  fi
  post_mcp "$req"
  classify_response "$label" "$expect_success"
}

call_tool() {
	local label="$1"
	local tool="$2"
	local args="${3-}"
	local expect_success="${4:-1}"
	local params
	if [[ -z "$args" ]]; then
		args="{}"
	fi
  params="$(jq -nc --arg name "$tool" --argjson args "$args" '{name:$name,arguments:$args}')"
  mcp_request "$label" "tools/call" "$params" "$expect_success"
}

if [[ -z "$ACCESS_TOKEN" ]]; then
  warn "MCP_ACCESS_TOKEN is not set; authenticated tools/list and tools/call checks will be skipped"
  printf '%s' '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' > "$TMPDIR/noauth-list.json"
  MCP_SESSION_ID=""
  post_mcp "$TMPDIR/noauth-list.json"
  classify_response "tools_list_without_auth" 0 || true
  exit 0
fi

echo "MCP live smoke"
echo "  MCP_BASE_URL=$BASE_URL"
echo "  MCP_ACCESS_TOKEN=<redacted>"
echo "  MCP_SMOKE_ENABLE_WRITES=$ENABLE_WRITES"
echo "  MCP_SMOKE_DELAY=$SMOKE_DELAY"

printf '%s' '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"mcp-live-smoke","version":"1.0.0"}}}' > "$TMPDIR/initialize.json"
post_mcp "$TMPDIR/initialize.json"
classify_response "initialize"
[[ -n "$MCP_SESSION_ID" ]] || fail "initialize did not return Mcp-Session-Id"

printf '%s' '{"jsonrpc":"2.0","method":"notifications/initialized"}' > "$TMPDIR/initialized.json"
post_mcp "$TMPDIR/initialized.json"
classify_response "initialized"

mcp_request "tools_list" "tools/list"
tools_count="$(jq -r '.result.tools | length' "$TMPDIR/last-tools_list.json")"
pass "tools/list returned $tools_count tools"

call_tool "unknown_tool" "codex_unknown_tool" "{}" 0
call_tool "get_site_information" "get_site_information" "{}"
sleep "$SMOKE_DELAY"
call_tool "list_pages" "list_pages" '{"limit":5,"offset":0}'
# Parse the nested JSON from result.content[0].text — the go-sdk serialises
# tool output as a JSON string inside content[], not in structuredContent.
pages_text="$(jq -r '.result.content[0].text // empty' "$TMPDIR/last-list_pages.json" 2>/dev/null || true)"
if [[ -n "$pages_text" ]] && printf '%s' "$pages_text" | jq -e 'any(.pages[]?.slug // ""; test("/(tags|categories|series)/"))' >/dev/null 2>&1; then
  fail "list_pages returned taxonomy slugs"
fi
sleep "$SMOKE_DELAY"
call_tool "get_sitemap" "get_sitemap" '{"limit":5,"offset":0}'
sleep "$SMOKE_DELAY"
call_tool "get_recent_posts" "get_recent_posts" '{"limit":3}'
sleep "$SMOKE_DELAY"
call_tool "search_content" "search_content" '{"query":"arleo","limit":3,"offset":0}'
sleep "$SMOKE_DELAY"
call_tool "get_site_health" "get_site_health" "{}"
sleep "$SMOKE_DELAY"
call_tool "validate_site" "validate_site" "{}"

if [[ "${MCP_SMOKE_BURST:-0}" == "1" ]]; then
  echo "Burst probe: $BURST_COUNT calls without pacing"
  for i in $(seq 1 "$BURST_COUNT"); do
    call_tool "burst_get_site_information_$i" "get_site_information" "{}" || fail "burst failed at call $i"
  done
fi

if [[ "$ENABLE_WRITES" == "1" ]]; then
  echo "Write checks enabled for slug: $WRITE_SLUG"
  sleep "$SMOKE_DELAY"
  call_tool "create_page" "create_page" "$(jq -nc --arg slug "$WRITE_SLUG" '{slug:$slug,title:"MCP live smoke",body:"Temporary MCP smoke page. Safe to delete.",tags:["mcp-smoke"],categories:["testing"]}')"
  sleep "$SMOKE_DELAY"
  call_tool "update_page" "update_page" "$(jq -nc --arg slug "$WRITE_SLUG" '{slug:$slug,title:"MCP live smoke updated",body:"Temporary MCP smoke page updated. Safe to delete."}')"
  sleep "$SMOKE_DELAY"
  call_tool "generate_featured_image" "generate_featured_image" "$(jq -nc --arg slug "$WRITE_SLUG" --arg prompt "Photo of a mountain at sunset" '{slug:$slug,prompt:$prompt}')"
  img_result="$(jq -r '.result.content[0].text // empty' "$TMPDIR/last-generate_featured_image.json" 2>/dev/null || true)"
  if [[ "$img_result" == *"config_error"* ]]; then
    echo "SKIP generate_featured_image (image_gen_url not configured)"
  elif [[ -z "$img_result" ]]; then
    fail "generate_featured_image returned empty content"
  else
    pass "generate_featured_image: content present (${#img_result} chars)"
  fi
  sleep "$SMOKE_DELAY"
  call_tool "get_page_created" "get_page" "$(jq -nc --arg slug "$WRITE_SLUG" '{slug:$slug}')"
  sleep "$SMOKE_DELAY"
  call_tool "delete_page" "delete_page" "$(jq -nc --arg slug "$WRITE_SLUG" '{slug:$slug}')"
  sleep "$SMOKE_DELAY"
  call_tool "preview_build" "preview_build" "{}"
  sleep "$SMOKE_DELAY"
  call_tool "build_site" "build_site" "{}"
else
  echo "SKIP write/admin mutation checks (set MCP_SMOKE_ENABLE_WRITES=1 to enable)"
fi

echo "mcp live smoke completed"
