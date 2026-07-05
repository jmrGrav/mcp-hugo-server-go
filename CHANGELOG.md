# Changelog

All notable changes to this project are documented here.

## [Unreleased]

### Added
- Regression tests for Claude.ai scope clamping, ChatGPT write-scope boundaries, and IsItAgentReady auth metadata.
- Release checklist guard to verify that the release tag points at the same commit that passed CI and was deployed.
- `check-changelog` release helper to fail releases when `CHANGELOG.md` is missing the target version.

### Changed
- Documentation now describes the current canonical scope model: `content.read`, `content.write`, and `site.admin`; legacy `system.admin` normalizes to `site.admin`.

### Fixed
- OAuth scope clamping now logs the requested and granted scopes without exposing secrets.
- Anonymous/read tool-boundary smoke checks were tightened so public access cannot silently gain authenticated tools.

## [v1.2.10] - 2026-07-05

### Changed
- Collapsed the former standalone `system.admin` tier into `site.admin`; `system.admin` remains accepted as a legacy alias.
- Simplified the active scope hierarchy to anonymous, `content.read`, `content.write`, and `site.admin`.

### Fixed
- Claude.ai authorization no longer fails with `invalid_scope` when it requests a wider historical scope list than the registered client ceiling.
- Admin and integrity tools, including `check_sri_versions`, are now served under `site.admin`.

## [v1.2.9] - 2026-07-05

### Fixed
- Added Claude.ai's observed `https://claude.ai/api/mcp/auth_callback` redirect URI to the admin client configuration path.

## [v1.2.8] - 2026-07-05

### Fixed
- Return a proper OAuth challenge for unauthenticated `/mcp` requests when OAuth is enabled, preventing authenticated clients from caching anonymous tool lists.

## [v1.2.7] - 2026-07-05

### Added
- Dynamic Client Registration scope inheritance from pre-registered clients when redirect URI policy matches.

### Fixed
- Hardened OAuth redirect handling and agent discovery metadata.
- Resolved CodeQL redirect findings with validated redirect sinks and documentation.

## [v1.2.6] - 2026-07-05

### Fixed
- Corrected `resource_documentation` metadata and added regression tests for the AgentReady scanner path.
- Added a regression test for the Auth.md backtick URL extraction issue.

## [v1.2.5] - 2026-07-05

### Fixed
- Resolved remaining AgentReady blockers for API/Auth/MCP/Skill Discovery 7/7.

## [v1.2.4] - 2026-07-05

### Fixed
- Added `register_uri` to agent auth discovery metadata.

## [v1.2.3] - 2026-07-05

### Added
- `scripts/verify-agent-ready.sh` for post-deploy discovery validation.
- RFC compliance documentation with live-tested discovery endpoint annotations.

## [v1.2.2] - 2026-07-05

### Fixed
- Applied `gofmt` to resolve CI formatting violations.

## [v1.2.1] - 2026-07-05

### Fixed
- Resolved remaining v1.2.0 follow-up issues around OAuth, client compatibility, and AgentReady discovery.

## [v1.2.0] - 2026-07-05

### Added
- Agent interop and AgentReady validation scripts.
- Secret scanning jobs for gitleaks and trufflehog in CI.

### Fixed
- Interop, security, and correctness issues found during the v1.2.0 hardening milestone.
- Deploy script now injects version ldflags so live binaries can report build version.

## [v1.1.0] - 2026-07-04

### Security
- Require `site.admin` or `system.admin` Bearer token on `POST /agent/identity/verify` — anonymous callers could previously self-claim and escalate to `content.read` ([#71](https://github.com/jmrGrav/mcp-hugo-server-go/issues/71))

### Added
- `internal/fileutil` package with shared `AtomicWrite`, `AtomicWriteBytes`, and `BoolPtr` helpers (#77)
- `Service.PurgeExpired()` cleans expired auth codes and agent registration maps every 5 minutes (#72, #74)
- Hourly reset of the per-IP OAuth allocation counter to prevent unbounded growth (#73)
- `security_contact` config field populates `/.well-known/security.txt` per RFC 9116
- `Canonical` line in `security.txt` falls back to `oauth.issuer` when `site_url` is blank (#94)
- Makefile with `build`, `test`, `cover`, `lint`, `vet`, `vuln`, and `check` targets (#96)
- API reference table in README (#98)
- Agent identity verification flow documented in README (#88)
- `security_contact` documented in README (#87)

### Changed
- `Version` in `internal/server` is now a `var` set at build time via `-ldflags` (defaults to `"dev"`) (#79)
- CI: staticcheck pinned to `2025.1.1` (#82)
- CI: `govulncheck` step added (#83)
- CI: `go build ./...` step added (#84)
- CI: coverage gate replaced `python3` with `awk` (#97)

### Fixed
- `handleSecurityTxt` no longer emits a relative `Canonical:` line when `site_url` is empty (#94)

## [v1.0.0] - 2026-06-01

Initial public release.

- Streamable HTTP MCP transport at `/mcp`
- OAuth 2.0 / PKCE authorization code flow
- Initial 5-tier scope hierarchy: anonymous → content.read → content.write → site.admin → system.admin
- Agent identity registration and claim flow
- SQLite and JSON token persistence backends
- Hugo content tools: `create_page`, `update_page`, `delete_page`
- Site admin tools: `build_site`, `preview_build`, `run_post_build_hooks`, `upload_asset`
- System tools: `check_sri_versions`
- PathGuard symlink and path traversal protection
- RFC 9116 security.txt, RFC 9116 robots.txt, llms.txt, MCP server card, agent card
