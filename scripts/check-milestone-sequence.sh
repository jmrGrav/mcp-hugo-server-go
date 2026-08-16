#!/usr/bin/env bash
# Verifies every open, semver-titled milestone (vX.Y.Z) is exactly one
# patch ahead of the most recent closed one. Exists because the milestone
# after v1.8.9 was briefly created as "v2.0.0" (a minor-version jump) before
# being corrected to v1.9.0 — this repo's own release history has never
# skipped a patch, and there was no automated check to catch the mistake
# before issues/PRs had already piled up under the wrong milestone.
#
# Usage:
#   scripts/check-milestone-sequence.sh   human-readable status; exits
#                                         non-zero on drift
#
# Requires: gh (authenticated), jq.
set -euo pipefail

REPO="${GITHUB_REPOSITORY:-jmrGrav/mcp-hugo-server-go}"
SEMVER_RE='^v[0-9]+\.[0-9]+\.[0-9]+$'

milestones_json="$(gh api "repos/${REPO}/milestones?state=all" --paginate --jq '.')"

latest_closed="$(echo "$milestones_json" | jq -r --arg re "$SEMVER_RE" '
  [.[] | select(.state == "closed" and (.title | test($re)))]
  | sort_by(.title[1:] | split(".") | map(tonumber))
  | last
  | .title // empty
')"

if [[ -z "$latest_closed" ]]; then
  echo "No closed semver-titled milestone found — nothing to compare against. Skipping."
  exit 0
fi

IFS='.' read -r major minor patch <<<"${latest_closed#v}"
# This repo's release numbering is a plain decimal odometer, not strict
# semver: the patch digit increments by one each release and rolls over
# into the minor digit at 10 (v1.8.9 -> v1.9.0, not v1.8.10) — confirmed by
# the project's own history (v1.3.9->v1.4.0, v1.4.9->v1.5.0, ...). Never
# bump major here; this repo has stayed on v1.x.x throughout.
patch=$((patch + 1))
if [[ "$patch" -ge 10 ]]; then
  patch=0
  minor=$((minor + 1))
fi
expected_next="v${major}.${minor}.${patch}"

echo "Latest closed milestone: $latest_closed"
echo "Expected next milestone: $expected_next"

open_semver_titles="$(echo "$milestones_json" | jq -r --arg re "$SEMVER_RE" '
  [.[] | select(.state == "open" and (.title | test($re))) | .title] | .[]
')"

if [[ -z "$open_semver_titles" ]]; then
  echo "No open semver-titled milestone found. Skipping (nothing to validate yet)."
  exit 0
fi

drift=0
while IFS= read -r title; do
  [[ -z "$title" ]] && continue
  if [[ "$title" != "$expected_next" ]]; then
    echo "DRIFT: open milestone \"$title\" is not the expected next patch version ($expected_next)."
    drift=1
  else
    echo "OK: open milestone \"$title\" matches the expected next patch version."
  fi
done <<<"$open_semver_titles"

distinct_count="$(echo "$open_semver_titles" | sort -u | wc -l)"
if [[ "$distinct_count" -gt 1 ]]; then
  echo "DRIFT: more than one distinct open semver milestone exists ($distinct_count) — should be exactly one."
  drift=1
fi

exit "$drift"
