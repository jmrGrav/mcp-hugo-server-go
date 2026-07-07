# Release Checklist

Run this checklist before triggering the `Release` workflow.

```bash
go test ./...
go test -race ./...
go vet ./...
staticcheck ./...
govulncheck ./...
gitleaks detect --no-banner --redact --source .
go test ./... -coverprofile=coverage.out
go tool cover -func=coverage.out | tail -n 1
go run ./cmd/check-changelog -version <tag>
go run ./cmd/check-readme-release
make check-changelog RELEASE_VERSION=<tag>
make check-readme-release
scripts/check-agent-ready.sh
SMOKE_LIVE=1 scripts/smoke-agent-interop.sh
```

Required gates:

- coverage stays at or above the CI threshold
- `CHANGELOG.md` contains an entry for the release tag
- `README.md` keeps dynamic release metadata (`Latest Release` badge + `releases/latest` link)
- `scripts/check-agent-ready.sh` passes
- `scripts/smoke-agent-interop.sh` passes in live mode
- the live MCP/Auth/Skill Discovery scan is at 7/7, or the blocker is documented explicitly before release
- the release target is the current `origin/main` HEAD
- the release target already has a successful `production` deployment record
- if fixes land after a tag, create a new patch release instead of moving the existing tag

Recommended operator sequence:

```bash
gh workflow run ci.yml                      # implicit on PR/main merge
gh workflow run deploy.yml -f ref=main
gh workflow run release.yml -f version=<tag> -f ref=main
```

The `Release` workflow is the only place that should create the final tag and GitHub release.
It will fail if the requested ref is stale, not deployed, or missing release metadata.
