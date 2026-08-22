#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OPENRESTY_CONF="$ROOT_DIR/docs/examples/agent-ready/openresty-www.arleo.eu.conf"
MCP_OPENRESTY_CONF="$ROOT_DIR/docs/examples/agent-ready/openresty-mcp.arleo.eu.conf"
AUTH_MD="$ROOT_DIR/docs/examples/agent-ready/static/auth.md"
RESOURCE_JSON="$ROOT_DIR/docs/examples/agent-ready/static/.well-known/oauth-protected-resource"
HOWTO="$ROOT_DIR/docs/agent-ready-howto.md"
RFC_COMPLIANCE="$ROOT_DIR/docs/rfc-compliance.md"
AGENT_SKILLS_INDEX="$ROOT_DIR/docs/examples/agent-ready/static/.well-known/agent-skills/index.json"
AGENT_SKILLS_SCHEMA="$ROOT_DIR/docs/examples/agent-ready/static/.well-known/agent-skills/schema.json"

need_pattern() {
  local file="$1"
  local pattern="$2"
  local label="$3"
  if ! grep -qF "$pattern" "$file"; then
    echo "FAIL: $label missing in $file" >&2
    return 1
  fi
  echo "PASS: $label"
}

forbid_pattern() {
  local file="$1"
  local pattern="$2"
  local label="$3"
  if grep -qF "$pattern" "$file"; then
    echo "FAIL: $label unexpectedly present in $file" >&2
    return 1
  fi
  echo "PASS: $label"
}

need_pattern "$OPENRESTY_CONF" "location = /.well-known/oauth-protected-resource/mcp {" "www alias route for protected-resource/mcp"
need_pattern "$MCP_OPENRESTY_CONF" "error_page 413 = @request_too_large;" "MCP proxy routes oversized requests to a structured response"
need_pattern "$MCP_OPENRESTY_CONF" '"code":-32010' "MCP proxy 413 response uses the server request-too-large code"
need_pattern "$MCP_OPENRESTY_CONF" "default_type application/json;" "MCP proxy 413 response is JSON"
need_pattern "$OPENRESTY_CONF" "location = /.well-known/mcp/server-card.json {" "www alias route for server-card.json"
need_pattern "$OPENRESTY_CONF" "location = /.well-known/mcp.json {" "www alias route for mcp.json"
need_pattern "$HOWTO" "https://www.arleo.eu/.well-known/oauth-protected-resource/mcp" "howto documents www protected-resource alias"
need_pattern "$HOWTO" "https://www.arleo.eu/.well-known/mcp/server-card.json" "howto documents www server card alias"
forbid_pattern "$AUTH_MD" '"system.admin"' "auth.md canonical scope list excludes system.admin"
forbid_pattern "$AUTH_MD" '"site.admin"' "auth.md canonical scope list excludes site.admin"
forbid_pattern "$RESOURCE_JSON" '"system.admin"' "website protected-resource excludes system.admin"
forbid_pattern "$RESOURCE_JSON" '"site.admin"' "website protected-resource excludes site.admin"
forbid_pattern "$AUTH_MD" '"returns": ["client_id", "client_secret"]' "public DCR docs never promise a client secret"
forbid_pattern "$RFC_COMPLIANCE" '`client_id` + `client_secret` returned' "RFC matrix never claims public DCR returns a client secret"
need_pattern "$OPENRESTY_CONF" "location = /.well-known/agent-skills/schema.json {" "www route for self-hosted agent-skills schema (#1250)"
forbid_pattern "$AGENT_SKILLS_INDEX" "schemas.agentskills.io" "agent-skills index.json never references the dead schemas.agentskills.io host"
need_pattern "$AGENT_SKILLS_INDEX" "https://www.arleo.eu/.well-known/agent-skills/schema.json" "agent-skills index.json \$schema points at the self-hosted schema"
need_pattern "$AGENT_SKILLS_SCHEMA" '"$id": "https://www.arleo.eu/.well-known/agent-skills/schema.json"' "self-hosted schema's \$id matches its own served URL"
