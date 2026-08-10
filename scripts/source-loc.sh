#!/usr/bin/env bash
set -euo pipefail

SCC_VERSION="v3.7.0"
ROOT="${SOURCE_LOC_ROOT:-$(git rev-parse --show-toplevel)}"
SCC_BIN="${SCC_BIN:-$(go env GOPATH)/bin/scc}"

usage() {
  echo "usage: $0 --print | --badge-json PATH" >&2
}

mode="${1:-}"
output_path="${2:-}"
if [[ "${mode}" != "--print" && "${mode}" != "--badge-json" ]]; then
  usage
  exit 2
fi
if [[ "${mode}" == "--badge-json" && -z "${output_path}" ]]; then
  usage
  exit 2
fi

if [[ ! -x "${SCC_BIN}" ]]; then
  echo "scc ${SCC_VERSION} is required; run: go install github.com/boyter/scc/v3@${SCC_VERSION}" >&2
  exit 2
fi

expected_version="${SCC_VERSION#v}"
actual_version="$("${SCC_BIN}" --version | awk '{print $NF}')"
if [[ "${actual_version}" != "${expected_version}" ]]; then
  echo "scc version ${actual_version} found, want ${expected_version}" >&2
  exit 2
fi

mapfile -d '' go_files < <(git -C "${ROOT}" ls-files -z -- '*.go')
tracked_files=()
for path in "${go_files[@]}"; do
  [[ "${path}" == *_test.go ]] && continue
  tracked_files+=("${ROOT}/${path}")
done
if (( ${#tracked_files[@]} == 0 )); then
  echo "no tracked production Go files found under ${ROOT}" >&2
  exit 1
fi

stats="$("${SCC_BIN}" --format json "${tracked_files[@]}")"
read -r loc display_loc < <(
  python3 -c 'import json, sys; n = sum(row["Code"] for row in json.load(sys.stdin)); print(n, f"{n:,}")' <<<"${stats}"
)

if [[ "${mode}" == "--print" ]]; then
  echo "${loc}"
  exit 0
fi

mkdir -p "$(dirname "${output_path}")"
python3 - "${output_path}" "${display_loc}" <<'PY'
import json
from pathlib import Path
import sys

payload = {
    "schemaVersion": 1,
    "label": "production Go LOC",
    "message": sys.argv[2],
    "color": "00ADD8",
    "cacheSeconds": 300,
}
Path(sys.argv[1]).write_text(json.dumps(payload, separators=(",", ":")) + "\n")
PY

echo "Generated Production Go LOC badge payload: ${display_loc}"
