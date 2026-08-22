#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/scripts/check-agent-ready.sh"
MCP_TMPDIR="$(mktemp -d)"
WWW_TMPDIR="$(mktemp -d)"
MCP_SERVER_PID=""
WWW_SERVER_PID=""

alloc_port() {
  python3 - <<'EOF'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
EOF
}

MCP_PORT="$(alloc_port)"
WWW_PORT="$(alloc_port)"
MCP_URL="http://127.0.0.1:$MCP_PORT"
WWW_URL_VALUE="http://127.0.0.1:$WWW_PORT"

cleanup() {
  for pid in "$MCP_SERVER_PID" "$WWW_SERVER_PID"; do
    if [[ -n "$pid" ]]; then
      kill "$pid" >/dev/null 2>&1 || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  rm -rf "$MCP_TMPDIR" "$WWW_TMPDIR"
}
trap cleanup EXIT

# The mock fixtures below deliberately mirror the real two-host production
# topology (mcp.arleo.eu vs www.arleo.eu are two distinct servers with two
# distinct oauth-protected-resource documents, `resource` differing by
# design) instead of one combined server for both roles. A single-server
# fixture previously let check-agent-ready.sh's new www.arleo.eu-specific
# checks (#1250/#1251) pass a `resource` value that could never be correct
# for both roles simultaneously — exactly the identity-collision class the
# real site's own regression was about.
write_mcp_fixture() {
  local scopes_json="$1"
  mkdir -p "$MCP_TMPDIR/.well-known/mcp"
  cat >"$MCP_TMPDIR/.well-known/oauth-authorization-server" <<EOF
{"issuer":"$MCP_URL","authorization_endpoint":"$MCP_URL/authorize","token_endpoint":"$MCP_URL/token","registration_endpoint":"$MCP_URL/register","scopes_supported":$scopes_json}
EOF
  cat >"$MCP_TMPDIR/.well-known/oauth-protected-resource" <<EOF
{"resource":"$MCP_URL/mcp","authorization_servers":["$MCP_URL"],"bearer_methods_supported":["header"],"scopes_supported":$scopes_json}
EOF
  cat >"$MCP_TMPDIR/.well-known/mcp/server-card.json" <<EOF
{"transport":{"endpoint":"/mcp"}}
EOF
  cp "$MCP_TMPDIR/.well-known/mcp/server-card.json" "$MCP_TMPDIR/.well-known/mcp.json"
}

write_www_fixture() {
  local scopes_json="$1"
  mkdir -p "$WWW_TMPDIR/.well-known/agent-skills"
  cat >"$WWW_TMPDIR/.well-known/oauth-protected-resource" <<EOF
{"resource":"$WWW_URL_VALUE","authorization_servers":["$MCP_URL"],"bearer_methods_supported":["header"],"scopes_supported":$scopes_json}
EOF
  cat >"$WWW_TMPDIR/auth.md" <<EOF
# Auth

registration_flow
registration_endpoint $MCP_URL/register
authorization_endpoint $MCP_URL/authorize
token_endpoint $MCP_URL/token
mcp_endpoint $MCP_URL/mcp
agent_auth_metadata
credential_types_supported
urn:ietf:params:oauth:token-type:id-jag
claim_uri
identity_assertion
EOF
  cat >"$WWW_TMPDIR/.well-known/ai-catalog.json" <<EOF
{"specVersion":"1.0","host":{"displayName":"fixture"},"entries":[{"identifier":"urn:air:fixture:mcp:server","displayName":"fixture","type":"application/mcp-server-card+json","url":"$MCP_URL/.well-known/mcp/server-card.json"}]}
EOF
  cat >"$WWW_TMPDIR/.well-known/agent-skills/schema.json" <<EOF
{"\$schema":"https://json-schema.org/draft/2020-12/schema","\$id":"$WWW_URL_VALUE/.well-known/agent-skills/schema.json","type":"object","required":["skills"],"properties":{"skills":{"type":"array"}}}
EOF
  cat >"$WWW_TMPDIR/.well-known/agent-skills/index.json" <<EOF
{"\$schema":"$WWW_URL_VALUE/.well-known/agent-skills/schema.json","skills":[{"name":"fixture_skill","type":"skill-md","description":"fixture","url":"$WWW_URL_VALUE/.well-known/agent-skills/fixture_skill.md"}]}
EOF
}

write_fixture() {
  # Same scopes_supported on both fixtures by default, matching the
  # real-world invariant check-agent-ready.sh now enforces (#1251).
  write_mcp_fixture "$1"
  write_www_fixture "$1"
}

start_mock_server() {
  local root="$1" port="$2" pid_var="$3"
  cat >"$root/server.py" <<'EOF'
import os
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

ROOT = Path(__file__).resolve().parent
PORT = int(os.environ["PORT"])

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/.well-known/oauth-protected-resource/mcp":
            base = f"http://127.0.0.1:{PORT}"
            data = ('{"resource":"%s/mcp","authorization_servers":["%s"]}' % (base, base)).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
            return
        rel = self.path.lstrip("/")
        target = ROOT / rel
        if target.is_dir():
            self.send_response(404)
            self.end_headers()
            return
        if not target.exists():
            self.send_response(404)
            self.end_headers()
            return
        if self.path == "/auth.md":
            content_type = "text/markdown; charset=utf-8"
        else:
            content_type = "application/json; charset=utf-8"
        data = target.read_bytes()
        self.send_response(200)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_POST(self):
        if self.path == "/register":
            self.send_response(400)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.end_headers()
            self.wfile.write(b'{"error":"invalid_client_metadata"}')
            return
        self.send_response(404)
        self.end_headers()

    def log_message(self, format, *args):
        return

HTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
EOF
  PORT="$port" python3 "$root/server.py" &
  eval "$pid_var=$!"
  sleep 1
}

start_servers() {
  start_mock_server "$MCP_TMPDIR" "$MCP_PORT" MCP_SERVER_PID
  start_mock_server "$WWW_TMPDIR" "$WWW_PORT" WWW_SERVER_PID
}

run_expect_success() {
  local label="$1"
  shift
  if WWW_URL="$WWW_URL_VALUE" "$@" >/tmp/check-agent-ready.stdout 2>/tmp/check-agent-ready.stderr; then
    echo "PASS: $label"
  else
    echo "FAIL: $label" >&2
    cat /tmp/check-agent-ready.stderr >&2
    return 1
  fi
}

run_expect_failure() {
  local label="$1"
  shift
  if WWW_URL="$WWW_URL_VALUE" "$@" >/tmp/check-agent-ready.stdout 2>/tmp/check-agent-ready.stderr; then
    echo "FAIL: $label unexpectedly succeeded" >&2
    return 1
  fi
  echo "PASS: $label"
}

write_fixture '["read","write","admin"]'
start_servers
run_expect_success "canonical scopes pass" "$SCRIPT" "$MCP_URL"
cleanup
MCP_SERVER_PID=""
WWW_SERVER_PID=""
MCP_TMPDIR="$(mktemp -d)"
WWW_TMPDIR="$(mktemp -d)"

write_fixture '["read","write","site.admin"]'
start_servers
run_expect_failure "legacy site.admin advertised fails" "$SCRIPT" "$MCP_URL"
