#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
SERVER_PID=""
cleanup() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$TMP"
}
trap cleanup EXIT

start_fixture() {
  local fallback="$1"
  rm -f "$TMP/port"
  FIXTURE_PORT_FILE="$TMP/port" FIXTURE_FALLBACK="$fallback" python3 - <<'PY' &
import json, os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

fallback = os.environ.get("FIXTURE_FALLBACK") == "1"

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def send_json(self, payload):
        raw = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Mcp-Session-Id", "fixture-session")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        request = json.loads(self.rfile.read(length) or b"{}")
        method = request.get("method")
        req_id = request.get("id")
        if method == "tools/list":
            self.send_json({"jsonrpc":"2.0", "id":req_id, "result":{"tools":[{"name":"fixture"}]}})
            return
        if method != "tools/call":
            self.send_json({"jsonrpc":"2.0", "id":req_id, "result":{}})
            return
        name = request.get("params", {}).get("name", "")
        if name == "codex_unknown_tool":
            self.send_json({"jsonrpc":"2.0", "id":req_id, "result":{"isError":True, "content":[{"type":"text", "text":"unknown_tool"}]}})
            return
        base = "http://" + self.headers["Host"]
        if name == "build_site":
            data = {"publish_ready":True, "stages":{"hugo_build":"ok", "output_swap":"ok", "source_index_reload":"ok", "public_index_reload":"ok", "callbacks_status":"ok"}}
        elif name == "get_site_health":
            data = {"public_output_complete":True, "missing_public_pages":0, "published_pages":2, "publishable_source_pages":2, "source_pages":2, "draft_pages":0, "runtime_degraded":False}
        elif name == "list_pages":
            data = {"pages":[
                {"slug":"/en/posts/probe/", "url":base+"/en/posts/probe/", "lang":"en"},
                {"slug":"/posts/sonde/", "url":base+"/posts/sonde/", "lang":"fr"}],
                "total":2, "next_offset":None}
        else:
            data = {}
        text = json.dumps({"success":True, "data":data, "warnings":[]})
        self.send_json({"jsonrpc":"2.0", "id":req_id, "result":{"isError":False, "content":[{"type":"text", "text":text}]}})

    def do_GET(self):
        base = "http://" + self.headers["Host"]
        if fallback:
            canonical, lang = base + "/", "fr"
        elif self.path == "/en/posts/probe/":
            canonical, lang = base + self.path, "en"
        elif self.path == "/posts/sonde/":
            canonical, lang = base + self.path, "fr"
        else:
            self.send_error(404)
            return
        padding = "x" * 300
        raw = f'<!doctype html><html lang="{lang}"><head><link rel="canonical" href="{canonical}"></head><body>{padding}</body></html>'.encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/html")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
with open(os.environ["FIXTURE_PORT_FILE"], "w") as f:
    f.write(str(server.server_address[1]))
server.serve_forever()
PY
  SERVER_PID=$!
  for _ in $(seq 1 50); do
    [[ -s "$TMP/port" ]] && return
    sleep 0.05
  done
  echo "fixture failed to start" >&2
  exit 1
}

run_smoke() {
  printf '%s\n' '[{"name":"fixture"}]' > "$TMP/tool-registry.json"
  MCP_SMOKE_LIVE=1 \
  MCP_BASE_URL="http://127.0.0.1:$(cat "$TMP/port")" \
  MCP_ACCESS_TOKEN="fixture-token" \
  MCP_WRITE_ACCESS_TOKEN="fixture-write-token" \
  MCP_SMOKE_VERIFY_WRITE_CATALOGUE=1 \
  MCP_TOOL_REGISTRY_MANIFEST="$TMP/tool-registry.json" \
  MCP_SMOKE_DELAY=0 \
  MCP_SMOKE_VERIFY_DEPLOY=1 \
  bash "$ROOT/scripts/smoke-mcp-live.sh"
}

start_fixture 0
good_output="$(run_smoke)"
grep -q 'PASS build_site completed with clean output and callbacks' <<<"$good_output"
grep -q 'PASS English edge page identity verified' <<<"$good_output"
grep -q 'PASS French edge page identity verified' <<<"$good_output"
kill "$SERVER_PID"
wait "$SERVER_PID" 2>/dev/null || true
SERVER_PID=""

start_fixture 1
if bad_output="$(run_smoke 2>&1)"; then
  echo "homepage fallback unexpectedly passed deploy-integrity smoke" >&2
  exit 1
fi
grep -q 'edge response does not contain its own canonical URL' <<<"$bad_output"

echo "deploy-integrity smoke tests passed"
