#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${MCP_STAGING_PORT:-18088}"
BINARY="${MCP_STAGING_BINARY:-}"
SMOKE_DELAY="${MCP_SMOKE_DELAY:-1}"

if [[ "${MCP_SMOKE_ENABLE_WRITES:-0}" == "1" ]]; then
  echo "local staging smoke is read-only; use the Hugo VM staging runbook for write/build checks" >&2
  exit 1
fi

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required tool: $1" >&2
    exit 1
  }
}

need curl
need jq
need python3
need openssl

TMPDIR="$(mktemp -d)"
SERVER_PID=""
cleanup() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$TMPDIR"
}
trap cleanup EXIT

if [[ -z "$BINARY" ]]; then
  BINARY="$TMPDIR/mcp-hugo-server-go"
  go build -ldflags "-X github.com/jmrGrav/mcp-hugo-server-go/internal/server.Version=staging-local" -o "$BINARY" ./cmd/mcp-hugo-server-go
fi

mkdir -p "$TMPDIR/site"
cp -R "$ROOT/testdata/fixtures/public/minimal" "$TMPDIR/site/public"
cp -R "$ROOT/testdata/fixtures/content" "$TMPDIR/site/content"
printf '# auth\n' > "$TMPDIR/site/public/auth.md"

cat > "$TMPDIR/registry.yaml" <<'YAML'
clients:
  - client_id: claude-admin
    client_secret: staging-local-secret-long-enough-value
    redirect_uris:
      - https://claude.ai/api/mcp/auth_callback
    scope: site.admin
YAML

cat > "$TMPDIR/config.yaml" <<YAML
site_root: $TMPDIR/site/public
hugo_root: $TMPDIR/site
content_root: $TMPDIR/site/content
site_url: https://www.example.com
site_name: staging-local
language_default: fr
transport: http
http_bind_addr: 127.0.0.1
http_bind_port: $PORT
streaming_enabled: true
oauth:
  enabled: true
  issuer: http://127.0.0.1:$PORT
  resource: http://127.0.0.1:$PORT/mcp
  dynamic_client_enabled: true
  auth_code_ttl_seconds: 300
  access_token_ttl_seconds: 3600
  trusted_authorize_cidrs:
    - 127.0.0.1/32
  client_registry_path: $TMPDIR/registry.yaml
  storage_backend: sqlite
  storage_path: $TMPDIR/tokens.db
YAML

MCP_HUGO_SERVER_CONFIG="$TMPDIR/config.yaml" "$BINARY" >"$TMPDIR/server.log" 2>&1 &
SERVER_PID="$!"

for _ in $(seq 1 40); do
  if curl -sf "http://127.0.0.1:$PORT/.well-known/oauth-authorization-server" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
curl -sf "http://127.0.0.1:$PORT/.well-known/oauth-authorization-server" >/dev/null || {
  cat "$TMPDIR/server.log" >&2
  exit 1
}

VERIFIER="staging-local-verifier-long-enough-value"
CHALLENGE="$(printf '%s' "$VERIFIER" | openssl dgst -sha256 -binary | openssl base64 -A | tr '+/' '-_' | tr -d '=')"
ENC_REDIRECT="$(python3 - <<'PY'
import urllib.parse
print(urllib.parse.quote('https://claude.ai/api/mcp/auth_callback', safe=''))
PY
)"
AUTH_URL="http://127.0.0.1:${PORT}/authorize?response_type=code&client_id=claude-admin&redirect_uri=${ENC_REDIRECT}&state=staging-local&code_challenge=${CHALLENGE}&code_challenge_method=S256&scope=site.admin"
LOCATION="$(curl -sS -D - -o /dev/null "$AUTH_URL" | awk 'tolower($1)=="location:" {sub(/^[^:]*:[[:space:]]*/,""); gsub(/\r/,""); print}')"
CODE="$(python3 - <<'PY' "$LOCATION"
import sys, urllib.parse
print(urllib.parse.parse_qs(urllib.parse.urlparse(sys.argv[1]).query).get('code', [''])[0])
PY
)"
[[ -n "$CODE" ]] || {
  echo "failed to obtain authorization code" >&2
  exit 1
}

TOKEN_JSON="$(curl -sS -X POST "http://127.0.0.1:${PORT}/token" \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode grant_type=authorization_code \
  --data-urlencode client_id=claude-admin \
  --data-urlencode client_secret=staging-local-secret-long-enough-value \
  --data-urlencode code="$CODE" \
  --data-urlencode redirect_uri=https://claude.ai/api/mcp/auth_callback \
  --data-urlencode code_verifier="$VERIFIER")"
TOKEN="$(printf '%s' "$TOKEN_JSON" | jq -r '.access_token')"
[[ -n "$TOKEN" && "$TOKEN" != "null" ]] || {
  printf '%s\n' "$TOKEN_JSON" >&2
  exit 1
}

MCP_SMOKE_LIVE=1 \
MCP_BASE_URL="http://127.0.0.1:${PORT}" \
MCP_ACCESS_TOKEN="$TOKEN" \
MCP_SMOKE_DELAY="$SMOKE_DELAY" \
bash "$ROOT/scripts/smoke-mcp-live.sh"
