#!/usr/bin/env bash
set -euo pipefail

ROOT="$(mktemp -d)"
trap 'rm -rf "${ROOT}"' EXIT

git -C "${ROOT}" init -q
cat >"${ROOT}/main.go" <<'EOF'
package fixture

// Value returns a fixture value.
func Value() int {
  return 1 // Inline comments do not erase code.
}
EOF
cat >"${ROOT}/main_test.go" <<'EOF'
package fixture

func ignoredTestHelper() int {
  return 2
}
EOF
cat >"${ROOT}/untracked.go" <<'EOF'
package fixture

func ignoredUntrackedCode() int {
  return 3
}
EOF
git -C "${ROOT}" add main.go main_test.go

script="$(dirname "$0")/source-loc.sh"
got="$(SOURCE_LOC_ROOT="${ROOT}" "${script}" --print)"
if [[ "${got}" != "4" ]]; then
  echo "production Go LOC fixture = ${got}, want 4" >&2
  exit 1
fi

badge_json="${ROOT}/badge/source-loc.json"
SOURCE_LOC_ROOT="${ROOT}" "${script}" --badge-json "${badge_json}" >/dev/null
python3 - "${badge_json}" <<'PY'
import json
from pathlib import Path
import sys

payload = json.loads(Path(sys.argv[1]).read_text())
expected = {
    "schemaVersion": 1,
    "label": "production Go LOC",
    "message": "4",
    "color": "00ADD8",
    "cacheSeconds": 300,
}
if payload != expected:
    raise SystemExit(f"badge payload = {payload!r}, want {expected!r}")
PY

echo "Production Go LOC exclusions and badge payload verified"
