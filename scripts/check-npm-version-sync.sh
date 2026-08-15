#!/usr/bin/env bash
# Compares manifest.json's version (the source of truth deploy.yml's
# release-notes gate already enforces npm/package.json matches at release
# time) against the version actually live on the npm registry. Advisory
# only — this never fails a build on its own; it exists to catch a missed
# or failed npm publish (v1.8.5-v1.8.8 went unpublished for 4 releases
# because publish-npm.yml used to be a separate manual step nobody
# re-triggered) before it's discovered by a user, not to block anything.
#
# Usage:
#   scripts/check-npm-version-sync.sh   human-readable status
set -euo pipefail

cd "$(dirname "$0")/.."

PACKAGE="@jmrgrav/mcp-hugo-server-go"

manifest_version="$(node -p "require('./manifest.json').version")"
npm_version="$(npm view "$PACKAGE" version 2>/dev/null || true)"

if [[ -z "$npm_version" ]]; then
  echo "Could not reach the npm registry to look up $PACKAGE's published version. Skipping (not treated as drift)."
  exit 0
fi

if [[ "$manifest_version" == "$npm_version" ]]; then
  echo "npm is in sync: $PACKAGE@$npm_version matches manifest.json."
  exit 0
fi

echo "npm version drift detected:"
echo "  manifest.json (this repo):  $manifest_version"
echo "  npm registry (published):   $npm_version"
echo
echo "If manifest.json is ahead, the most recent release's automatic npm publish (deploy.yml's publish-npm job) either hasn't run yet, is still propagating, or failed. Re-run it manually: gh workflow run publish-npm.yml -f version=v$manifest_version (after confirming the GitHub release for that tag has its binaries attached)."
exit 1
