# Contributing

Thanks for contributing to `mcp-hugo-server-go`.

This repository is maintained with a release-hardening mindset: small changes,
clear scope, explicit evidence, and no hand-wavy reviews.

## Ground Rules

- Open or link an issue before changing behavior.
- Prefer one issue per branch and one coherent change per PR.
- Do not push directly to `main` unless you are the maintainer performing a
  deliberate repository-maintenance or documentation-only change.
- Do not merge with red or skipped required checks.
- Do not close an issue without proof:
  - tests,
  - logs or checks,
  - validated behavior.

## Recommended Workflow

1. Confirm the target issue and current milestone.
2. Create a dedicated branch:

   ```bash
   git checkout -b feat/issue-123-short-name
   ```

3. Make the smallest change that solves the actual problem.
4. Add or update regression tests.
5. Run the relevant checks locally.
6. Open a draft PR until the work is ready for review.
7. Link evidence in the PR description and in the issue when appropriate.

## Validation Expectations

Run the checks that match your change. For most behavior changes, the baseline is:

```bash
go test ./...
go test -race ./...
go vet ./...
staticcheck ./...
govulncheck ./...
```

For release-facing or discovery-facing changes, also verify the relevant docs
and helper checks:

```bash
go run ./cmd/check-changelog -version <tag-or-target-version>
go run ./cmd/check-readme-release
```

If you did not run a check, say so explicitly in the PR.

## Testing New Parameters and Response Fields

For any PR that adds a new opt-in parameter or a new documented response
field to an existing tool, include at least one test that (#607):

1. Calls the tool with the new parameter set, against a fixture shaped like
   realistic production content — not just the simplest fixture that makes
   the test pass. For example, a fixture exercising a bilingual site's
   per-language taxonomy behavior should use explicit `lang` suffixes
   (`index.fr.md`/`index.en.md`), matching how content actually exists on a
   real deployed site, not a bare `index.md`.
2. Asserts the *documented* behavior actually occurs — not just "the call
   succeeds," but that the specific field or effect described in the tool's
   own description is present and correct.

This closes the gap between "the tool description promises X" and "a test
proves X happens." A test suite that only exercises the easiest fixture
shape can pass while the documented behavior silently fails to trigger on
realistic input — treat that as a review blocker, not a nitpick.

## AI-Assisted Contributions

AI-assisted coding is allowed.

AI-assisted issue triage, review comments, and PR review are also allowed, but
they are held to the same standard as human review:

- every claim of fault must point to concrete evidence;
- every requested change must cite one of:
  - failing test,
  - reproducible runtime behavior,
  - exact file/line mismatch,
  - contract or spec violation,
  - log evidence,
  - security invariant breach.

Not acceptable:

- style-only nitpicks presented as bugs;
- vague "this feels wrong" review comments;
- speculative security claims without reproduction or code proof;
- AI-generated summaries that do not identify the actual failing path.

If you use AI substantially, disclose it in the PR and verify the output
yourself before requesting review.

## Review Standard

This project prefers evidence-driven review.

Good review feedback:

- names the failing invariant;
- cites the file and path involved;
- explains the user or operator impact;
- suggests a concrete correction direction.

Bad review feedback:

- broad "refactor this" without root cause;
- performance/security concerns without proof;
- contract changes without compatibility analysis.

## Issues

Before opening an issue:

- search existing open and closed issues;
- confirm the problem is still present on current `main`;
- strip secrets, tokens, cookies, and private config values from evidence.

Prefer issues that include:

- affected version or commit;
- reproduction steps;
- observed behavior;
- expected behavior;
- impact;
- likely root cause if known.

## Pull Requests

PRs should explain:

- what changed;
- why it changed;
- what evidence proves it;
- what compatibility or operator risks remain.

Keep PRs small enough that a reviewer can verify them end to end.

## Documentation and Contracts

If a change touches public behavior, review the relevant docs:

- `README.md`
- `docs/tools.md`
- `docs/mcp-contract.md`
- `docs/operator-guide.md`
- `docs/release-checklist.md`
- wiki pages if the operator workflow or public contract changed

Do not let code, changelog, README, and wiki drift apart.

## Security

- Never commit secrets.
- Never post bearer tokens, OAuth codes, cookies, or Cloudflare tokens in
  issues, PRs, wiki pages, or screenshots.
- Use private reporting for vulnerabilities as documented in [SECURITY.md](SECURITY.md).

## Communication

Preferred project languages:

- French
- English
