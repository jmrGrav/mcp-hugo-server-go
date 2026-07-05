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
scripts/check-agent-ready.sh
```

Required gates:

- coverage stays at or above the CI threshold
- `scripts/check-agent-ready.sh` passes
- the live MCP/Auth/Skill Discovery scan is at 7/7, or the blocker is documented explicitly before release

