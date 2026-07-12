#!/usr/bin/env bash
set -euo pipefail

: "${SOAK_DURATION:=30s}"
: "${SOAK_CONCURRENCY:=4}"
: "${SOAK_WITH_DB:=0}"
: "${SOAK_SUMMARY_PATH:=/tmp/mcp-hugo-soak-summary.json}"

echo "Running local soak harness"
echo "  duration:     ${SOAK_DURATION}"
echo "  concurrency:  ${SOAK_CONCURRENCY}"
echo "  with_db:      ${SOAK_WITH_DB}"
echo "  summary_path: ${SOAK_SUMMARY_PATH}"

SOAK=1 \
SOAK_DURATION="${SOAK_DURATION}" \
SOAK_CONCURRENCY="${SOAK_CONCURRENCY}" \
SOAK_WITH_DB="${SOAK_WITH_DB}" \
SOAK_SUMMARY_PATH="${SOAK_SUMMARY_PATH}" \
go test ./internal/soak -run TestMutationBuildSoak -count=1 -v

echo
echo "Summary written to ${SOAK_SUMMARY_PATH}"
cat "${SOAK_SUMMARY_PATH}"
