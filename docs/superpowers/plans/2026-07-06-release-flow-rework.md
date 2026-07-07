# Release Flow Rework Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Simplify the repo workflow so production release happens only after a validated production deploy, with automated gates for changelog and machine-checkable README release metadata.

**Architecture:** Keep CI focused on PR/main quality checks, keep deploy focused on promoting a main commit to production, and add a dedicated release workflow that can only publish a tag/release for the commit already deployed successfully. Put release gates in `internal/releasecheck` so they are testable outside GitHub Actions.

**Tech Stack:** GitHub Actions, Go 1.25, `gh` CLI / GitHub REST API, existing `internal/releasecheck` helpers.

---

### Task 1: Add testable release gates in Go

**Files:**
- Modify: `internal/releasecheck/changelog.go`
- Create: `internal/releasecheck/readme.go`
- Modify: `internal/releasecheck/changelog_test.go`
- Create: `internal/releasecheck/readme_test.go`
- Create: `cmd/check-readme-release/main.go`

- [ ] Add README release policy tests first
- [ ] Implement a small `CheckReadmeReleasePolicy` helper that validates the dynamic latest-release badge/link contract
- [ ] Keep `CheckChangelogVersion` unchanged except for any small refactors needed by tests
- [ ] Add a tiny CLI entrypoint for the README gate
- [ ] Run targeted tests for `internal/releasecheck`

### Task 2: Rewire workflows around the new contract

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/deploy.yml`
- Modify: `.github/workflows/pre-release-smoke.yml`
- Create: `.github/workflows/release.yml`

- [ ] Remove tag-push behavior from `CI` so tag creation does not spawn duplicate quality runs
- [ ] Keep `Pre-release Smoke` manual-only so it is a tool, not an implicit state in merge/release flow
- [ ] Harden `Deploy to Production` so it only deploys refs that resolve to commits already reachable from `origin/main`
- [ ] Add a dedicated `Release` workflow that:
  - checks out the requested ref
  - validates `CHANGELOG.md` for the requested version
  - validates README release policy
  - verifies the requested ref equals the latest successful production deployment SHA
  - creates/pushes the tag if missing
  - creates the GitHub release
- [ ] Ensure permissions are minimal but sufficient (`contents: write`, `deployments: read`, `actions: read` if needed)

### Task 3: Update release and operator docs

**Files:**
- Modify: `docs/release-checklist.md`
- Modify: `README.md`
- Modify: `docs/operator-guide.md`

- [ ] Document the new three-step flow: merge -> deploy -> release
- [ ] Document that README is validated only for machine-checkable release metadata, not arbitrary prose freshness
- [ ] Document the exact workflows and when each one should be run

### Task 4: Verify end-to-end locally

**Files:**
- No new files expected

- [ ] Run `gofmt -w` on touched Go files
- [ ] Run `go test ./...`
- [ ] Run `go vet ./...`
- [ ] Run `go run ./cmd/check-changelog -version v1.3.4`
- [ ] Run `go run ./cmd/check-readme-release`
- [ ] Review the workflow YAML diffs for accidental privilege expansion or broken triggers

### Task 5: Commit in small logical chunks

**Files:**
- No new files expected

- [ ] Commit releasecheck helper/tests
- [ ] Commit workflow refactor
- [ ] Commit docs updates
