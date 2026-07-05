# Release Checklist

Run this checklist before any tag or release.

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
make check-changelog RELEASE_VERSION=<tag>
scripts/check-agent-ready.sh
SMOKE_LIVE=1 scripts/smoke-agent-interop.sh
```

Required gates:

- coverage stays at or above the CI threshold
- `CHANGELOG.md` contains an entry for the release tag
- `scripts/check-agent-ready.sh` passes
- `scripts/smoke-agent-interop.sh` passes in live mode
- the live MCP/Auth/Skill Discovery scan is at 7/7, or the blocker is documented explicitly before release
- the release tag points at the same commit that passed CI and was deployed; if fixes land after a tag, create a new patch release instead of moving the existing tag

Before creating the GitHub release, verify:

```bash
git rev-parse HEAD
git rev-list -n 1 <tag>
git log --oneline <tag>..HEAD
```

`git log --oneline <tag>..HEAD` must be empty for a final release tag. If it is not empty, the tag is stale.
