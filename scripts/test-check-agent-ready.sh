#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/scripts/check-agent-ready.sh"
TMPDIR="$(mktemp -d)"
SERVER_PID=""
PORT="$(
  python3 - <<'EOF'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
EOF
)"
BASE_URL="http://127.0.0.1:$PORT"

cleanup() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$TMPDIR"
}
trap cleanup EXIT

write_fixture() {
  local scopes_json="$1"
  mkdir -p "$TMPDIR/.well-known/mcp"
  cat >"$TMPDIR/.well-known/oauth-authorization-server" <<EOF
{"issuer":"$BASE_URL","authorization_endpoint":"$BASE_URL/authorize","token_endpoint":"$BASE_URL/token","registration_endpoint":"$BASE_URL/register","scopes_supported":$scopes_json}
EOF
  cat >"$TMPDIR/.well-known/oauth-protected-resource" <<EOF
{"resource":"$BASE_URL/mcp","authorization_servers":["$BASE_URL"]}
EOF
  cat >"$TMPDIR/.well-known/mcp/server-card.json" <<EOF
{"transport":{"endpoint":"/mcp"}}
EOF
  cp "$TMPDIR/.well-known/mcp/server-card.json" "$TMPDIR/.well-known/mcp.json"
  cat >"$TMPDIR/auth.md" <<EOF
# Auth

registration_flow
registration_endpoint $BASE_URL/register
authorization_endpoint $BASE_URL/authorize
token_endpoint $BASE_URL/token
mcp_endpoint $BASE_URL/mcp
agent_auth_metadata
credential_types_supported
urn:ietf:params:oauth:token-type:id-jag
claim_uri
identity_assertion
EOF
}

start_server() {
  cat >"$TMPDIR/server.py" <<'EOF'
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
  PORT="$PORT" python3 "$TMPDIR/server.py" &
  SERVER_PID=$!
  sleep 1
}

run_expect_success() {
  local label="$1"
  shift
  if WWW_URL="$BASE_URL" "$@" >/tmp/check-agent-ready.stdout 2>/tmp/check-agent-ready.stderr; then
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
  if WWW_URL="$BASE_URL" "$@" >/tmp/check-agent-ready.stdout 2>/tmp/check-agent-ready.stderr; then
    echo "FAIL: $label unexpectedly succeeded" >&2
    return 1
  fi
  echo "PASS: $label"
}

write_fixture '["read","write"]'
start_server
run_expect_success "canonical scopes pass" "$SCRIPT" "$BASE_URL"
cleanup
SERVER_PID=""

write_fixture '["read","write","site.admin"]'
start_server
run_expect_failure "legacy site.admin advertised fails" "$SCRIPT" "$BASE_URL"
