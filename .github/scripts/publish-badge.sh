#!/usr/bin/env bash
# Publishes a single shields.io badge JSON payload as a file on the orphan
# `badges` branch, preserving any other files already committed there (the
# branch hosts one payload per badge, e.g. source-loc.json and
# coverage-badge.json side by side).
#
# Usage: publish-badge.sh <name-in-branch> <path-to-payload>
set -euo pipefail

name="$1"
payload="$2"

export GIT_INDEX_FILE
GIT_INDEX_FILE="$(mktemp -u)"
trap 'rm -f "$GIT_INDEX_FILE"' EXIT

parent=()
if git ls-remote --exit-code --heads origin badges >/dev/null 2>&1; then
  git fetch --depth=1 origin badges
  git read-tree FETCH_HEAD
  if git cat-file -e "FETCH_HEAD:${name}" 2>/dev/null && \
     cmp -s "${payload}" <(git show "FETCH_HEAD:${name}"); then
    echo "Badge payload for ${name} is unchanged"
    exit 0
  fi
  parent=(-p FETCH_HEAD)
fi

blob=$(git hash-object -w "${payload}")
git update-index --add --cacheinfo "100644,${blob},${name}"
tree=$(git write-tree)

export GIT_AUTHOR_NAME="github-actions[bot]"
export GIT_AUTHOR_EMAIL="41898282+github-actions[bot]@users.noreply.github.com"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME"
export GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"
commit=$(printf 'chore: update %s badge\n' "${name}" | git commit-tree "$tree" "${parent[@]}")
git push origin "$commit:refs/heads/badges"
