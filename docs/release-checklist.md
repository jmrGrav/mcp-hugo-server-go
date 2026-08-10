# Release Checklist

Run this checklist before triggering `deploy.yml` with `release_version` set.

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
- `npm/package.json` and `manifest.json` already carry the release version
- if fixes land after a tag, create a new patch release instead of moving the existing tag

Recommended operator sequence:

```bash
gh workflow run ci.yml                      # implicit on PR/main merge
gh workflow run deploy.yml -f ref=main -f release_version=<tag>
```

`deploy.yml` is the only place that builds, deploys, tags, and creates the GitHub release —
one call does all four, in that order. It gates on `CHANGELOG.md`/`npm/package.json`/
`manifest.json` already carrying `<tag>` *before* building or touching production, and fails
fast if any of them don't match — merge the changelog/version-bump PR first. Omitting
`release_version` deploys the ref to production without cutting a release (an ad-hoc hotfix
ahead of the next versioned release, for example).
