#!/usr/bin/env bash
# Verify that the live MCP server passes the key agent-readiness checks.
# Run after deploy to confirm critical discovery endpoints are intact.
# Exit code 0 = all checks passed; non-zero = failures.

BASE="${MCP_BASE_URL:-https://mcp.arleo.eu}"
FAIL=0

pass() { echo "✅ $1"; }
fail() { echo "❌ $1"; FAIL=$((FAIL + 1)); }
ok()   { if [ "$1" -eq 0 ]; then pass "$2"; else fail "$2"; fi; }

# ── RFC 8414: OAuth authorization server metadata ─────────────────────────────
META=$(curl -sf "$BASE/.well-known/oauth-authorization-server") || { fail "RFC 8414: metadata unreachable"; META="{}"; }
python3 -c "import json,sys; d=json.loads(sys.argv[1]); assert d.get('issuer')=='$BASE'" "$META" 2>/dev/null; ok $? "RFC 8414: issuer"
python3 -c "import json,sys; d=json.loads(sys.argv[1]); assert 'authorization_endpoint' in d" "$META" 2>/dev/null; ok $? "RFC 8414: authorization_endpoint"
python3 -c "import json,sys; d=json.loads(sys.argv[1]); assert 'token_endpoint' in d" "$META" 2>/dev/null; ok $? "RFC 8414: token_endpoint"
python3 -c "import json,sys; d=json.loads(sys.argv[1]); assert 'registration_endpoint' in d" "$META" 2>/dev/null; ok $? "RFC 8414: registration_endpoint (#117)"
python3 -c "import json,sys; d=json.loads(sys.argv[1]); assert len(d.get('scopes_supported',[])) > 0" "$META" 2>/dev/null; ok $? "RFC 8414: scopes_supported"
python3 -c "import json,sys; d=json.loads(sys.argv[1]); assert 'S256' in d.get('code_challenge_methods_supported',[])" "$META" 2>/dev/null; ok $? "RFC 8414: S256 code_challenge_method"

# ── RFC 9728: protected resource metadata ────────────────────────────────────
PR=$(curl -sf "$BASE/.well-known/oauth-protected-resource") || { fail "RFC 9728: metadata unreachable"; PR="{}"; }
python3 -c "import json,sys; d=json.loads(sys.argv[1]); assert d.get('resource')=='$BASE/mcp'" "$PR" 2>/dev/null; ok $? "RFC 9728: resource"
python3 -c "import json,sys; d=json.loads(sys.argv[1]); assert '$BASE' in d.get('authorization_servers',[])" "$PR" 2>/dev/null; ok $? "RFC 9728: authorization_servers"
python3 -c "import json,sys; d=json.loads(sys.argv[1]); assert 'header' in d.get('bearer_methods_supported',[])" "$PR" 2>/dev/null; ok $? "RFC 9728: bearer_methods_supported"

# ── RFC 6750: WWW-Authenticate on 401 ────────────────────────────────────────
WWW_HEADERS=$(curl -s -D - -o /dev/null -X POST "$BASE/mcp" \
    -H 'Content-Type: application/json' \
    -H 'Authorization: Bearer invalid_xyz_token' \
    -d '{"jsonrpc":"2.0","method":"tools/list","id":1}' 2>/dev/null)
echo "$WWW_HEADERS" | grep -q "HTTP/[1-2].* 401"; ok $? "RFC 6750: 401 on invalid bearer"
echo "$WWW_HEADERS" | grep -qi "www-authenticate:"; ok $? "RFC 6750: WWW-Authenticate header present"
echo "$WWW_HEADERS" | grep -qi "realm="; ok $? "RFC 6750: realm in WWW-Authenticate"
echo "$WWW_HEADERS" | grep -qi "invalid_token"; ok $? "RFC 6750: error=invalid_token"

# ── RFC 7636: PKCE enforcement ───────────────────────────────────────────────
PKCE_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
    "$BASE/authorize?response_type=code&client_id=x&redirect_uri=https%3A%2F%2Fexample.com%2Fcb&state=s" 2>/dev/null)
[ "$PKCE_CODE" -eq 400 ]; ok $? "RFC 7636: /authorize rejects missing code_challenge (HTTP $PKCE_CODE)"

# ── MCP discovery endpoints ──────────────────────────────────────────────────
SC=$(curl -sf "$BASE/.well-known/mcp/server-card.json") || { fail "MCP: server-card.json unreachable"; SC="{}"; }
python3 -c "import json,sys; d=json.loads(sys.argv[1]); assert 'serverInfo' in d" "$SC" 2>/dev/null; ok $? "MCP: server card accessible"
python3 -c "import json,sys; d=json.loads(sys.argv[1]); assert 'protocolVersion' in d" "$SC" 2>/dev/null; ok $? "MCP: protocolVersion present"
python3 -c "import json,sys; d=json.loads(sys.argv[1]); assert d.get('transport',{}).get('type')=='streamable-http'" "$SC" 2>/dev/null; ok $? "MCP: transport.type=streamable-http"

# ── A2A agent card ───────────────────────────────────────────────────────────
AGENT=$(curl -sf "$BASE/.well-known/agent.json") || { fail "A2A: agent.json unreachable"; AGENT="{}"; }
python3 -c "import json,sys; d=json.loads(sys.argv[1]); assert 'name' in d" "$AGENT" 2>/dev/null; ok $? "A2A: agent.json accessible"
python3 -c "import json,sys; d=json.loads(sys.argv[1]); assert '\$schema' in d" "$AGENT" 2>/dev/null; ok $? "A2A: \$schema present"

# ── auth.md ───────────────────────────────────────────────────────────────────
AUTH_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/auth.md" 2>/dev/null)
[ "$AUTH_CODE" -eq 200 ]; ok $? "auth.md: accessible (HTTP $AUTH_CODE)"
AUTH_BODY=$(curl -sf "$BASE/auth.md" 2>/dev/null) || AUTH_BODY=""
echo "$AUTH_BODY" | grep -qi "registration.endpoint\|/register"; ok $? "auth.md: /register endpoint mentioned"
echo "$AUTH_BODY" | grep -q "content.read"; ok $? "auth.md: correct scopes (content.read)"
! echo "$AUTH_BODY" | grep -q "Scope: \`mcp\`"; ok $? "auth.md: no stale 'Scope: mcp' line"
echo "$AUTH_BODY" | grep -q "registration_flow"; ok $? "auth.md: machine-readable registration_flow present"

# ── www.arleo.eu: protected resource (OpenResty inline JSON) ─────────────────
WWW_PR=$(curl -sf "https://www.arleo.eu/.well-known/oauth-protected-resource") || { fail "www: oauth-protected-resource unreachable"; WWW_PR="{}"; }
python3 -c "import json,sys; d=json.loads(sys.argv[1]); assert d.get('resource')=='https://mcp.arleo.eu/mcp'" "$WWW_PR" 2>/dev/null; ok $? "www: resource points to mcp.arleo.eu/mcp"
python3 -c "import json,sys; d=json.loads(sys.argv[1]); assert 'content.read' in d.get('scopes_supported',[])" "$WWW_PR" 2>/dev/null; ok $? "www: scopes_supported has content.read (not stale mcp)"

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
if [ "$FAIL" -eq 0 ]; then
    echo "All checks passed against $BASE"
else
    echo "$FAIL check(s) FAILED against $BASE"
    exit 1
fi
