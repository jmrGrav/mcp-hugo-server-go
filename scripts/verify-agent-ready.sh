#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${MCP_BASE_URL:-https://mcp.arleo.eu}"
SITE_URL="${WWW_URL:-https://www.arleo.eu}"

echo "Legacy agent-ready wrapper"
echo "  BASE_URL=$BASE_URL"
echo "  SITE_URL=$SITE_URL"

"$(dirname "$0")/check-agent-ready.sh" "$BASE_URL"
SMOKE_LIVE=1 BASE_URL="$BASE_URL" SITE_URL="$SITE_URL" "$(dirname "$0")/smoke-agent-interop.sh"
