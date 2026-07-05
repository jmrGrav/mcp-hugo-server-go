#!/usr/bin/env bash
# lint-registry.sh — validate an oauth-clients.yaml registry file.
# Usage: scripts/lint-registry.sh [path/to/oauth-clients.yaml]
# Defaults to docs/oauth-clients.example.yaml when no argument is given.
set -euo pipefail

REGISTRY="${1:-docs/oauth-clients.example.yaml}"
KNOWN_SCOPES=("content.read" "content.write" "site.admin" "read" "write" "admin" "system.admin")
VALID_HTTPS_RE='^https://'
ERRORS=0

fail() { echo "FAIL $*" >&2; ((ERRORS++)) || true; }
pass() { echo "OK   $*"; }

if [[ ! -f "$REGISTRY" ]]; then
  fail "registry file not found: $REGISTRY"
  exit 1
fi

# 1. YAML parses cleanly
if ! python3 -c "import sys, yaml; yaml.safe_load(open('$REGISTRY'))" 2>/dev/null; then
  fail "$REGISTRY: invalid YAML"
  exit 1
fi
pass "$REGISTRY: valid YAML"

# Extract fields using python3 + yaml
python3 - "$REGISTRY" <<'PYEOF'
import sys, yaml, re, os

KNOWN_SCOPES = {
    "content.read", "content.write", "site.admin",
    "read", "write", "admin", "system.admin",
}
ERRORS = 0

def fail(msg):
    global ERRORS
    print(f"FAIL {msg}", file=sys.stderr)
    ERRORS += 1

def ok(msg):
    print(f"OK   {msg}")

path = sys.argv[1]
data = yaml.safe_load(open(path))
clients = data.get("clients", [])

if not clients:
    fail(f"{path}: no clients defined")
    sys.exit(1)
ok(f"{path}: {len(clients)} client(s) defined")

for c in clients:
    cid = c.get("id") or c.get("client_id") or "(unknown)"

    # 2. Every client must have at least one redirect URI
    uris = c.get("redirect_uris", [])
    if not uris:
        fail(f"client {cid!r}: no redirect_uris")
    else:
        ok(f"client {cid!r}: {len(uris)} redirect URI(s)")

    # 3. All redirect URIs must be HTTPS (or localhost for dev)
    for uri in uris:
        if not (uri.startswith("https://") or
                uri.startswith("http://localhost") or
                uri.startswith("http://127.0.0.1")):
            fail(f"client {cid!r}: non-HTTPS redirect URI: {uri!r}")
        else:
            ok(f"client {cid!r}: URI scheme OK: {uri}")

    # 4. Scope values must be from the known set
    scope_val = c.get("scope") or ""
    scopes_list = c.get("scopes") or []
    all_scopes = list(filter(None, scope_val.split())) + scopes_list
    for s in all_scopes:
        if s not in KNOWN_SCOPES:
            fail(f"client {cid!r}: unknown scope {s!r} (known: {sorted(KNOWN_SCOPES)})")
        else:
            ok(f"client {cid!r}: scope {s!r} is valid")

    # 5. Warn if claude.ai client uses broad wildcard (security note)
    if "claude" in cid.lower():
        for uri in uris:
            if uri == "https://claude.ai/*":
                print(f"WARN client {cid!r}: wildcard {uri!r} — consider using exact URIs for Claude", file=sys.stderr)

sys.exit(min(ERRORS, 1))
PYEOF

if [[ $? -ne 0 ]]; then
  echo "lint-registry: $REGISTRY has errors" >&2
  exit 1
fi

echo "lint-registry: $REGISTRY OK"
