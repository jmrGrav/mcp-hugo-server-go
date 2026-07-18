#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OPENRESTY_CONF="$ROOT_DIR/docs/examples/agent-ready/openresty-www.arleo.eu.conf"
AUTH_MD="$ROOT_DIR/docs/examples/agent-ready/static/auth.md"
RESOURCE_JSON="$ROOT_DIR/docs/examples/agent-ready/static/.well-known/oauth-protected-resource"
HOWTO="$ROOT_DIR/docs/agent-ready-howto.md"

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
need_pattern "$OPENRESTY_CONF" "location = /.well-known/mcp/server-card.json {" "www alias route for server-card.json"
need_pattern "$OPENRESTY_CONF" "location = /.well-known/mcp.json {" "www alias route for mcp.json"
need_pattern "$HOWTO" "https://www.arleo.eu/.well-known/oauth-protected-resource/mcp" "howto documents www protected-resource alias"
need_pattern "$HOWTO" "https://www.arleo.eu/.well-known/mcp/server-card.json" "howto documents www server card alias"
forbid_pattern "$AUTH_MD" '"system.admin"' "auth.md canonical scope list excludes system.admin"
forbid_pattern "$AUTH_MD" '"site.admin"' "auth.md canonical scope list excludes site.admin"
forbid_pattern "$RESOURCE_JSON" '"system.admin"' "website protected-resource excludes system.admin"
forbid_pattern "$RESOURCE_JSON" '"site.admin"' "website protected-resource excludes site.admin"
