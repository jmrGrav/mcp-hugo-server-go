#!/usr/bin/env bash
# Lists every direct/indirect Go module dependency in go.mod that has a
# newer version available upstream. Advisory only — this script never fails
# the build on its own; it exists to make dependency staleness visible
# before a merge, not to block one (unrelated upstream releases shouldn't
# hold up an unrelated PR the way a real test failure should).
#
# Usage:
#   scripts/check-deps-fresh.sh          human-readable table
#   scripts/check-deps-fresh.sh --count  prints only the outdated count
set -euo pipefail

cd "$(dirname "$0")/.."

# `go list -u -m all` marks a module with `[vNEW]` appended when a newer
# version is available; the main module itself and up-to-date deps have no
# bracket suffix.
outdated="$(go list -u -m all 2>/dev/null | awk '$3 ~ /^\[.*\]$/ {print}')"

if [[ "${1:-}" == "--count" ]]; then
  if [[ -z "$outdated" ]]; then
    echo 0
  else
    echo "$outdated" | wc -l
  fi
  exit 0
fi

if [[ -z "$outdated" ]]; then
  echo "All Go module dependencies are at their latest available version."
  exit 0
fi

echo "Go module dependencies with a newer version available:"
echo "$outdated" | awk '{printf "  %-55s %-15s -> %s\n", $1, $2, $3}'
echo
echo "$(echo "$outdated" | wc -l) dependencies are behind. Run 'go get -u <module>' then 'go mod tidy' to update one, or 'go get -u ./...' + 'go mod tidy' for all (verify go build/vet/test/govulncheck after — an upstream major bump can be a breaking change)."
