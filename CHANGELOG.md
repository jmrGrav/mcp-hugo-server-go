# Changelog

All notable changes to this project are documented here.

## [v1.7.9] - 2026-08-10

Follow-up fixes from the 2026-08-09 external audits of v1.7.8 (Sol, ChatGPT, Claude), delivered as 15 independently-reviewed PRs. Each PR went through an `advisor`-level review pass before merge, catching a real cross-tenant vulnerability and a live production data-integrity bug before they could be exploited/hit.

### Security
- **Cross-tenant isolation for plans, rollback snapshots, and previews** (#932, #934): server-held `apply_content_plan`/`apply_bundle_plan` plans, `rollback_change` snapshots, and preview sessions were reusable by any authenticated `write`-scoped bearer, not just the caller that created them. Any principal could consume, apply, or revoke another principal's ephemeral state. Fixed by binding all of it to the requesting principal's caller key (`internal/caller`); confirmed via red/green testing against the unpatched code that the attack — a second bearer applying/rolling back a first bearer's content — genuinely succeeded before the fix.
- **`test_content.owner` demoted to advisory-only metadata** (#927, #936): the `delete_page`/`delete_page_asset` destructive-quota exemption keyed on a caller-supplied `owner` string matching frontmatter's `test_content_owner` — a capability-by-label pattern with no binding to authenticated identity. The exemption is removed entirely; `owner` remains an advisory label for filtering/audit correlation only.
- **Inline-event-handler detector bypass in `inspect_rendered`/`inspect_preview`** (found during #929's review): the `security_inline_event_handlers` check's stylesheet-preload allowlist used `strings.Contains` instead of an exact match, so an `onload` handler embedding the allowlisted snippet inside a longer malicious payload (e.g. exfiltrating `document.cookie`) would pass as benign. Fixed with an exact-match allowlist restricted to the pattern confirmed present in the live theme.
- **`operator.internal_scopes` discovery drift** (#933, #937): `discovery.go`'s fallback `auth.md` template still advertised `operator` as `["read", "write"]` after the static file and runtime model had already collapsed to `write` implying `read` — zero live risk (the fallback only fires when `access_profiles` is absent from the served file), but a latent contract inconsistency, now aligned and regression-tested.

### Correctness
- **`rollback_change` could repopulate a bilingual page's in-memory index from the wrong language** (#911, #920): the post-restore index rebuild used a language-blind `GetBySlug` lookup, which could resolve a sibling translation instead of the language actually restored, poisoning `get_page_frontmatter`/`get_page_for_edit` until the next full reindex. Now uses `GetBySlugLang`/`GetDefaultBySlug`. Found in the same review pass: `update_page` had the identical typed-field staleness gap (`Date`/`Draft`/`PublishDate`/`ExpiryDate` left unsynced after a write) — closed separately (#921, #922).
- **`get_storage_health` false-flagged live hero images as orphaned** (#912, #919, #929): a bare-slug lookup against the section-qualified source index misreported nearly every pre-existing flat-named hero image (`static/images/{slug}-featured.jpg`) as orphaned — the advisory remediation text pointed callers at `delete_page_asset`, which would have deleted live, referenced images. Fixed with an explicit frontmatter-reference check that takes priority over the slug-matching heuristic, including `.png` variants.
- **`search_content` silently normalized invalid input instead of rejecting it** (#915, #918, #923): unknown `sort`/`order` fell back to defaults, unknown `language` silently returned empty results, and over-max `limit` was only caught by schema validation (an unstructured error). All four now return explicit `invalid_params`.
- **Contradictory slug/lang combinations and diagnostic false positives** (#913, #914, #929): `create_page`/read tools now reject a slug/lang combination that can't jointly resolve instead of guessing; preview-prefixed link/image inspection no longer misreports broken links inside a preview build; `verify_publication` correctly forces `sitemap_present`/`feed_present` to `false` for intentionally-unpublished content; a consumed `apply_bundle_plan` hitting `build_in_progress` now returns replan-specific guidance instead of a generic retry hint.

### Contract / documentation convergence
- **`read`/`write` are the only canonical scopes, everywhere** (#917, #925, #926, #928, #930, #931, #938): `auth.md`, `docs/access-model.md`, `docs/mcp-contract.md`, `docs/agent-tool-matrix.md`, `docs/operator-guide.md`, `docs/tools.md`, and every tool description's `Requires ...` wording converged on the live two-scope model — `read` is full source-aware visibility (drafts included), `write` implies `read`. Legacy strings (`content.read`, `content.write`, `site.admin`, `system.admin`, `reader`, `mcp`, `admin`) remain accepted only as compatibility aliases, never advertised as canonical. `docs/agent-tool-matrix.md`'s workflow examples now recommend `publish_changes` (build + verified publication) over a bare `build_site` call. A new CI regression test (`TestContractToolDescriptionsAvoidLegacyScopesAndPublicSafeWording`) fails the build if legacy wording reappears in any published tool description.
- **`meta.content_provenance` on untrusted site-source payloads** (#925, #935): source-carrying read/edit-context tools (`get_page_markdown`, `get_page_frontmatter`, `get_page_for_edit`, `build_agent_context`, `export_agent_context`, `diff_page` when it falls back to raw source) now mark their response envelope with `content_provenance: "site_source_untrusted"`, so an agent can distinguish site-controlled content from instruction-like authority.

### Internal / coverage
- Multi-principal end-to-end isolation harness (#932, #934), `internal/caller` coverage (#939), previously low-covered packages raised with meaningful (not just line-count) tests — `previewstore`'s owner-scoped `ListOwned`/`RevokeOwned`/`RevokeAllOwned` (#941, #942), `fileutil`'s atomic-write/TOCTOU-symlink-rejection paths, `contentmodel`, and `buildinfo` (#940, #942).
- Fixed a `staticcheck` regression (S1017) that was failing the push-triggered CI `test` job on every push to `main` after #940 merged, before the actual test suite could run (#943).

## [v1.7.8] - 2026-08-09

Nine issues from four rounds of live external audits, delivered as 8 independently-reviewed PRs. Each PR went through its own opus-level review pass before merge, catching several real bugs (a data race, a validation-detail-masking bug, and an incomplete quota-exemption scope) before they shipped.

### Security
- **Data race in `upload_page_asset`'s and `delete_page_asset`'s pre-lock eligibility checks** (#887 regression, #901): both tools' pre-lock `validateBundleSlug` read `hugosite.SourceIndex` without holding `hugosite.ContentMu`, racing a concurrent `delete_page`/`create_page`'s write under the full lock. CI's own `-race` run caught the `upload_page_asset` instance; the identical pre-existing pattern in `delete_page_asset` was found and fixed in the same pass. Both now take a brief `RLock`/`RUnlock` around the pre-lock check; the authoritative existence gate (an `os.ReadFile` under the write lock) is unchanged. New concurrent regression tests exercise both races directly under `-race -count=1`.
- **Schema-level `enum` validation bypassed the structured-error pipeline** (#892): the MCP SDK validates arguments against a tool's published JSON Schema *before* calling the handler — an out-of-enum value (e.g. an invalid `response_mode`) never reached this server's own `internal/toolcontract` error pipeline, so it produced a flat, unstructured error text instead of the `StructuredContent`/error-code shape every other error path already had. Migrated schema-level `enum` constraints to the equivalent handler-level validation that already existed (`ResolveResponseMode`, etc.) across every affected read tool, `search_pages`'s `match`, and `generate_hero_image`'s `style`/`accent`. New `TestContractInvalidEnumInputsReturnStructuredError` asserts every migrated field produces a recognized structured error code, proven fail-red against a reintroduced schema-level `enum`.
- **Reserved-slug denylist gap** (#890): `create_page` now rejects `404`/`index`/`_index` as any path segment of a slug (exact-per-segment match, verified not to false-positive on e.g. `posts/404-not-found-explained`), closing a way a caller could shadow the site's own reserved pages.
- **`plan_content_change`/`plan_bundle_change` bypassed taxonomy-length validation** (#904): both share `resolvePlanOperations` for `add_tag`/`add_category`, which never enforced the same tag/category length cap (`validateTaxonomyTerms`, #886) `create_page`/`update_page` already had — a plan could still produce an arbitrary-length tag. Fixed once in the shared function, checked at plan time. The reserved-slug half of the original report was confirmed non-reproducing (neither plan path can create a new page, so there's no slug-creation surface to bypass) — closed with confirming regression tests instead of new logic.

### Agent ergonomics
- **`update_page`'s `revision_conflict`/`bundle_conflict` errors now echo the fresh revision** (#893): `data.current_revision`/`data.current_bundle_revision` let a caller retry immediately after a conflict without an extra read round-trip.
- **`validate_site`/`validate_frontmatter` expose `test_content_owner` and an `owner` filter** (#894): a structured `test_content` field (slug + owner) alongside the existing `test_content_slugs`, plus an optional `owner` filter so an agent running alongside others can safely enumerate only its own disposable test content. A review-found bug in the initial implementation — the owner filter was masking *invalid* pages from the validation detail rows while still counting them as invalid — was fixed in the same PR before merge.
- **`generate_hero_image` gains `dry_run`** (#897): previously the only write tool without one. Returns the full output contract (path/public_path/source_key/delete_slug/delete_scope/delete_filename) without touching disk or making a network call in either the local-render or external-API path.
- **`get_page` gains `max_body_chars`** (#896), matching `build_agent_context`/`get_page_for_edit`'s existing pattern: omitted preserves the full body (unchanged default); an explicit positive value truncates `html` with a warning; 0/negative is rejected.
- **`delete_page`/`delete_page_asset` gain an `owner` param exempting caller-owned `test_content` deletes from the destructive quota** (#895): a bilingual test bundle's cleanup can need 4-5 destructive calls across both tools, which used to hit `DestructivePerMin` mid-cleanup. Exempt only when the target's frontmatter has `test_content: true` AND its recorded `test_content_owner` exactly matches `owner`; every other deletion (real content, mismatched/absent owner, or a `scope:"generated"` delete with no owning bundle) still consumes the quota exactly as before.
- **`create_page`'s `lang` param gains a true-reject path** (#899): the original #891 fix could only warn on an unrecognized language, since the server had no authoritative configured-language set to reject against. Adds an opt-in `config.ConfiguredLanguages`: when set, an out-of-set `lang` is rejected (`invalid_params`, no file written) and `get_capabilities.languages.available` reports exactly that set. Unset (the default for every existing deployment) preserves the original warn-only behavior byte-for-byte; confirmed `update_page` needs no equivalent guard, since it can never mint a new file.
- **`apply_bundle_plan`/`rollback_bundle` wired into `get_mutation_status`** (#880): both already wrote to the same client-scoped idempotency store `apply_content_plan`/`rollback_change` use; they were just missing from the tool-name allowlist `get_mutation_status` checks against.
- **Documented the two `get_storage_health` orphaned-generated-asset classes and their one-time cleanup procedure** (#881): a historical hero-image backlog (predating the #606 delete-cascade fix) versus a generate-without-attach gap (closed by #897's `dry_run`). No new orphans form going forward; the existing ones are a one-time backlog to clean up via `delete_page_asset(scope=generated)`.
- **Offset/idempotency-key/taxonomy-term input hardening** (#885, #886, #888): negative `offset` now rejected across every paginated read tool; `idempotency_key` length/charset validated consistently across all 9 mutation tools that accept one (including a review-found gap on `rollback_change`); tag/category values capped at 100 runes.

### Also (theme/content repo, no Go change)
- **`inspect_rendered`'s `security_unsafe_urls`/`security_inline_event_handlers` findings in theme chrome** (#884): confirmed inert (static markup, no user-controllable input), fixed by moving inline `on*=`/`javascript:` handlers in `hugo-site`'s LoveIt theme overrides (search toggle/clear, theme switch, language switcher, back link, all 25 share-network buttons) into one small bound-listener JS file, same selectors, zero behavior change. `security_unsafe_urls` now passes site-wide.

## [v1.7.7] - 2026-08-05

Full security + agent-ergonomics hardening pass (16-item cycle, #866), delivered as 9 independently-reviewed PRs (#867-879). Every PR was reviewed by a separate agent with no access to the implementer's reasoning before merge; several real bugs were caught and fixed in review rather than shipped — see notes below.

### Security
- **Cross-language content leak in `ResolveWithLang`** (#867): a self-contradictory request (e.g. an `/en/` URL paired with `lang="fr"`) resolved straight through to the English page instead of correctly reporting not-found, leaking wrong-language content through `get_page_markdown`/`get_page_frontmatter`/`build_agent_context`/`get_page_for_edit`.
- **Shared URL/path validator for URL-like frontmatter** (#855): generalizes the `featured_image` charset-allowlist fix from v1.7.6 into one `internal/urlpolicy` package (`LocalOnly`/`ExternalAllowed` policies), rejecting `javascript:`/`data:`/`vbscript:`/`file:`/protocol-relative URLs and HTML metacharacters consistently rather than per-field. Caught in review: the `ExternalAllowed` branch initially had no metacharacter guard at all — closed before merge, though not yet reachable from any live caller.
- **Preview session hardening — single-use entry tokens + per-caller/disk limits** (#853/#871): the preview entry token is now retired once its session is confirmed in active use (activation-gated, not naively invalidated on first exchange, so legitimate reload/second-tab/retry still work), and `create_preview` now enforces a configurable per-caller active-preview cap and global disk-usage cap, both with explicit rejection signals rather than silent limits.
- **Rendered-output security checks now flag `vbscript:` URLs** (CodeQL alert #11), closing an incomplete-URL-scheme-check finding alongside the existing `javascript:`/`data:` checks shared by `inspect_rendered` and the new `inspect_preview`.
- **Data race in `previewstore.Store.GetBySession`** (found independently twice, in #868 and again in a stale-based #870/#871): `SessionToken`/`sessionActivated` were read outside the mutex while `EstablishSession` mutates them under lock — fixed with a snapshot-under-lock pattern, verified clean under `go test -race -count=30`.
- **CI contract snapshots + adversarial integration matrix** (#862): a golden-file snapshot of every published tool's name/scope/description/schema now fails CI on unintended drift; new adversarial tests exercise title-XSS, malicious `featured_image`, bilingual mutation isolation, and early-error rate-limit accuracy through the real MCP `tools/call` boundary. Caught in review: one adversarial test asserted only `IsError=true`, so it stayed green even with its target validator fully neutered — tightened to assert on the specific rejection reason.
- **`update_page` gains an optional `expected_bundle_revision` guard** (#857 AC3): rejects a mutation if a sibling translation or bundle-local asset changed since the caller last read the bundle, reusing the `contentmodel.BundleRevision` primitive. Purely additive — omitting the field is a byte-for-byte no-op versus the previous behavior.

### Agent ergonomics / observability
- **`bundle_revision`** exposed on `get_page_for_edit`, covering an entire page bundle (all translations + bundle-local assets) as one optimistic-concurrency unit (#857).
- **Bundle-level transactional mutations** — `plan_bundle_change`/`apply_bundle_plan`/`rollback_bundle` update a multilingual bundle atomically: validates every translation before writing any, rolls back already-written files on a mid-apply failure (#854). Documented as runtime, not crash, atomicity — no POSIX multi-file rename guarantee.
- **`build_site` reports stage- and page-aware detail**: `data.stages` (Hugo build, output swap, source/public index reload, per-callback outcomes) and `data.pages` (included/excluded_drafts/deleted_outputs), purely additive to the existing response (#858).
- **`get_capabilities`** — machine-readable runtime limits and feature discovery (#859).
- **`get_storage_health`** — advisory-only detection of orphaned generated assets, expired preview residue, and source/index/public inconsistencies (#861).
- **Contract observability**: `request_id`/`duration_ms` on every response envelope, explicit `changed:false` no-op signal on `update_page` (#860).
- **`rate_limit_remaining`** converged on one canonical source of truth between the root scalar and the structured bucket (#852); a review-added regression test closed a gap where only the error path, not the success path, was pinned.
- **Git dirty-state classified by safe resource class** with a conservative catch-all bucket, so an unrecognized path can never be mislabeled safe (#864).
- **`check_ai_readiness`** gains an explicit `lang` parameter, matching the other multilingual read tools (#850, closed via #879).
- **`inspect_preview`** (formerly scoped as `inspect_rendered_page` for previews) — rendered-inspection for draft/test-content pages via their isolated preview build, without needing to publish first (#863).
- **Generated hero-image delete contract**: `generate_hero_image`'s response now feeds directly into `delete_page_asset`'s arguments (`source_key`/`delete_slug`/`delete_scope`/`delete_filename`), closing a previously-manual cleanup gap (#845).
- Sharpened feed/recent/readiness/get_page semantics and cost-signaling documentation (#865); clarified sitemap/broken-link scan contracts with typed document-class counters (#848/#849); explicit `intentionally_unpublished`/`no_hooks_configured` lifecycle states (#847/#851).

### Internal
- Every PR in this cycle went through an independent review pass (separate agent, no access to implementer reasoning) before merge, in addition to the required CI gates.

## [v1.7.6] - 2026-08-03

Batch of five hardening/correctness fixes from independent live-agent audits (Sol/OpenAI, cross-checked by Claude sessions) against production, all reviewed and, where the audit's own proposed fix fell short, corrected before merge — see #830/#831 for the two issues resolved earlier in this cycle without a Go change (stored XSS and preview-token/security-header leak, the latter partly closed here too via #844).

### Fixed
- **`update_page` could silently corrupt a bilingual page's title/body** (#829): the handler resolved `existing` via the language-agnostic `SourceIndex.GetBySlug` *before* language resolution ran, then built the updated record from that pre-resolution value — so updating one language's non-title/body fields (e.g. `description`) could overwrite that language's `title`/`body` with whichever translation `GetBySlug` happened to return internally. Now re-resolves via `GetBySlugLang` after the language is known. The originally shipped regression test exercised the one language direction where the bug happened not to trigger (`GetBySlug` coincidentally matched); strengthened to assert both directions, verified red against the pre-fix code before merging.
- **`update_page`'s `featured_image` couldn't be cleared, and accepted unsafe values at write time** (#825, #835): `description`/`featured_image` are now `*string`, distinguishing "omitted" (leave unchanged) from "explicit empty string" (clear the frontmatter key) — previously both looked identical. Separately, `featured_image` write-time validation blocked path traversal and disallowed URL schemes (`data:`, `javascript:`, `http(s)://`) but never bounded the character set, so a value like `/img.jpg" onerror="alert(1)` or `/images/<script>.jpg` passed every check and was written verbatim into frontmatter the site theme renders into HTML attributes/CSS `url()` without re-escaping. Added a charset allowlist (`^/[A-Za-z0-9._~/-]+$`) matching the containment already applied to `title`, plus negative regression tests for the injection class specifically (probe-confirmed against the pre-fix validator before adding the fix).
- **Preview builds leaked their bearer token via share-button URLs, and carried a contradictory robots signal** (#831): `create_preview` URLs embed their access token in the path (`/preview/<id>/<token>/...`), but the LoveIt theme's share-button partial built every share link from `.Permalink`, handing the token to X/Threads/Facebook/Telegram/etc. in a URL parameter on click. Fixed entirely in the `hugo-site` theme/content repo (no Go change): the share partial and the page-level robots `<meta>` tag now both gate on `Site.BaseURL` containing `/preview/`, a signal already reliably present on every preview build via the existing `--baseURL` flag — no new `--environment` flag needed. Separately, `internal/previewstore.Store.HTTPHandler` (the code that actually serves `/preview/` requests — not an OpenResty/host-level layer, correcting the original issue's own wrong assumption about where these responses come from) only set `X-Robots-Tag`; now also sets `Cache-Control: private, no-store, max-age=0`, `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`, and a minimal `Content-Security-Policy: frame-ancestors 'none'`, since the token embedded in the URL is the sole access control for that content (#844, deployed and verified live ahead of this release).
- **`test_content` detection relied only on a legacy slug-prefix heuristic** (#832): `test_content_slugs` reporting checked for `mcp-audit-`/`test-audit-`/`codex-` slug prefixes instead of reading the authoritative `test_content: true` frontmatter marker (written by `create_page`'s own `test_content` option since #584) — a page carrying the real marker under an ordinary slug was invisible to detection. Now checks the frontmatter marker first, falling back to the slug-prefix heuristic only as an additional (not replacement) signal.
- **`create_page`'s `test_content.ttl_hours` accepted 0 or negative values, silently defaulting to 24h instead of rejecting them** (#833): `ttl_hours` is now `*int` so omitted and explicit-0 are distinguishable; an explicit value outside `1..168` (7 days) is now rejected with `invalid_params` instead of silently coerced.
- **`create_preview`'s `ttl_seconds` silently clamped out-of-range values with no signal** (#838, split from #834): `ttl_seconds` is now `*int`; an explicit value outside the configured bounds is clamped as before but the response now echoes `data.effective_ttl_seconds` and appends a warning explaining the clamp, rather than returning a TTL different from what was requested with no explanation.
- **`rate_limit_remaining` could report a false `0` on early-validation errors instead of the caller's real remaining quota** (#836, split from #834): tool responses that error out before the shared create/update/upload rate limiter was ever consulted still populated `rate_limit_remaining` from the response envelope's typed zero value, indistinguishable from a genuinely exhausted quota. The limiter lookup now happens before early validation on every affected write path, so an early-error response always reports the caller's actual remaining budget.
- **`max_body_chars` accepted `0`/negative values and silently treated them as unlimited** (#837, split from #834): now `*int` on `get_page_for_edit`/`build_agent_context`; an explicit non-positive value is rejected with `invalid_params`, while omitting the field still means "no truncation" — only an explicit non-positive value is now an error, not the previous ambiguous silent-unlimited behavior.
- **`plan_page`'s `relevant_tags`/`relevant_categories` could mismatch `list_tags`/`list_categories`** (#826): `plan_page` drew its vocabulary from a different source than the two standalone tools it's meant to bundle, so an existing category could be reported as unmatched (`empty_categories_reason`) while its cross-cased tag-side twin correctly surfaced. Now uses the same source-index-plus-alias-normalization vocabulary as `list_tags`/`list_categories`, giving byte-for-byte parity between `plan_page`'s facets and their standalone equivalents.

### Internal
- **New required CI check, `commit-identity`**: verifies every commit newly reachable on a push/PR to `main` is authored by the project's own account identity, and that no email outside an allowlist (GitHub noreply / `@anthropic.com`) appears anywhere in a commit message — closing a gap where a validly-signed commit could still carry the wrong author name, or leak a personal email via a `Co-authored-by` trailer, neither of which GPG signing alone catches. `required_signatures` is now also enforced on `main`.

## [v1.7.5] - 2026-08-03

The two items deliberately scoped out of v1.7.4 as "larger, independent additions" — pulled forward.

### Added
- **`inspect_rendered` gains a dedicated `featured_image` check** (#818): the existing `missing_images` check treats every `<img>` uniformly and can't tell a broken hero image apart from a broken body image. `fail` means a configured `featuredImage` doesn't resolve to a file in the built public output; `warn` covers fixable-not-broken issues (no alt text on the rendered `<img>` referencing it — checked against both `src` and `data-src`, since lazy-loading theme markup can put the real URL in either — or an `og:image` meta tag that doesn't match); `pass` reports decoded pixel dimensions when available. Deliberately local filesystem/DOM inspection only, never an outbound HTTP request to the page's own public URL — this server's own production deployment terminates TLS upstream of the process it runs in, so a self-fetch of the page's `https://` URL isn't guaranteed to even resolve from where the server runs. Resolves the featuredImage's on-disk path through `security.PathGuard`, not a bare `filepath.Join` — the raw frontmatter value is agent-writable and unvalidated for path shape, and an unguarded join would let a value like `/../../../etc/hostname` turn this read-only check into an arbitrary-file existence/size/dimensions oracle (caught in review before merge).
- **`get_site_health` gains `untracked_source_pages`** (#819): counts source pages with no git-tracked file, via a single `git ls-files --others` call scoped to the content root rather than one check per page — an operational-hygiene signal (no git-based rollback path for that content) surfaced proactively instead of only discoverable per-page via `diff_page`'s own `git_untracked` status. Omitted entirely (not a zero) when git status can't be determined at all (no repo, git unavailable), so a genuine "0 untracked" is never confused with "couldn't check." Never affects `score`/`status`.
- **`gosec` advisory scan wired into CI and the local Makefile**: runs after `govulncheck` as a non-blocking signal (`continue-on-error: true`) over `./cmd/...` and `./internal/...` with test files excluded, so the repo gains an extra static security sentinel without immediately turning known low-value noise in test fixtures into a hard gate.

### Internal
- **Hero-image local renderer no longer uses `crypto/md5` for non-cryptographic selection/palette seeding**: deterministic background selection and palette variation now derive from a stable SHA-256-based 64-bit hash instead. This does not change any security boundary or public contract (background/palette selection for a given title will simply shift to a different-but-still-deterministic pick than before), but removes an avoidable weak-primitive warning from security scanning and keeps future audits focused on higher-signal findings.
- **Remaining targeted `gosec` findings quieted with precise justifications** (`G306`, `G204`, `G101`): documented fixed-command subprocess calls (git/hugo invocations with server-controlled, non-caller arguments) and the public hero-image write (`0644`, intentionally world-readable for static hosting) with `#nosec` comments rather than broad suppression. The Google Index quota-state file's permission was tightened from `0644` to `0600` (operator-local state, no reason to be group/world readable).

## [v1.7.4] - 2026-08-03

Live-audit follow-up. A ChatGPT-based live audit initially reported `update_page`'s `featured_image` write param as missing — that was wrong (stale client-side tool schema; the auditor retracted it after re-verifying live) and no code change was needed for it. Two things from the same audit *were* real and are fixed here:

### Fixed
- **`get_page_frontmatter`/`get_page_for_edit` didn't expose `featured_image`/`featured_image_preview`/`description`/`draft`** (#817): these were present in source frontmatter and already writable via `update_page`, but invisible to a caller reading through the structured frontmatter tools — the only way to discover e.g. a page's `featuredImage` was an indirect tool like `diff_page` or `list_page_assets`. Now populated from source frontmatter (omitted, not a zero value, when unset or when only public output is resolvable) with field names matching `update_page`'s write parameters for direct read→write round-tripping.
- **Production deploys silently lost release identity (`build_channel: "main"` instead of the actual release tag) whenever the `Deploy to Production` workflow's optional `release_version` input was omitted** (#816): `git describe --tags --exact-match` always fails for this repo's deploy-then-tag ordering (Release requires the deployment to already be live before it cuts the tag), and #555's fix required a human to remember to pass `release_version` by hand on every dispatch — forgotten on the last two production deploys (v1.7.2, v1.7.3) in a row. `deploy.yml` now falls back to reading `npm/package.json`'s `version` (already bumped as part of every release commit) when both the explicit input and `git describe` come up empty, so the deployed identity is correct without relying on anyone remembering an optional flag.

## [v1.7.3] - 2026-08-02

### Added
- **`generate_hero_image` and `update_page` now advise instead of silently drifting on the hero-image/frontmatter link** (#814): considered fully automating `generate_hero_image` → `featuredImage` (the two-call gap that shipped a broken homepage card earlier), but ruled it out — the tool has no language awareness (a bundle's translations each need their own `featured_image`) and no access to `update_page`'s optimistic-locking (`expected_revision`), so a silent write would mean either duplicating that locking or bypassing it. Instead: `generate_hero_image` now returns `data.public_path` (the ready-to-paste `featuredImage` value) and a warning pointing at `update_page` when the generated image isn't attached to any page yet; `update_page` now warns when `title` changes without `featured_image` also being set in the same call and the page already has a `featuredImage` — since the hero image's title text is baked directly into the JPEG, a later title-only edit has no automatic way to reach it. Both are advisory (`warnings`/`data.warning`), never block the write.

## [v1.7.2] - 2026-08-02

### Fixed
- **`generate_hero_image` local renderer now uses a proper display font instead of a tiny debug bitmap font** (#812): every baked-in text element (title, subtitle, tags, brand mark) was drawn with `golang.org/x/image/font/basicfont.Face7x13` — a fixed 7×13px monospace font meant for debug output, not display use — regardless of the requested visual size. This made every locally-rendered hero image look illegible and out of place next to hand-made card art, and separately caused `→` (U+2192) and other glyphs outside that font's tiny coverage to render as a missing-glyph box. Switched to `golang.org/x/image/font/gofont/gobold` rendered at real sizes (46px title / 20px subtitle / 15px tags / 18px brand) via `opentype.NewFace` — already vendored via `golang.org/x/image`, no new dependency. Verified via `sfnt.GlyphIndex` that gobold covers `→` and the accented French characters used throughout this site's content. Multi-line titles now anchor their last line to a fixed baseline and grow upward, so a long wrapped title no longer overlaps the subtitle/tag row below it.

## [v1.7.1] - 2026-08-02

### Added
- **`update_page` gains a `featured_image` parameter** (#809): sets the theme's `featuredImage` frontmatter key verbatim — until now there was no MCP tool path to attach a `generate_hero_image`-generated image to a page's frontmatter at all, so generated hero images never got the theme's cover-photo/title-overlay list-card treatment, only the plain in-page render. Confirmed live before this fix: neither `plan_content_change`'s `set_field` nor a direct `update_page` parameter accepted it.

### Fixed
- **In-memory `SourceIndex.FrontmatterRaw` staleness after `update_page`/`apply_content_plan`/`rollback_change`** (#810): each of these tools patched only `title` into the in-memory index by hand after a successful write, so every other frontmatter field they can set — `description`, `draft`, and now `featured_image` — was written correctly to disk but the in-memory copy kept its old value until the next full server reindex. Surfaced in production as `check_ai_readiness`/`get_page_for_edit`'s readiness block reporting `description_present: false` immediately after a successful `update_page`/`apply_content_plan` call that set a description, even though the description was genuinely present in the file. All three call sites now re-parse `FrontmatterRaw` wholesale from the content actually written (or restored, for rollback) instead of patching individual keys in by hand — closes the gap for every current and future settable field at once.

## [v1.7.0] - 2026-08-02

Milestone release: closes #782, the public distribution plan (MCPB/Claude Connectors Directory + npm). This is the first release published under all three install paths at once — self-hosted HTTP+OAuth (unchanged), `npx`/npm (`@jmrgrav/mcp-hugo-server-go`, live on the npm registry), and a `.mcpb` Claude Desktop extension (manually install-tested on real Windows/Claude Desktop, then submitted to the Claude Connectors Directory for review).

### Added
- **`.mcpb` bundle packing automated into the release pipeline** (#806): every release now gets a correctly-scoped `.mcpb` attached automatically (`.mcpbignore` keeps it to the 5 files the manifest actually references, not the whole repo) — no more manual local builds.

### Fixed
- **README's Installation section corrected** (#807): no longer claims the `.mcpb` bundle and Directory listing are "planned but not yet published" — both are live. npx badge now points at the real npmjs.com listing.

### Internal
- Companion wiki refresh (Installation Guide, Home, Release Checklist) to match — the release process now has three distribution channels, not one, and the checklist previously only covered the self-hosted HTTP+OAuth deploy.

## [v1.6.10] - 2026-08-02

### Added
- **npm publish provenance** (#782): `publish-npm.yml` now publishes `@jmrgrav/mcp-hugo-server-go` with `--provenance` (`id-token: write` + `publishConfig.provenance: true`) — a cryptographically verifiable, Sigstore/Rekor-backed link between the published package and the exact GitHub Actions run/commit that built it. Supply-chain scanners (npm's own registry UI, Socket.dev, etc.) treat provenance-backed publishes as materially more trustworthy than a bare token-authenticated one. `v1.6.9` was published before this change and has no provenance attestation; `v1.6.10` is the first version that does.
- **`npm/` package now bundles its own `LICENSE`** and declares an explicit `author` field — both were previously only present at the repository root, not in the published tarball/metadata.

## [v1.6.9] - 2026-08-02

### Added
- **GoReleaser cross-compile pipeline** (#782, #797): `release.yml` previously tagged the repo and wrote release notes but attached zero binaries to any GitHub release. Adds `.goreleaser.yaml` and wires it in, `release.mode: keep-existing`, so each release from this point on gets real `darwin/amd64`, `darwin/arm64`, `windows/amd64`, `linux/amd64`, `linux/arm64` binaries plus a `checksums.txt`. This is the first release built with the pipeline in place.
- **`@jmrgrav/mcp-hugo-server-go` npm wrapper** (#782 Phase 4, #798): esbuild/ripgrep-pattern package — `npm install`/`npx` downloads the matching-version binary from this release's GitHub Release assets and verifies its SHA-256 against `checksums.txt`. Verified end-to-end against a real cross-compiled binary on a separate VM: all 51 tools reachable over stdio through the packaged shim. Not yet published to the npm registry.
- **MCPB manifest icon** (#782, #796): 512x512 RGBA PNG wired into `manifest.json`'s `icon` field.
- **README badges and npx install docs** (#799): `MCP-stdio`, `npx`, and `Claude Desktop` badges; `## Installation` now leads with `npx @jmrgrav/mcp-hugo-server-go`.

## [v1.6.8] - 2026-08-02

### Added
- **stdio transport for local, single-user MCPB/desktop-extension use** (#782, #789): a new `NewStdio` construction path grants write access unconditionally over stdio (no OAuth) — safe because it never shares the HTTP transport's scope-routing callback, proven by a bearerless-HTTP regression test. The existing `mcp.arleo.eu` HTTP+OAuth deployment is completely unchanged.
- **`MCP_HUGO_*` environment-variable config overlay** (#790): `MCP_HUGO_SITE_ROOT`/`MCP_HUGO_HUGO_ROOT`/`MCP_HUGO_CONTENT_ROOT`/`MCP_HUGO_SITE_URL`/`MCP_HUGO_SITE_NAME` fill in config fields left empty by `config.yaml` (or its absence) — needed because MCPB-style hosts can only inject environment variables, never write a config file. File values always win.
- **TOML (`+++`) frontmatter support** (#786, #788): pages using TOML frontmatter were previously silently misread as missing title/date. YAML and TOML are both read; writes still normalize to YAML only, unchanged.
- **`manifest.json` draft and README installation guide** (#792, #794) for MCPB/Directory submission: `user_config`-driven env injection, `privacy_policies`, and a new README `## Installation` section covering both transport modes (stdio for local single-user use, HTTP+OAuth for shared/remote self-hosting) and which to pick.
- **`TestAllToolsHaveAnnotations`** (#793): an enforced regression test, not just a one-time audit, proving every registered tool exposes correct `readOnlyHint`/`destructiveHint` MCP annotations — a Directory-submission requirement.

### Fixed
- **`suggest_links` rejected a resolved `slug` with no `tags`/`categories`** (#784): the validation ran before the slug's own tags/categories were merged in, so a valid taxonomy-free call incorrectly failed `invalid_params`.
- **`site_root` pointed at a Hugo project root (instead of `public/`) silently ingested raw theme templates as content pages** (#785, #787): added a heuristic startup warning when `site_root` looks like a project root rather than a build output directory.
- **Generated hero images hardcoded `arleo.eu` as the watermark** (#783): now uses the caller-supplied site domain, so third-party deployments no longer get this deployment's own brand on their images.

### Internal
- **Windows was never actually able to compile** (#791): POSIX-only `syscall` calls in `internal/tools/admin` (process-group handling, ownership checks) had no build-tag gating. Fixed via a `//go:build windows`/`!windows` platform split, verified on real `windows-latest`/`macos-latest` GitHub Actions runners via a new additive, non-blocking `stdio-cross-platform.yml` workflow.

## [v1.6.7] - 2026-07-27

### Added
- **`get_runtime_status` exposes `data.git.changed_files_count`** (#775): a safe, reliable count of `git status --porcelain` lines when the baseline is dirty, never exposing paths or content. A `dirty_reason` mcp-vs-external provenance classifier was evaluated and deliberately not added — the closest existing signal, `index_staleness.likely_source` (#583/#617), documents itself as a coarse best-effort hint rather than reliable per-caller attribution, and reusing that same guarantee here risked shipping a field that looks more precise than it actually is.

### Internal
- Added MCP contract-drift sentinel tests that fail when a tool's runtime, exported `tools/list` schema, and embedded description drift apart — covering `run_post_build_hooks.dry_run`, `get_page`'s null payload on not-found, `delete_page`'s `bundle_will_be_fully_removed`, `get_mutation_status`'s `apply_content_plan`/`rollback_change` coverage, and `get_site_health`'s translation-pair status wording (#773, #776, #777).
- Added deterministic end-to-end integration coverage for `run_post_build_hooks`: multiple concurrent hooks with request introspection (method/headers/body), non-2xx response handling (captured in `status`, not treated as a transport error), partial-failure behavior (one hook failing doesn't block others), and confirmation that hook failures are advisory, never blocking the tool's own response (#774).

## [v1.6.6] - 2026-07-26

### Fixed
- **`get_site_health` no longer degrades `status` to `healthy_with_advisories` for a pure `translation_pair`/`info` finding** (#761): a deliberate bilingual pair (e.g. `security`/`sécurité`) is expected localization, not an actionable defect, so it stays visible via `advisories_count`/`taxonomy_inconsistency_details` but no longer drags an otherwise-healthy site's `status` down on its own. A `warning`-severity finding (`casing_variant`/`alias_mismatch`/`possible_duplicate`) still promotes `status` to `healthy_with_advisories` as before, and `score`'s existing `#719` cap-at-99 behavior for warning findings is unchanged.
- **`get_page` returned an empty placeholder page object instead of `null` on a not-found error** (#759): `data.page` is now a nullable pointer, so a `content_not_found` error response serializes `data.page: null` rather than an empty struct a caller could mistake for a real (if blank) page.

### Changed
- **`delete_page`'s `bundle_fully_removed` field converted to a nullable pointer** (#762): a dry-run response now omits `bundle_fully_removed` entirely (it was never computed) in favor of the dry-run-only `bundle_will_be_fully_removed` prediction field, instead of misleadingly reporting a real-looking `false`. A real (non-dry-run) delete's `bundle_fully_removed` is unaffected and still reports its true value, including an explicit `false` on a partial multilingual delete.

### Added
- **`run_post_build_hooks` supports `dry_run: true`** (#760): previews which hook URLs would be called (`data.results`, `data.configured_count`) without making any HTTP calls, for auditability before firing real webhooks.
- **`get_runtime_status` surfaces overdue test/scratch content** (#757): `data.site.overdue_test_content[*]` (`slug`, `owner`, `expires_at`, `overdue_seconds`, `reason`) lists reserved test slugs or explicit `test_content` pages past their threshold, reusing the same detection `create_page`'s stale-test-content guard already runs, now visible without triggering the guard's own error path.
- **`get_mutation_status` covers `apply_content_plan` and `rollback_change`** (#758): these two mutation tools already recorded idempotency results under their own tool-name keys; they were simply missing from the lookup allowlist.

### Internal
- Removed two stray tracked local tooling artifacts (`.brooks-lint-history.json`, `.superpowers/sdd/gemini-audit-report.md`) that had no code references (#753).
- Replaced fixed-delay `time.Sleep` calls with goroutine-start/condition-polling handshakes in `internal/server`, `internal/tools/admin`, `internal/tools/write`, and `internal/tools/read` concurrency/timing tests, removing a source of CI flakiness under load without weakening any test's actual assertion (#751, #752).
- Widened unit coverage for `internal/tools/admin`'s stale-test-content sentinel (bilingual-bundle dedup, malformed-expiry fallback), `internal/observability` (legacy-scope/byte-estimation/degraded-result helpers), and `internal/server` (bearer-middleware pass-through, discovery/security.txt/auth.md handlers) — no runtime behavior changes, coverage-only hardening on security- and operator-facing helper paths.

## [v1.6.5] - 2026-07-26

### Fixed
- **`get_site_health`'s `taxonomy_inconsistency_details` could stay stale after a source edit was fixed and rebuilt** (#730): post-build wiring only reloaded the public `*site.Index`, not the in-memory `*hugosite.SourceIndex` `get_site_health` actually reads its taxonomy findings from — an out-of-band source edit (or an `update_page` fix) followed by `build_site`/`publish_changes` refreshed public output while source-derived health kept reporting the pre-fix casing drift. Fixed with a new `(*hugosite.SourceIndex).Reload(contentRoot)` that replaces the index's pages and derived maps in place (existing read-tool handlers already hold the original pointer, so swapping the pointer in `server.New` wouldn't have reached them) — called from the required `index_reload` post-build callback, inside the same `ContentMu` write-lock `runBuild` already holds for the whole build+callback dispatch, matching the locking convention `Upsert`/`Delete` already require. A new integration test reproduces the exact stale path: seed a casing drift, confirm health reports it, edit the file out-of-band, invoke the real post-build callback, assert the drift is gone.
- **`delete_page`'s idempotent replay could return `not_found` for the exact same successful delete instead of the stored result** (#724): the replay lookup ran after the on-disk existence check, so a successful first delete (which removes the slug) made an exact retry with the same `idempotency_key` fail instead of replaying the original success — breaking the documented safe-retry-after-timeout guarantee. Fixed by moving the idempotency replay lookup ahead of `SafeJoin`'s existence check, `ResolvePageSource`, and the rate limiter's `Allow()`, under the content lock, so a same-key retry short-circuits to the cached result before either the destructive quota or path resolution ever runs.
- **`create_page`/`update_page`/`delete_page` hostile-slug rejection now has explicit end-to-end regression coverage on `update_page` and `delete_page`, not just `create_page`** (#735): a review pass following #691 found the existing hostile-slug corpus (raw/encoded/double-encoded traversal, backslash traversal, absolute path, Unicode confusable slash, control characters) was only exercised against `create_page`. New tests confirm `update_page` and `delete_page` both reject every case in the corpus and leave an unrelated victim page byte-identical on disk afterward. No runtime behavior changed — `normalizeInputSlug`'s existing fail-closed posture already covered these paths; this closes the regression-net gap, not a live vulnerability.
- **`generate_hero_image`'s slug input contract tightened to reject the same hostile-input corpus as `create_page`** (#734), and **`create_page`/`update_page`/`delete_page`/`upload_page_asset` write-contract edge cases cleaned up** (#733), alongside **`test_content: true` pages now consistently required to stay `draft: true` until the marker is removed, across every write path that touches front matter, not just `create_page`/`update_page`'s own direct callers** (#731).

### Changed
- **`plan_page` gains multilingual planning controls: `language`, `suggestion_limit`, and richer compact-mode trimming** (#722, #723): optional `language` filters `suggested_links` to one language; `one_per_source_key: true` collapses FR/EN translation siblings into one conceptual recommendation while preserving the default ungrouped behavior; both are applied against the full scored candidate pool, not a pre-truncated one — a lower-ranked matching candidate is no longer lost to `suggestion_limit` before the language/collapse filter runs. `response_mode: "compact"` now also trims `content_types` down to `name`/`source` only, and `empty_categories_reason` makes an unmatched category input explicit rather than a silent empty array.
- **`search_content`/`explain_structure`/`get_page_markdown`/`get_page_frontmatter`/`build_agent_context`/`export_agent_context`/`get_page_for_edit` gain an opt-in `include_terms` parameter (default `true`) to omit the heavier `tag_terms`/`category_terms` payload** (#618, #720): these nested term objects duplicate the plainer `tags`/`categories` arrays already present on the same response; `include_terms: false` (or `response_mode: "compact"`, which implies the same omission) drops them where a caller only needs the plain string arrays.
- **`get_changelog`/`diff_page`/`explain_structure` compact mode is now genuinely compact, not a partial trim** (#720): `get_changelog(response_mode: "compact")` defaults to 1 entry (not 5) when `limit` is omitted and omits each entry's raw Markdown `body`; `diff_page(response_mode: "compact")` omits the full unified diff (and any full-source fallback) in favor of a short `data.diff_summary`; `explain_structure(response_mode: "compact")` now omits `recent_pages` examples and the long `notes` list entirely, keeping only the structural summary (section/language/taxonomy counts).
- **`get_site_health`'s top-level `score` no longer reads a misleading `100` alongside an actionable `warning`-severity taxonomy finding** (#719): a `casing_variant`/`alias_mismatch`/`possible_duplicate` finding is still zero-weighted in `score_breakdown.taxonomy.weight` (unchanged), but now caps an otherwise-perfect top-level score at `99` so the response doesn't advertise perfection while `taxonomy_inconsistency_details` lists a real, actionable finding. Info-only findings (e.g. expected bilingual `translation_pair`s) still never move the score.

### Internal
- Split the oversized `internal/tools/write.Register` composition function into a dedicated `writeRegisterRuntime` plus per-tool `registerCreatePageTool`/`registerUpdatePageTool`/`registerDeletePageTool` helpers, and similarly split `internal/tools/anonymous.Register`/`internal/tools/read.Register`/`RegisterWithSourceIndex`'s monolithic registrar bodies into smaller focused helpers (#672) — behavior-preserving in both cases: tool names, schemas, scopes, and ordering are unchanged, verified by new `tools/list` catalog sentinel tests.
- Extended `PathGuard` TOCTOU/symlink-swap regression coverage (the previously-untested core guarantee that `RevalidateForWrite` rejects a directory swapped for a symlink between `SafeJoin` and the actual write) and a stat-error branch in `rejectSymlinkComponents`.
- Added a write idempotency replay matrix covering `create_page` (dry-run doesn't poison the real replay key), `update_page` (same-key replay returns the original envelope), `delete_page` (including a `lang`-specified bilingual-bundle case), and `upload_page_asset` (same key + different payload correctly fails `idempotency_conflict`) (#727).

## [v1.6.4] - 2026-07-26

### Security
- **`create_page`/`update_page` no longer accept a lone leading-slash slug like `/tmp/escape` as a valid, writable slug** (#691): a hostile-path regression corpus over `create_page`, `upload_page_asset`, and `generate_hero_image` found that `normalizeInputSlug` used `strings.Trim(s, "/")`, which silently rewrote an absolute-path-looking input into a relative one (`/tmp/escape` → `tmp/escape`) — a form that then passed the existing `slugPattern` regex validation and was written under `content_root` as `content_root/tmp/escape/index.md`, not the traversal-outside-root it superficially resembled, but still a case of a hostile-looking input being silently accepted rather than rejected. `normalizeInputSlug` now only strips slashes symmetrically (`/posts/foo/` → `posts/foo`, the canonical-public-to-source-key conversion #265 requires); a lone leading slash is left intact, so the same pre-existing `slugPattern` regex (which requires the first character to be `[a-z0-9]`) now rejects it outright as `invalid_params`, exactly like every other malformed slug. `delete_page`/`upload_page_asset`/`rollback_change`/`apply_content_plan`, which resolve slugs via `pg.SafeJoin` rather than the stricter regex, were never actually vulnerable to this specific input — `filepath.Join` already keeps a leading-slash argument contained within the given root — so this is a `create_page`/`update_page`-specific hardening, not a new-vs-old behavior change for those other tools. A new hostile-input test corpus (raw/encoded/double-encoded traversal, backslash traversal, absolute path, Unicode confusable slash, control characters) now runs against `create_page`'s slug, `upload_page_asset`'s filename, and `generate_hero_image`'s slug.
- **OAuth/agent token and code generation no longer crashes the whole process when `crypto/rand` fails to read entropy** (#671): `internal/oauth/oauth.go`'s `randomString` helper — the sole random-token generator behind dynamic client registration, authorization codes, access/refresh tokens, and anonymous agent registration/claim tokens — called `panic("crypto/rand failed")` on a read error, which crashes the entire server process from inside a live, network-reachable request handler, taking down every other in-flight request and connection along with it, not just the one that hit the failure. `randomString` now returns `(string, error)`; every call site (`registerClient`, `issueAuthCode`, `exchangeRefreshToken`, `issueBearerPair`, `registerAgentAnonymous`, `exchangeAgentAssertion`, `initiateClaim`) propagates the error up to its HTTP handler (`HandleRegister`, `HandleAuthorize`, `HandleToken`, `HandleAgentIdentity`), which now returns a `server_error`/HTTP 500 instead of letting the panic propagate. No insecure fallback (weaker/pseudo-random source) and no partial token issuance — the request fails closed, loudly, without affecting any other request. The random source is now an injectable package-level seam (`cryptoRandReader`, defaulting to `crypto/rand.Reader`) so tests can force the failure path deterministically; four new regression tests cover `/register`, `/authorize`, `/token`, and `/agent/identity` all failing closed with a 500 rather than a process crash when entropy generation fails.
- **`delete_page` can no longer delete an entire bilingual/multilingual bundle when only one language should be removed** (#682): a live v1.6.3 audit found `delete_page` had no `lang` parameter — on a bundle with `index.fr.md` + `index.en.md`, it resolved to one language file (reported in `resolved_source_path`) via a one-off helper (`inspectDeleteSource`) that just picked the alphabetically-first file and never errored on ambiguity, unlike `update_page`'s existing `ambiguous_language` contract — then deleted the **entire bundle directory**, both languages and any shared assets. `delete_page` now accepts `lang`; omitting it on a bundle with more than one language file fails with `ambiguous_language` instead of guessing, matching `update_page`. Only the resolved language's source file is removed; the bundle directory (and shared assets, hero image, public output, derived DB/index entries) is only removed once no language file remains — `data.bundle_fully_removed` reports which happened. In-memory index removal is now scoped to the deleted language via a new `SourceIndex.DeleteLang(slug, lang)` (the existing whole-slug `Delete` was itself part of the bug: it silently dropped every language's index entry, so a surviving translation's file could remain on disk while vanishing from `get_page`/`list_pages`). A partial delete leaves the surviving language's public output untouched rather than risk deleting a live translation's rendered page — surfaced via `data.warning` recommending a rebuild to reconcile. Idempotency's `request_hash` now includes `lang`, so replaying a delete for two different languages under the same `idempotency_key` no longer collides. Until this fix, this was a genuine data-loss risk: an agent intending to delete one translation of a bilingual page could delete all of them. **`lang` is validated with the same `validateLangParam` check `create_page`/`update_page` already use before it ever reaches path resolution** — a first version of this fix passed `lang` straight into `contentmodel.ResolvePageSource` (which builds candidate paths via `filepath.Join`), so an unvalidated value like `../../victim` could resolve to, and delete, a file entirely outside the requested slug's own bundle; caught by Strix Security Review before merge. An explicit `lang` that doesn't match any file on disk is now rejected outright rather than silently downgraded to the source-less/public-only case — that downgrade was itself a second bug the same review caught: it skipped the `expected_revision` requirement and drove the whole-bundle-deletion branch, so an invalid `lang` could still wipe every translation instead of failing cleanly. A follow-up Strix pass on the corrected commit found a third, narrower regression: the new single-file `os.Remove(resolvedSource.SourcePath)` branch never called `pg.RevalidateForWrite` before unlinking, unlike every other write path in this server (`create_page`, `update_page`, `upload_page_asset`, `rollback_change`) — if the slug directory were swapped for a symlink pointing outside `content_root` between the initial `SafeJoin`/resolve and this delete, `os.Remove` would follow it and delete a file outside the content root, whereas the previous whole-directory `os.RemoveAll` only ever removed the symlink itself. Fixed by revalidating the resolved source path immediately before the unlink, matching the existing pattern everywhere else.

### Added
- **`suggest_links` now falls back to lexical term matching when tag/category taxonomy affinity yields zero candidates** (#680): a live audit found `suggest_links` returning `no_candidates_with_sufficient_taxonomy_affinity` even when a draft's `body` clearly named topics an existing published page already covered, because taxonomy overlap (shared tags/categories) was the *only* signal — no relation existed if the caller's tags happened not to overlap with any indexed page's tags/categories, regardless of what the body actually said. The fallback only activates when taxonomy scoring produces nothing; taxonomy candidates always take precedence and suppress it entirely whenever any exist, so this is additive-only, not a ranking change for the common case. It reuses the same `scoreContentPage` lexical matcher `search_pages`/`search_content` already score against, rather than introducing a second search subsystem or a `db_path`/FTS dependency — so it works identically on reader-only deployments with no derived index.
- **Every mutation tool (`create_page`, `update_page`, `delete_page`, `upload_page_asset`, `delete_page_asset`, `apply_content_plan`, `rollback_change`) now returns a structured `data.rate_limit` object, alongside the pre-existing scalar `rate_limit_remaining`** (#690): `{remaining, limit, window_seconds, scope, reset_at, retry_after_seconds}` gives an agent everything needed to reason about pacing without cross-referencing `get_rate_limits` separately — `scope` (`"create_update_upload"` or `"destructive"`) identifies which of the two independent per-caller budgets applies, `reset_at` is the earliest timestamp a call would succeed (derived from the token-bucket state, not a fixed-window schedule the runtime doesn't actually implement). `get_rate_limits` (#614) now returns the same enriched shape for its own two budget fields. The legacy scalar `rate_limit_remaining` (root and `data`) is unchanged and stays in place for v1.x compatibility — this is additive reporting only, quota behavior itself does not change.
- **`list_page_assets`/`delete_page_asset`/`delete_page` now separately expose a page's `generate_hero_image`-generated hero image, previously invisible to all three tools** (#683): a `generate_hero_image`-generated hero at `{HugoRoot}/static/images/{slug}-featured.jpg` lives outside the page bundle directory these tools otherwise operate on, so an agent had no way to discover it, delete it independently of the whole page, or preview its cleanup impact before a `delete_page` dry-run. `list_page_assets` now reports it separately in `data.generated_assets[*]` (`name`, `path`, `kind: "global_static"`, `size_bytes`, `modified_at`, `sha256`), distinct from `data.assets`' bundle-local files, so the existing "empty means nothing to see" `data.hint` now only fires when both arrays are empty. `delete_page_asset` gains an optional `scope: "generated"` to explicitly retarget the call from the default bundle-local file to the generated hero image instead — resolved purely from `slug` (never from the caller-supplied `filename`, which must still match the canonical `{slug}-featured.jpg` name or the call fails `not_found`), echoed back via `data.scope`/`data.path`/`data.kind`; omitting `scope` (or passing `"bundle"`) keeps existing behavior unchanged. `delete_page`'s `dry_run` preview now includes `data.generated_assets` (same shape) when the whole bundle would be removed, matching the existing cleanup boundary (a partial multilingual delete never touches the hero image). No storage migration and no automatic remapping into bundle-local `data.assets` — the generated image stays at its existing location; this is presentation/lifecycle-visibility only.

### Changed
- **`get_site_health` now reports `status: "healthy_with_advisories"` instead of `"healthy"` when `advisories_count > 0` on an otherwise-healthy site** (#681): a live audit found the top-level `score`/`status` pair could read `100`/`"healthy"` while `data.taxonomy_inconsistency_details`/`advisories_count` still listed a real finding (e.g. a `casing_variant` or `alias_mismatch`) — an agent reading only `status` at a glance had no signal to look further, since taxonomy findings were designed to never move `score` (#419). `score` and `score_breakdown` are unchanged; this is presentation-only, matching #419's own scope note. Both `info`- and `warning`-severity findings count toward `advisories_count` identically, so severity doesn't change whether `status` flips.
- **`get_page(response_mode: "compact")` now omits `page.html` and `page.tag_terms`/`page.category_terms`; `delete_page(dry_run: true, response_mode: "compact")` omits `data.content`/`data.backlinks`** (#687): two live audits found `compact` mode still shipped the full rendered HTML body on `get_page` and the full source content on a `delete_page` preview, undercutting the point of a low-token page-selection/preview path. `get_page` compact still sets `page.rendered_html_available`/`page.html_origin` so a caller can tell rendered HTML exists without paying for it; `delete_page` compact dry-run returns `data.backlinks_count` in place of the full backlink list, so impact size is still visible. `data.backlinks_count` is present only on a `dry_run` response (compact or not) — a real (non-`dry_run`) delete never runs a backlink scan, so it never appears there, avoiding a misleading `backlinks_count: 0` reading as "verified zero backlinks" on a call that never checked.

### Fixed
- **`upload_page_asset`'s MIME-mismatch error (e.g. a `.jpg` filename whose bytes don't sniff as JPEG) now correctly identifies `errors[0].field: "filename"` instead of leaving `field` unset**: the error message ("uploaded content does not match declared extension...") didn't match any prefix `toolcontract.inferField` already recognized, so the structured error carried a valid `code: "invalid_params"` but no `field`, leaving an agent to re-parse the free-text message to figure out which parameter to fix. Fixed by adding that message prefix to `inferField`. Widened adversarial upload test coverage alongside it (fake-JPEG rejection, uppercase-extension acceptance, oversize-decoded-payload rejection with no residue left behind) — the latter two were already covered by #688's regression suite, this adds the specific field-inference assertion.
- **A structured error's `field` inference no longer mangles a quoted enum value with a leading stray double-quote** (e.g. `must be one of: "post", "page"` → an error for the second option previously surfaced as `"page` instead of `page`): `toolcontract.splitValues` trimmed quote characters (`strings.Trim(part, `"'`)`) before trimming whitespace, so a value like ` "page"` (leading space from the `, ` separator) never had its leading quote reachable by `Trim` — the space blocked it from the string's edge, and the subsequent whitespace trim ran too late to expose it to a second quote-trim pass. Fixed by trimming whitespace first, then quotes. Found via new characterization tests added while raising `internal/...` test coverage toward the 85% floor requested by #669, not a live-audit-reported symptom, but a genuine contract bug nonetheless.
- **Reading a source-only multilingual page bundle (no built public page yet) with no explicit `lang` now deterministically prefers the server's configured `DefaultLanguage`, instead of whichever language file the source index's internal slug lookup happened to return first** (#684): `site.PageResolver.resolveSource` fell through to `SourceIndex.GetBySlug`, which resolves via a `bySlug` map built from the slug-sorted page list with last-write-wins semantics — for a bundle like `index.fr.md` + `index.en.md`, which language "won" depended on internal iteration/insertion order, not on any rule a caller could reason about. The resolver now checks the configured `DefaultLanguage` for a source-only candidate before falling back to that ordering-dependent lookup, so the same call always resolves to the same language. `get_page`'s response now also echoes the resolved language in `page.lang` (previously only `page.resolved_lang` carried it in this source-fallback path).
- **`get_rate_limits` (#614) no longer rejected as `unknown_tool` for a caller holding a valid `write`-scoped token** (#675): a live v1.6.3 audit found the new tool failed with a permission error even though every other `write`-scoped tool worked fine in the same session with the same token. Root cause: `internal/oauth/acl.go`'s `ScopePolicy.checkOne` resolves a tool's required scope via the registry built at startup from each package's `Defs()` — `get_rate_limits` was wired as a real tool (`registerGetRateLimits`, #614/#656) but never added to `write.Defs()`, so `RequiredScopeFor` returned `known=false` and every caller was denied outright, regardless of scope. The same class of gap was previously caught as a drive-by for `plan_page`/`list_page_revisions` during #612; this instance slipped through because `get_rate_limits` shipped in a separate PR that didn't touch `Defs()`. Fixed by adding the missing `Defs()` entry, plus two new regression tests (`TestACLGetRateLimitsResolvesKnownScope`, `TestACLEveryWriteToolResolvesKnownScope`) — the latter asserts every tool `write.Defs()` declares actually resolves to a known scope, so this exact bug class can't recur silently.
- **`list_page_revisions` now returns `revisions: []`, not `null`, for a page inside a real git repository that was never itself committed** (#676): every other listing tool on this server (`list_pages`, `get_sitemap`, ...) uses the empty-array convention when there's nothing to report; `list_page_revisions`'s `status: "ok"` path (a git repo exists, but `git log --follow` returns nothing for this specific file) left the underlying Go slice `nil`, encoding as JSON `null` — a client that assumes an array and calls `.length`/iterates without a null-check breaks specifically on this tool. Fixed by initializing the slice as `[]pageRevisionDTO{}` before the loop.

### Documentation
- **`normalize_taxonomy_casing`'s `lang`-scoping caveat is now the first sentence of the parameter's description, not an aside after the main explanation** (#677): #604 already documented that matching is scoped to the exact `lang` bucket of the page being written, but two independent live audits (pre- and post-#604) still missed it and read the resulting no-op as a broken feature. No behavior change — the caveat now leads both `create_page`'s and `update_page`'s description text instead of following the "what this flag does" explanation.

## [v1.6.3] - 2026-07-25

### Added
- **`create_page` gains an opt-in `test_content` marker** (#661): a live audit noted that a disposable test page it created was accepted with no creation-time signal that it was test/throwaway content — `validate_frontmatter`/`validate_site`'s `test_content_slugs` (#584) and the post-build advisory (#608) both only ever act after the fact. `test_content: {ttl_hours?, owner?}` (default `ttl_hours`: 24) is a deliberate, explicit opt-in — never inferred from `slug`/`title`, so a real published page that happens to start with e.g. `codex-` is never wrongly constrained. When set, it forces `draft: true` regardless of any other setting and writes `test_content`/`test_content_owner`/`test_content_expires_at` into the page's own frontmatter; the effective expiry is echoed back in `data.test_content_expires_at`. `build_site`/`publish_changes`'s post-build advisory (#608) now honors `test_content_expires_at` unconditionally, independent of the server-wide `stale_test_content_threshold_hours` setting — the caller explicitly asked for TTL tracking on that specific page, so it keeps working even when the server-wide sweep stays disabled. Still report-only: it never auto-deletes, only surfaces a warning recommending `delete_page`.

### Added
- **New `get_changelog` anonymous-tier read tool** (#612): returns CHANGELOG.md entries (`version`, `date?`, `body`) — an agent auditing the live server previously had no way to ask "what changed since vX.Y.Z" without already knowing to fetch the raw file from GitHub, or blindly re-testing the entire tool surface on every audit. CHANGELOG.md is not shipped to disk in production (`deploy.yml` deploys only the compiled binary), so its content is embedded at build time (`embed_changelog.go`, module root — Go's `//go:embed` cannot reach outside its own package directory, hence a new root-level file) rather than read from a runtime path, meaning the served changelog always exactly matches the running binary with zero drift. Without arguments, returns the 5 most recent versioned releases (bounded by default, never a full dump); `since_version` returns every release strictly newer than that version instead, failing `invalid_params` if it doesn't match a real release heading. `limit` caps the count either way (default 5, max 20). Each entry's `body` is the release section's raw Markdown verbatim, not further parsed into structured Added/Fixed/Security subsections — reuses `internal/releasecheck`'s existing heading-parsing convention rather than inventing new parsing. Anonymous tier, matching `list_tags`/`list_categories`: the changelog is already public on GitHub, so gating it adds no real confidentiality, only friction for exactly the kind of unauthenticated audit session this tool exists to help.

### Added
- **New `plan_page` read tool** (#622): a pre-writing scaffold bundling three calls into one before writing the first line of a new article — `data.content_types`/`data.special_files` (byte-identical to `list_content_types`), `data.suggested_links` (byte-identical to `suggest_links`, populated when `tags`/`categories` is provided), and `data.relevant_tags`/`data.relevant_categories` (the subset of the site's existing tag/category vocabulary matching the optional `topic` or any submitted `tags`/`categories`, via a case-insensitive substring match in either direction — this also surfaces an existing differently-cased spelling for a tag/category about to be introduced, e.g. submitting `tags: ["Debug"]` when the index already has `"debug"`). All three facets reuse the exact underlying logic of their standalone tools (`list_content_types`'s computation extracted into a shared `computeContentTypes` helper, `suggest_links`'s existing `scoreLinkSuggestions`) rather than duplicating it — the only new logic is the tag/category substring match.
- **New `list_page_revisions` read tool** (#615): returns the prior git commits touching a page's source file, most recent first (`commit`/`short_commit`/`date`/`subject`) — the read-side "what could I revert to" answer, independently useful (e.g. comparing `diff_page` against an older revision, not just `HEAD`) and a deliberately conservative first step before any write-path rollback tool. Requires a local Git repository and configured content root, same as `diff_page`; `status: "git_unavailable"` (empty `revisions`, explanatory warning) is returned rather than failing outright when git metadata can't be resolved. `limit` caps returned commits (default 20, max 100); `--follow` tracks renames across history. A real `rollback_page` write tool remains a separate, larger design question — it would need to interoperate with `expected_revision`/idempotency/the in-memory index/rate limits the same way `update_page` does, not become a second, possibly-inconsistent mutation path — and is explicitly out of scope for this read-only first step.

### Added
- **`index_staleness` gains a coarse `likely_source` hint** (#617): `get_broken_links`/`get_backlinks`/`get_related_content`'s existing `index_staleness` field (#583) tells a caller the in-memory index is behind on-disk content, but not *why* — a manual out-of-band edit (e.g. direct SSH/git, the root cause of a July 20 `get_backlinks` false-positive investigation) looks identical to this server's own recent, expected pending write. `likely_source` is now `"mcp_pending_build"` when any source page is currently marked `BuildPending` (the same bookkeeping `create_page`/`update_page`/`apply_content_plan`/`rollback_change` already maintain, cleared on the next successful build) or `"external_or_unknown"` otherwise. Deliberately a coarse binary, not per-caller/per-session attribution — no new identity bookkeeping, reusing existing state entirely (new `hugosite.SourceIndex.HasPendingBuild()` helper).
- **`update_page` reports a per-term tags/categories delta breakdown** (#645): `update_page`'s `tags`/`categories` are a whole-list replacement, unlike `plan_content_change`'s per-operation `add_tag`/`remove_tag` vocabulary and its `operations_applied`/`operations_rejected` reporting — a live audit specifically praised that granularity as superior to `update_page`'s single whole-call diff. `data.tags_delta`/`data.categories_delta` (`added`/`removed`/`unchanged`) now compare the submitted list against the page's current value, on both `dry_run` and a real write, whenever the corresponding key is present in the request at all (an explicit empty list is a valid "clear them all"; omitting the key entirely leaves the field unchanged and reports no delta, matching `update_page`'s existing nil-means-unchanged contract). Narrowly scoped to tags/categories only, not a full retrofit of `plan_content_change`'s operation vocabulary onto `update_page` — `update_page` remains the right tool for a caller that already knows its exact final content and doesn't need a preview step.

### Added
- **New `force_dry_run_all` server config flag** (#611): when `true`, overrides every mutation tool's per-call `dry_run` argument to `true` server-wide — `create_page`, `update_page`, `delete_page`, `upload_page_asset`, `delete_page_asset`, `apply_content_plan`, and `rollback_change` all become read-only previews regardless of what a caller passes, including an explicit `dry_run: false`. Lets an operator safely exercise the full write-tool surface during a live audit or CI smoke run without every caller having to remember `dry_run: true` on every call. Preserves dry-run's existing "never consumes rate-limit quota" property. Deliberately a single server-wide config flag rather than a per-caller/per-session mechanism, matching this repo's preference for the simplest mechanism proportionate to the actual use case. Each affected tool's response still reports `data.dry_run: true` as normal, so the override is directly visible to the caller, not silent.
- **New opt-in post-build advisory for forgotten test/audit content** (#608): `validate_frontmatter`/`validate_site`'s existing `test_content_slugs` field (#584) only ever detects leftover test-prefixed content (`mcp-audit-`/`test-audit-`/`codex-`) when an operator or agent thinks to call one of those tools. The new `stale_test_content_threshold_hours` config setting (off by default) makes `build_site`/`publish_changes` proactively check for such content past a configurable age, surfacing it both in server logs and in that call's own `data.warning` — reusing the existing post-build-callback-warning mechanism (#644) rather than adding new scheduling infrastructure. Report-only: it never deletes or modifies anything. The reserved-test-prefix detection logic itself moved to a new shared `internal/contentmodel.IsReservedTestSlug` helper so both the existing on-demand detection and this new proactive check agree on exactly the same definition of "test content."
- **`get_page_for_edit` gains an opt-in `readiness` include facet** (#621): `include: ["readiness"]` returns the same source-structure audit `check_ai_readiness` reports for the slug (`status`/`checks`/`warnings`/`suggestions`), identical to a standalone `check_ai_readiness` call. Combined with the existing `preview` (rendered/SEO/broken-links) and `quality` (frontmatter validity) facets, a single `get_page_for_edit(slug, include=["preview","quality","readiness"])` call now covers the full pre-publish check an agent previously had to assemble from three separate tool calls (`check_ai_readiness` + `inspect_rendered(include_preview=true)` + `get_broken_links`). Opt-in only, never part of the default bundle; a page with no matching source (public-only legacy content) omits `readiness` with a warning instead of failing the whole edit-prep bundle, matching `preview`'s existing fallback behavior for a page with no rendered output.

### Added
- **New `get_rate_limits` read tool** (#614): reports the caller's current remaining budget on both independent per-caller mutation quotas (`create_update_upload`, shared by `create_page`/`update_page`/`upload_page_asset`/`apply_content_plan`/`rollback_change`; `destructive`, shared by `delete_page`/`delete_page_asset`) as `{remaining, limit, retry_after_seconds}`. Previously the only way to learn remaining quota was after the fact, via a mutation call's own `rate_limit_remaining` field — a caller mid-way through a batch of writes had no way to check headroom before continuing except by risking a `rate_limit_exceeded` on the next call. Reuses the exact `callerLimiter`/`rateLimitRemaining`/`rateLimitRetryAfterSeconds` machinery the mutation tools already share (#378, #466) rather than duplicating rate-limit logic; calling it is a pure read and never itself consumes either quota. Requires `content.write`, the same trust level as the tools whose quota it reports.

### Fixed
- **`PostBuildSync` now prunes DB rows for pages no longer in the sitemap, instead of only ever upserting** (#646): `delete_page` performs a best-effort `siteDB.DeletePage` call independent of the disk removal, reported via the existing `partial_success`/warning convention when it fails. Previously, the *only* code path that ever cleaned up a page row left behind by such a failure was `StartupSync`, which runs once at process boot — on a long-running, low-traffic deployment, a failed delete could leave a stale row (and a stale `search_content` hit for a page that no longer exists) in place for weeks until the next restart. `PostBuildSync`, which already runs after every `build_site`/`publish_changes`, now also deletes any `published=1` row whose slug is absent from the current sitemap, so the gap self-heals on the very next build instead of waiting for a restart. This is a narrower, more honestly-scoped fix than the issue's original "consolidate all SQLite sync into one transactional path" framing — the DB-layer writes (`SyncSourcePage`/`DeletePage`) were already each wrapped in a single transaction, and the two bugs that originally motivated the issue (#643, #589) turned out on inspection to be unrelated to DB sync atomicity. Backed by a new regression test, `TestPostBuildSyncPrunesStalePublishedPages`, which fails without the fix.
- **`get_mutation_status` no longer leaks another caller's mutation result across the OAuth client boundary** (#627): the idempotency store backing `create_page`/`update_page`/`delete_page`/`upload_page_asset`/`delete_page_asset`'s replay-on-retry behavior, and `get_mutation_status`'s read-only lookup, was keyed only by `(tool, idempotency_key)` — any write-scoped caller who knew or guessed another caller's `idempotency_key` for a given tool could read that caller's mutation result (`replay` additionally required a matching `request_hash`, but `lookup`, which backs `get_mutation_status`, did not). Fixed by threading a stable per-bearer-token identifier (`oauth.CtxTokenID`, the same hash already used to key the access-token store, populated in `bearer_middleware.go`/`server.go` alongside the existing `oauth.CtxScope`/`oauth.CtxCallerIP`) into every `replay`/`remember`/`lookup` call, so the store is now namespaced by `(callerToken, tool, idempotency_key)`. Chose token-hash scoping over persisting OAuth client IDs on access tokens (a design that would have required a schema change across all three token-storage backends, memory/JSON/SQLite): the default access-token TTL (1h) is longer than the default idempotency-key TTL (15m, #616), so a same-client token refresh mid-window is negligible in practice, and every distinct bearer token already belongs to exactly one client. Deployments with OAuth disabled are unaffected (no bearer boundary to enforce; all callers share one bucket, matching pre-fix behavior for that mode). Backed by a new discriminating regression test, `TestIdempotencyStoreIsolatesByCallerKey`, confirmed to fail against the pre-fix two-argument cache key and pass with the fix.

### Documentation
- **New README section documenting the multi-page editorial call pattern** (#631): plan and apply each page individually (`plan_content_change` → review preview → `apply_content_plan` right away, since `plan_id` is single-use with a 5-minute TTL), tracking each page's `plan_id`/`revision` for `rollback_change`, then call `publish_changes` once for the whole batch. No new orchestration tool or behavior change — the existing per-page revision pinning and per-page rollback already compose into this pattern.

## [v1.6.2] - 2026-07-25

### Documentation
- **`list_content_types`/`explain_structure` now document that a root-level single-file page is listed as its own one-off content type/section** (#642): observed live as a throwaway test page showing up as a distinct "content type" with `page_count: 1` — confirmed to be existing, intentional behavior (consistent with other real root-level pages like `hall-of-fame`/`privacy-policies`), not a regression, so this documents the behavior in both tools' descriptions (and `explain_structure`'s own `notes` field) rather than changing it, per the issue's own "discuss before implementing" guidance on any actual behavior change.
- **Confirmed `normalize_taxonomy_casing` matches correctly against real bilingual content when `lang` is passed explicitly** (#589, reopened): two independent live audits against `v1.6.1` reported the feature as a no-op. New regression test `TestUpdatePageNormalizesCategoryCasingOnExplicitBilingualLang` reproduces the exact scenario the audits raised — a real bilingual site (pages saved as `index.fr.md`/`index.en.md`, not the single-language `index.md` every other existing test used) with an explicit `lang: "fr"` on `update_page` — and confirms the mechanism resolves and rewrites casing correctly within that language bucket. This narrows the root cause to #604's already-documented lang-scoping caveat (omitting `lang` on a call resolves to the empty-string bucket, which has no forms to match against on a site where every real page specifies `lang`): the two audits most likely omitted `lang`, not a code defect in the matching logic itself. No code change; the contract already documents this caveat correctly (#604) — this closes out the "is it broken or just under-documented" open question with a passing test against the previously-untested real-bilingual scenario.

### Fixed
- **`build_site`/`publish_changes` now identify *which* post-build callback failed or timed out, by name, instead of an opaque positional index** (#644): `data.build.warning`/`data.warning` previously read "post-build callback 2 timed out after 30s" — meaningless without reading `internal/server/server.go`'s wiring order to know callback 2 is the CDN purge, not the DB reindex or search-index submission. `publish_changes`'s own audit found this exact gap: it correctly detected and reported `status: "build_succeeded_unverified"` when a callback timed out, but nothing downstream (`run_post_build_hooks` re-fires hooks fresh each call and retains no history; `get_runtime_status`'s `last_build` only tracks the build itself, not per-callback outcomes) could say *what* had failed. Fixed by introducing `admin.PostBuildCallback{Name, Fn}` — `RegisterBuild`/`RegisterPublishChanges`/`runBuild`'s callback lists now carry a stable name (`index_reload`, `db_reindex`, `cloudflare_purge`, `search_index_submit`) alongside each function, and the warning message now reads e.g. `post-build callback "cloudflare_purge" failed: cdn purge failed`. `run_post_build_hooks` (the separate, stateless, operator-configured *webhook* firing tool) is unaffected — this is specifically about the internal side-effect callbacks (`siteReload`) that already run as part of every `build_site`/`publish_changes` call, not that tool.
- **Mutation tools no longer duplicate `rate_limit_remaining` under `data` on success responses** (#520, #605): `create_page`, `update_page`, `delete_page`, `upload_page_asset`, `delete_page_asset`, `apply_content_plan`, and `rollback_change` all populated their success `data.rate_limit_remaining` with the exact same value already reported at the response root — contradicting `docs/mcp-contract.md`'s own claim of "no other top-level payload duplication as of v1.5.7 (#520)" for these tools. Root cause: each tool's `new*Output` constructor read the root-level value straight off the typed `*Data` struct passed to it, and every success-path call site set that struct field to the same `rateLimitRemaining(limiter)` value used for the root field — so the "single canonical value, mirrored at construction time" pattern silently produced two copies instead of one. #520 first fixed this for `create_page`/`update_page`/`upload_page_asset`/`delete_page` in `v1.5.7`; two later live audits against `v1.6.1` found it recurring on `build_site`/`check_sri_versions` (tracked separately, #605/#640) and, on closer inspection prompted by that recurrence, still present on `apply_content_plan` and `rollback_change` — tools added after `v1.5.7`'s fix that copied the same (buggy) pattern rather than a structurally enforced one. Fixed by making `rateLimitRemaining` an explicit parameter to every `new*Output` constructor instead of a field read off `data`; the `*Data` structs keep the field (`omitempty`) only because write-tool *error* responses intentionally still carry `rate_limit_remaining` in both places (`request_context`/`rate_limit_remaining` are the two deliberately-kept root+data fields on the error path, #466/#510/#522, sharing one `OutputSchema` with the success path) — success-path call sites simply never populate it anymore. Root `rate_limit_remaining` is unaffected on both success and error. Per #605's explicit ask, this is backed by a new generic regression test (`TestWriteToolSuccessResponsesDoNotDuplicateRootData`, plus a `dry_run` counterpart) that walks every key in a real success response across all seven affected tools and fails on *any* undocumented root/data overlap — not a per-tool, per-field assertion that a future tool could bypass by simply not being added to a list. **Observable delta for clients**: a caller reading `data.rate_limit_remaining` on a success response for any of these seven tools must now read it from the response root instead — root `rate_limit_remaining` itself is unchanged, on both success and error.
- **Negative `limit` is now rejected with `invalid_params` instead of silently clamping to the default** (#641): `list_pages`, `search_pages`, `get_recent_posts`, `get_sitemap`, `get_feed`, `get_related_content`, `export_agent_context`, `search_content`, `get_broken_links`, and `suggest_links` all shared a `clampLimit(v, defaultVal, maxVal)` helper whose `v <= 0` branch treated a negative value exactly the same as an omitted one. A negative limit most likely indicates a caller-side bug (a miscalculated pagination offset/limit); silently substituting the default meant that bug went unnoticed instead of failing fast — asymmetric with the upper bound, which is already schema-rejected with a clear validation error. `limit: 0` is unaffected and continues to mean "use the default" (a real, previously-documented request shape, per `tools.WithMaxLimit`'s own comment on why no schema-level `minimum` is published) — only genuinely negative values are now rejected, via a new `negativeLimitError` check ahead of each tool's existing `clampLimit` call. Backed by a new regression test per package (`TestNegativeLimitRejectedAcrossTools`, `TestNegativeLimitRejectedAcrossReadTools`) covering all ten affected tools plus their `limit: 0` behavior.
- **`rollback_change` now restores the in-memory source index's `Body` field, not just `Tags`/`Categories`/`Title`/`Revision`** (#643): after a real (non-`dry_run`) rollback, `get_page_markdown` — which reads a page's body straight from the in-memory `SourceIndex` entry before the next full rebuild — kept serving the pre-rollback body, while every other field on that same entry (and every other tool: `get_page`'s rendered HTML, `diff_page`'s direct disk read, and `revision`/`tags` on `get_page_markdown`'s own response) was already correctly reverted. `index_state` reported `"fresh"`, giving a caller no signal to distrust the response. Root cause: `rollback_change`'s index-upsert code copied the existing `SourcePage` struct wholesale and reassigned `FilePath`/`Lang`/`Title`/`Tags`/`Categories`/`BuildPending`, but never reassigned `Body` — unlike `update_page`'s equivalent upsert, which does. Fixed by extracting the restored body from the snapshot content (`bodyFromRaw`, matching `hugosite.splitFrontmatter`'s own trimming convention exactly, since both write into the same `SourcePage.Body` field) and assigning it alongside the other restored fields. Reproduced independently twice against production before the fix (a full `create_page`→`plan_content_change`→`apply_content_plan`→`publish_changes`→`rollback_change`→`build_site` cycle both times), and caught by a new regression test that fails without the fix and passes with it.

## [v1.6.1] - 2026-07-24

### Documentation
- **`normalize_taxonomy_casing`'s language-scoping caveat** (#604): `create_page`/`update_page`'s tool descriptions and their row in `docs/mcp-contract.md` now explicitly call out that matching is scoped to the *exact* `lang` bucket of the page being written — on a bilingual site where every real page specifies `lang` explicitly, omitting `lang` on a `normalize_taxonomy_casing` call resolves to an empty-string bucket with no existing forms to match, and silently no-ops. No behavior change; this closes a gap where the no-op looked identical to a broken feature.
- **New "Testing New Parameters and Response Fields" section in `CONTRIBUTING.md`** (#607): PRs adding a new opt-in parameter or new documented response field to an existing tool should include at least one test using a realistic-content fixture (not just the simplest passing fixture) that asserts the tool's *documented* behavior actually occurs.
- **New README sections: slug formats and recommended authoring workflow** (#610, #623): a prominent top-level explanation of the two slug shapes (`slug` on read-tool outputs is the canonical public URL form; `slug` on write-tool inputs expects the source-relative `source_key` form, provided alongside `slug` on every read tool specifically so it can be fed straight into a write tool without reformatting), plus a "recommended authoring workflow" section listing the suggested call order for a fresh article: `list_content_types` → `suggest_links` → `create_page` → `verify_publication`.
- **`create_page`'s tool description now cross-references `suggest_links`** (#623) as a recommended pre-write step, the same way it already cross-references `update_page` for edits.

### Added
- **New tools `plan_content_change`/`apply_content_plan`** (#438, design anchor #338, `docs/transactional-edit-design.md`): a server-held, TTL'd (5 min), single-use preview/apply split for editing an existing page. `plan_content_change` takes a small, deliberately non-general operation vocabulary (`update_body`, `set_title`, `add_tag`/`remove_tag`, `add_category`/`remove_category` — computed as a delta against the page's current tags/categories rather than a full-list replacement, `set_draft`, `set_field` with `field: "description"` only), never writes, and returns a `plan_id` plus the exact diff applying it would produce. `apply_content_plan` takes only `plan_id` and writes exactly what was previewed — no body/title/tags resent, nothing re-derived from fresh input. Fails `plan_not_found` if the plan is unknown, already applied, or expired; fails `revision_conflict` if the page changed since the plan was created. `plan_content_change` requires no scope (planning never writes, #450); `apply_content_plan` requires `write`. On a successful write, `apply_content_plan` snapshots the pre-write content (24h TTL) for `rollback_change` to consume.
- **New tool `publish_changes`** (#438, #340): bundles `build_site` + `verify_publication` into one explicit, separately-confirmed step, so an agent doesn't have to build then remember to separately verify the result actually went live. `data.status` is `"published"` only when the build succeeds cleanly (no failed post-build callback — e.g. a CDN purge that would leave stale bytes cached at the edge) *and* `verify_publication`'s own check reads `"fresh"`; otherwise `"build_succeeded_unverified"`, with `data.build.warning`/`data.publication.status`/`data.publication.explanation` saying which stage is behind. Never auto-chained onto `apply_content_plan`/`update_page` — publishing stays a deliberate, separate call. Requires `write`.
- **New tool `rollback_change`** (#438, #340, #629; **amends #379**): restores a page's source to a prior revision `apply_content_plan` or `update_page` itself snapshotted. #379 originally required a rollback target to be an immutable git commit, never "the state before the last apply" — that assumed the server would eventually make its own git commits, which turned out not to be true (this deployment's content checkout is host-managed, `baseline_mode: auto`, can be dirty, and the server has no git-commit code path). Amended (see #379's comment thread) to accept a server-held snapshot as a valid rollback target, deliberately scoped to revisions `apply_content_plan`/`update_page` themselves produced and snapshotted — not arbitrary git history, and not a general "undo anything" mechanism. `update_page` snapshots its own pre-write content the same way `apply_content_plan` does (#629) — `docs/transactional-edit-design.md` §5 is explicit that `update_page` remains the primary tool most real edits actually use, so scoping snapshot capture to `apply_content_plan` alone would have left most edits permanently un-rollback-able, failing with `snapshot_not_found` indistinguishable from "this revision never existed." `create_page` is deliberately not snapshotted — there is no meaningful "pre-create" state to roll back to (rolling back to "before creation" would mean deleting the page, which is not what `rollback_change` does). Guarded by the same `expected_revision` optimistic-concurrency check every other write tool uses, so it can never silently undo a newer, unrelated change; fails `snapshot_not_found` if no snapshot exists for the requested revision. **Re-runs `create_page`/`update_page`'s blocked-shortcode policy (#590) against the snapshot content before restoring it** — a snapshot is a verbatim copy of whatever the page held *before* the write that produced it, which for `update_page` in particular may predate the denylist or otherwise not have been checked against it; without this, restoring old content would be a way to reintroduce a body direct writes now reject outright (caught by strix-security review on PR #636 before merge). Unlike a plan, a snapshot is not consumed on use (`IdempotentHint: true`). Requires `write`.
- **Idempotency-key TTL is now configurable via `idempotency_ttl_seconds`** (#616), instead of a hardcoded 15 minutes. Controls the retention window for the idempotency cache backing `create_page`/`update_page`/`delete_page`/`upload_page_asset`/`delete_page_asset` and the `get_mutation_status` lookup (#586): a longer window gives an agent more time to positively confirm via `get_mutation_status` whether a mutation landed after a connector-level outage (the 2026-07-12 connector outage was the motivating precedent), instead of falling back to a blind — if always-safe — retry. Defaults to `900` (15 minutes, unchanged from prior behavior). Deliberately a deployment-level setting only, never a per-call tool parameter: a caller-supplied TTL could otherwise be used to shorten the window and evade duplicate-submission protection. A non-positive configured value (`0` or negative) is treated as a misconfiguration and clamped back to the 900-second default at load time, rather than silently disabling replay protection — mirroring how `rate_limit.destructive_per_min`/`rate_limit.create_update_per_min` are already clamped against the same class of accidental zero/negative config.
- **`get_page` gains opt-in `include_terms`** (#618), default `true` (non-breaking, unlike #619's default flip): `tag_terms`/`category_terms` are richer `{label,slug,source}` objects duplicating the same information already present in the plainer `tags`/`categories` string arrays, at roughly 3-4x the bytes — pass `include_terms=false` to omit them when a caller only needs the plain names. Scoped to `get_page` for this release, the tool the originating audit's example was actually about and the only "everyday" read tool in the anonymous/reader tier with `tag_terms`/`category_terms` unconditionally present; `search_content`'s equivalent duplication (`internal/tools/read/extended.go`) is tracked separately as a fast-follow, not silently dropped.

### Changed
- **BREAKING: `get_page`'s `content_only` now defaults to `true`** (#619): previously defaulted to `false`, returning the full rendered page including theme chrome (nav, footer, search widgets, share buttons, scripts) unless a caller explicitly opted into `content_only=true` — observed live to run to several thousand tokens for a short article. `content_only` now defaults to the article-body-only behavior; pass `content_only=false` explicitly to opt back into the old full-page HTML. Applies to the source-fallback path too: with the new default, a source-only page's `html`/`html_origin` behave as they previously did only under explicit `content_only=true` (empty `html`, `html_origin: "none"`) — pass `content_only=false` to get the raw Markdown body back. Shipped as an explicit, CHANGELOG-flagged breaking change (same treatment as #520), not a silent default flip, per the audit's own recommendation.

### Fixed
- **`scripts/smoke-agent-interop.sh`'s anon `tools/call` checks now tolerate a 401 when OAuth is enabled** (#601): `probe_tools_list` already treated a 401 on an unauthenticated `tools/list` call as a pass (the server correctly challenging, not a regression) on a fully-OAuth-enabled deployment with no true zero-auth anonymous tier; the `tools/call` checks further down (`get_site_information`, `get_recent_posts`) had no equivalent tolerance and would always FAIL on such a deployment, independent of any real regression. `mcp_tool_call` now returns a distinct exit code (2) for this tolerated case so callers can skip their own downstream content assertion instead of double-reporting it as a shape/content failure.

### Fixed
- **`delete_page` now removes the matching `generate_hero_image`-generated hero image, closing a disk-space leak** (#606): `generate_hero_image(slug, title, ...)` writes to `{HugoRoot}/static/images/{slug}-featured.jpg` — a location keyed by slug but physically outside the page's own content bundle directory (`{ContentRoot}/{slug}/`). `delete_page`'s `os.RemoveAll(dir)` only ever touches that content bundle, so a page's hero image silently survived every delete, accumulating as an orphaned JPEG under `static/images/` — a real leak surfaced by test content, abandoned drafts, and repeated audits that each create-then-delete a page with a generated hero image. Re-confirmed the gap still existed (no prior fix had landed) before changing anything. Fix: `delete_page` now also attempts to remove `{HugoRoot}/static/images/{slug}-featured.jpg` for the exact slug being deleted, once the source directory is gone. This is safe with no rename/reuse ambiguity: the filename is deterministically derived from the exact slug just deleted, so a different page (necessarily a different slug) can never have its hero image touched by this — and `static/images/` is not used for any other purpose in this codebase (page assets uploaded via `upload_page_asset` live inside the page's own content bundle, not here). Mirrors the existing best-effort cleanup pattern already used for the public-dir/DB/audit-log steps in the same handler: never fails the delete (the source is already gone by this point, so there's nothing to roll back to), and a removal failure — or the image simply never having existed, the common case — is folded into `data.warning` (`status: "partial_success"`) rather than surfaced as an error. No new response field; reuses the existing `data.warning`.

### Security
- **`upload_page_asset`'s SVG allowlist now rejects external `url(...)` references in `fill`/`stroke`/`clip-path`/`mask`** (#626): the structural allowlist parser added for SVG uploads (#571) already restricted `href`/`xlink:href` to a local fragment reference (`"#id"`), but `fill`/`stroke`/`clip-path`/`mask` — all of which can legally carry a CSS `url(...)` paint reference — were allowlisted with no value check, so an uploaded SVG with e.g. `fill="url(http://attacker.example/x)"` passed validation unmodified and would cause the rendering browser to fetch an attacker-controlled or internal resource whenever the asset was displayed. Every `url(...)` occurrence inside these four attributes is now individually validated (not just the first/last, which a naive trim-based check would miss on a value like `url(#a) url(http://evil)`) and must target a local fragment. Independently verified and fixed following a strix-security[bot] report on PR #600.

## [v1.6.0] - 2026-07-24

### Added
- **`get_backlinks`/`get_related_content`/`get_broken_links` (in-memory fallback path) expose `data.index_staleness`** (#583): populated only when the in-memory site index is behind on-disk content — e.g. a manual `hugo` build or direct filesystem edit that bypassed `build_site`/`create_page`/`delete_page` (the only paths that refresh the index). Absence of the field means the index reflects current source. Detected via a cached, stat-only disk walk (30s TTL) to avoid re-walking the site on every read call.
- **`validate_frontmatter`/`validate_site` expose `data.test_content_slugs`** (#584): a slug whose last segment starts with `mcp-audit-`, `test-audit-`, or `codex-` (case-insensitive — a known subset of the throwaway-content prefixes observed in this project's own audit history, deliberately excluding bare `test-`/`audit-` so real content like a published security-audit article isn't misclassified) is listed here, so it surfaces during routine validation instead of only being caught by an external audit days later. Advisory only — never affects `data.invalid`, per-page `issues`, or `data.status`.
- **`get_site_health` exposes `data.advisories_count`** (#591): the total count of taxonomy findings across *both* `info` and `warning` severity, at the top level next to `score`/`status` — deliberately broader than `score_breakdown.taxonomy.advisories` (info-only), so a `casing_variant`/`alias_mismatch`/`possible_duplicate` finding is just as visible as a `translation_pair` one. Not a scoring change — `score_breakdown.taxonomy.weight: 0` staying zero is correct and unchanged; this only fixes a discoverability gap where an agent reading just `status`/`score` at a glance had no way to notice pending findings without deliberately drilling into `score_breakdown.<category>`.
- **New tool `get_mutation_status`** (#586): a read-only way to ask "did my last `create_page`/`update_page`/`delete_page`/`upload_page_asset`/`delete_page_asset` call actually land" after a timeout or otherwise ambiguous response, using the same `idempotency_key` — without resending the original mutation payload. Backed by the existing per-tool idempotency cache (15-minute TTL), so it only ever confirms a *successful* call; a failed/still-in-flight/expired/never-attempted key all report the same `status: "unknown"`, which is never proof of failure — retrying the original call with the same key is always safe regardless. Requires `content.write`.
- **`create_page`/`update_page` gain opt-in `normalize_taxonomy_casing`** (#589): when set `true` (default off), a submitted tag/category that only differs in casing from a single existing spelling elsewhere in the index (same language) is rewritten to that existing spelling before the page is written — preventing new casing drift instead of only letting `get_site_health`'s `casing_variant` finding (#577) report it after the fact. Rewrites are reported in `data.taxonomy_casing_normalized[]`; a term left untouched because the index already has 2+ conflicting spellings for it is reported instead in `data.taxonomy_casing_ambiguous[]` — this feature never guesses which of several already-coexisting spellings is correct, it only prevents a *new* one from being introduced. Also previewed on `update_page`'s `dry_run` path. This *prevents* new drift; it does not *remediate* an already-drifted term — a page resubmitting its own current casing verbatim is a silent no-op, and `casing_variant` findings `get_site_health` already surfaces still require an explicit new value to fix.
- **`upload_page_asset` now supports `.svg`** (#571): previously rejected outright (#348) because raw SVG is XML and can carry `<script>`, event-handler attributes, or external references — accepting arbitrary bytes and serving them from the site would be a stored-XSS vector, and Go's `http.DetectContentType` byte-sniffing (used for the other allowed image types) has no SVG signature to check against. `.svg` uploads are now validated by a strict structural allowlist parser instead: only a fixed set of shape/text/gradient/reuse elements and presentation attributes is accepted; `<script>`, `<style>`, `<foreignObject>`, `<image>`, SMIL `<animate*>` elements, any `on*` event-handler attribute, DOCTYPE/entity declarations, and any `href`/`xlink:href` that isn't a local `"#id"` fragment reference are all rejected outright with `invalid_svg` — never silently stripped and written as a modified file. The exact bytes that pass validation are the exact bytes written to disk — no re-serialization step that could drift from what was checked. Two known, deliberate boundaries: this is a Go XML-layer structural check, not a formal guarantee against every browser's own SVG parsing quirks; and the allowlist is intentionally strict enough that a typical design-tool export (Figma/Illustrator/Inkscape `<style>` blocks, `<metadata>`, editor-namespace attributes) will very likely fail with `invalid_svg` — optimize/minify the SVG (e.g. with SVGO) before uploading if that happens.

### Changed
- **`response_mode=compact` now keeps `meta.release_version`/`commit`/`build_channel`, only trimming `meta.generated_at`** (#567): three independent live audits flagged compact mode's prior meta shape (`schema_version` only) as confusing — an agent running in compact mode couldn't tell which server build answered it. This reverses the narrower #526/#553 decision; `compact` now only ever narrows `data`/row-level payload, never meta's release-identity fields, which are cheap static per-process values with no payload-size justification for trimming.

### Fixed
- **`create_page`/`update_page`/`upload_page_asset`'s `dry_run` no longer consumes the shared create/update rate-limit quota** (#588). A sweep triggered by #575 (which had verified this invariant for `delete_page_asset` only) found these three tools called the rate limiter's `Allow()` before checking `dry_run`, unlike `delete_page`/`delete_page_asset` — so a caller previewing a mutation with repeated `dry_run` calls was silently burning the real budget it would need for the actual write. Fixed so all five `dry_run`-capable tools consistently defer `Allow()` until after the `dry_run` early return.

### Security
- **`create_page`/`update_page` reject bodies invoking a blocked shortcode** (#590): a threat-model pass confirmed that Hugo's own `markup.goldmark.renderer.unsafe=false` setting (already correct on this server's deployed sites) only blocks raw HTML typed directly into Markdown — it does not block a theme-provided shortcode built specifically to bypass that protection. An audit of the deployed LoveIt theme's `layouts/_shortcodes/` (grep for `safeHTML`/`safeJS`/`safeURL`/`safeCSS`/`Scratch` usage, then manual review of each hit) found three genuine bypasses: `{{< raw >}}` (stores its inner content for later unescaped DOM injection), `{{< script >}}` (emits its inner content as a literal `<script>` tag), and `{{< style >}}` (emits its first argument unescaped into a `<style>` rule — CSS injection). All three were previously writable through `create_page`/`update_page`'s `body` field with zero validation. Both tools now reject a `body` invoking any shortcode named in the new `blocked_shortcodes` server config option (default: `raw`, `rawhtml`, `script`, `style`) with `invalid_params`; `update_page` enforces this on `dry_run` calls too. This is server-config-tunable (for themes using a different escape-hatch shortcode name) but is never a per-call opt-out — an agent caller cannot bypass it on a single request. **This is a best-effort denylist seeded from one theme's audit, not a guarantee of a fully closed content-security surface**: an arbitrary theme's own shortcodes (or an unblocked shortcode's own unescaped parameter handling) can still carry equivalent risk, and operators should review their theme's `layouts/_shortcodes/` and extend the list accordingly.

## [v1.5.9] - 2026-07-20

Follow-up from ChatGPT's and Claude.ai's independent live audits of v1.5.8 (both 9.2/10, 31/31 and 25/41 tools tested — no failures, refinement items only).

### Changed
- **BREAKING: finished #520's root/data de-duplication on `create_preview` and `generate_hero_image`** (#573): both tools gained their envelope slightly later than the 5 mutation tools #520 originally covered (via #552, in the same release cycle), and were missed by that convergence. Their success payload now lives only under `data.*` — `preview_id`/`url`/`expires_at`/`build` (`create_preview`) and `path` (`generate_hero_image`) are no longer mirrored at the root. Callers reading those fields at the root must switch to `data.*`.
- **`build_site` now uses the standard structured envelope** (#572): the last tool with zero envelope at all (not even root-level duplication) — success/error responses now carry `success`/`data`/`errors`/`warnings`/`meta` like every other tool. Existing flat fields (`status`, `duration_ms`, `build_id`, `output_revision`, `publish_ready`) are kept as root compatibility aliases — additive, not breaking.
- **`get_site_health` detects same-language taxonomy casing variants** (#577): a new `casing_variant` finding kind catches e.g. `Infrastructure`/`infrastructure` used within the same language — a blind spot `possible_duplicate`/`translation_pair` structurally couldn't see, since `taxonomy.Slug()` already lowercases before either of those checks ever runs. This will surface new findings on sites with existing casing drift; `score`/`status` are unaffected (taxonomy findings never move the top-level score).

### Fixed
- **`validate_site` exposes `data.status`** (`"valid"`/`"invalid"`, #568) instead of requiring callers to derive validity from an empty `pages` list plus a counter.
- **`list_page_assets` now returns `sha256`** (#574), matching what its own description already promised as the way to get the current hash for `delete_page_asset`'s `expected_sha256` guard.
- **`list_page_assets` adds `data.hint` when `data.assets` is empty** (#569), clarifying the tool only covers page-bundle sibling files, not the site's global static assets a page may still reference.
- **`list_pages`/`search_pages`/`get_recent_posts`/`get_sitemap`/`get_feed` expose `source_key`** alongside `slug` (#576), matching `get_page`/`get_page_frontmatter`, so a browsing result can feed directly into a write tool's `slug` input without guessing the expected format.

### Docs
- **`get_feed`'s description now states it's site-wide**, not posts-only (#570) — use `get_recent_posts` for posts-only.
- **`dry_run` quota semantics clarified** (#575): investigated a live audit's observation that `delete_page_asset`'s `dry_run` appeared to consume its destructive quota. A regression test proves it does not — the observed drop is consistent with normal token-bucket refill timing between an earlier real call and the next observation, not a leak. No code change; documented in `docs/mcp-contract.md` §6.4.

## [v1.5.8] - 2026-07-19

**BREAKING.** Follow-up from the v1.5.6/v1.5.7 "ChatGPT tool disabled" incident report (confirmed by production nginx/mcp log audit to be a client-side connector safety trip, not a server regression — zero 5xx/429/unexpected-4xx responses to the ChatGPT connector across the full log history) plus an explicit maintainer field-naming request.

### Changed
- **BREAKING: `meta.server_version` renamed back to `meta.release_version`** (#563, PR #566), at explicit maintainer request. Same value, same always-populated semantics (release tag on a release build, `main-<sha>` otherwise) — only the JSON key name changed. Callers reading `meta.server_version` must switch to `meta.release_version`.
- **`release_version` is now frozen.** This is the fourth change to this one field across four releases (v1.5.5 add → v1.5.6/v1.5.7 merge → v1.5.8 rename back) — churn that an external client audit flagged as a contract-stability problem. The name and semantics will not change again without a major version bump; see `docs/mcp-contract.md` §5.

### Investigated
- **"ChatGPT tool disabled" incident (#565): confirmed not a server-side regression.** Full nginx access/error log review across the incident window and full history shows zero anomalous responses to the ChatGPT connector — the identical healthy request pattern (200 → 202 ack → 200 payload → 499 client-closed SSE) repeats cleanly through all 35 tool-call cycles in the flagged session, with no error on the final cycle. The larger cycle count versus prior successful audits (35 vs. 11–15) points to ChatGPT's own connector-side safety circuit breaker tripping, not a passthrough of a proxied error.
- **nginx `mcp_oauth` rate-limit zone reviewed** (#564): current limits (30r/m, burst 20 nodelay) were not the cause of this incident, but are tight enough that a somewhat larger legitimate multi-call agent session could plausibly trip them in the future. Hardening tracked separately in #564; no config change shipped in this release.

## [v1.5.7] - 2026-07-19

**BREAKING.** Ships #520's preferred remediation as a patch release rather than waiting for v1.6.0 — the maintainer decided to reverse v1.5.6's deferral note once the fix turned out to be low-risk to implement cleanly.

### Changed
- **BREAKING: mutation root/data payload duplication removed** (#520, PR #562): `create_page`/`update_page`/`upload_page_asset`/`delete_page`/`delete_page_asset` success responses no longer mirror their payload at the root — `data.*` is now the sole payload location, matching the read tools' contract. Two root fields are deliberately kept, not part of this removal: `request_context` (error-path only, #455) and `rate_limit_remaining` (#466/#510/#522), so an agent can still self-regulate pacing from the root alone. **Note:** the v1.5.6 changelog said this was "deferred to v1.6.0" — that plan changed; this ships now, one patch release later, as an explicit breaking change rather than a silent contradiction. Callers reading `create_page`'s (etc.) root-level `slug`/`path`/`new_revision`/`state`/`status`/`warning`/`content`/`dry_run`/`diff`/`backlinks`/`filename`/`content_type`/`size_bytes`/`sha256`/`duplicate_of`/`referenced`/`referenced_in` directly must switch to reading the same fields under `data`.

## [v1.5.6] - 2026-07-19

Fast-turnaround fixes from the v1.5.5 live ChatGPT audit and direct user feedback on the same build-identity fields v1.5.5 just added.

### Changed
- **Write tools' `slug` on success is now the canonical public `/posts/x/` form** (#554, PR #559), matching read tools (#519), instead of echoing the raw source-relative input — applies to `create_page`/`update_page`/`upload_page_asset`/`delete_page`/`delete_page_asset`. `source_key` (v1.5.4, #545) remains the stable source-relative identifier for callers that need to feed a value back into another write tool's `slug` input. **Note:** the v1.5.5 changelog said this was deferred to v1.6.0 — it shipped here in v1.5.6 instead, once bundled with #520 turned out to need less coordination than expected (root-alias removal, #520, is still deferred; only the slug format changed here).
- **`meta.release_version` removed from the contract** (#560, PR #561): it was added in v1.5.5 (#550) to expose the deploy-time release identity, but turned out to be pure duplication — `meta.server_version` already *is* the release tag on a release build (and `main-<sha>` otherwise), with `meta.build_channel` distinguishing the two cases. `server_version` + `build_channel` are now the sole version signal across every tool response and `get_runtime_status`.

### Deferred to v1.6.0
- **Mutation root/data field duplication removal** (#520): still needs a documented deprecation window per the v1.x compatibility policy: the root aliases stay in place, not removed.

## [v1.5.5] - 2026-07-19

Fast-turnaround fixes from the v1.5.4 live audits (Claude.ai and ChatGPT), triaged into quick wins (this release) versus breaking changes (deferred to v1.6.0).

### Added
- **`meta.release_version` now honors an explicit deploy-time input** (#550): `deploy.yml` gained a `release_version` workflow_dispatch input that sets `server_version`/`release_version`/`build_channel=release` at deploy time, instead of requiring the git tag to already exist — this repo deploys main, then tags after production validation, so the old exact-tag-only policy could never populate the field for a normal release. `docs/mcp-contract.md` and `docs/operator-guide.md` document the new `-f release_version=vX.Y.Z` deploy → approve → `release.yml` tag sequence.

### Fixed
- **`generate_hero_image` no longer leaks the host's absolute filesystem path** (#551): the returned `path` is now projected to a `hugo_root`-relative logical path (e.g. `static/images/my-post-featured.jpg`), matching the existing convention used for content-root-relative paths elsewhere.
- **`check_sri_versions`/`preview_build`/`create_preview`/`run_post_build_hooks` now return the standard structured envelope** (#552): all four previously returned flat, un-enveloped output; they now embed `toolcontract.ToolResponse[XxxData]` (existing flat root fields kept as compatibility aliases) and route errors through `toolcontract.WrapTool` for consistent structured error responses, matching every other tool.

### Docs
- **Clarified that `response_mode=compact` trims `meta` by design** (#553): ChatGPT's audit flagged `search_pages`'s compact-mode `meta` as looking incomplete; contract tests now prove this is the documented `compact` behavior, not a default-mode regression, and `docs/mcp-contract.md` §5.2 carries an explicit callout so future audits don't re-flag it.

### Deferred to v1.6.0
- **Mutation root/data field duplication removal** (#520) and **write-tool `slug` canonicalization** (#554): both touch the same mutation-tool response surface and are breaking changes against the v1.x compatibility policy; drafted together for review, not merged.
- **Execution planner** (#438): still blocked on `plan_content_change`/`apply_content_plan` (#338/#340) maturing first.

## [v1.5.4] - 2026-07-19

Implementation follow-through on the four design proposals locked in v1.5.3, plus the top priority from the v1.5.3 live ChatGPT audit (complex front-matter preservation proof) and a canonical source-identity field.

### Added
- **`check_ai_readiness` tool** (#437, PR #541): deterministic, source-oriented audit of a page's Markdown/frontmatter — heading hierarchy, section/paragraph length outliers, metadata presence, internal-link density, and citation structure. Explicitly does not score SEO, rendered HTML, or build freshness.
- **`response_mode=compact` extended uniformly across the full read/anonymous tool surface** (#526, PR #540): in compact mode, `meta` trims to `schema_version` only (root `generated_at` is preserved for compatibility); every read tool's input schema now advertises the enum, with contract tests covering all 20 tools.
- **`get_page_for_edit` gains `impact` and `preview` as opt-in `include` facets** (#527, PRs #542, #543), alongside the existing `backlinks`: identical data to standalone `get_related_content(include=["impact"])` and `inspect_rendered(include_preview=true)` calls, sharing the same underlying helpers (no forked logic). A page with no rendered public output yet omits `preview` with a warning instead of failing the whole bundle.
- **`source_key` field added across source-aware read and write tools** (#545, PR #547): a canonical, unslashed source-relative identity (`posts/hello`) distinct from the canonical public-route `slug` (`/posts/hello/`), resolving the slug-format ambiguity flagged in the v1.5.3 audit. Write tools expose it as an alias of their existing (already source-relative) `slug` value — additive, non-breaking.

### Fixed
- **Strong regression proof that `update_page` preserves complex front matter** (#544, PR #546): a new end-to-end test exercises nested maps, lists of maps, translations, custom nested fields, and field ordering, confirming the yaml.v3 node-level rewriter leaves untouched fields byte-identical and does not reorder sections — directly answering the v1.5.3 audit's top trust request.

### Deferred to v1.6.0
- **Mutation root/data field duplication removal** (#520): the v1.5.3 audit reconfirmed `create_page`/`update_page`/`upload_page_asset`/`delete_page` still duplicate their payload at the root in addition to `data`. Removing the root aliases is a breaking change against the now-documented v1.x compatibility policy (#531) and needs a real deprecation window, not a v1.5.x patch.
- **Execution planner** (#438): remains blocked on the transactional-edit primitives (`plan_content_change`/`apply_content_plan`, #338/#340) maturing first.

## [v1.5.3] - 2026-07-18

Follow-up fixes and contract clarifications from the v1.5.2 live audit, plus a real build-pipeline bug found while investigating one of them. Four adjacent design proposals (compact response mode, pre-mutation bundling, AI-readiness rubric, execution planner) were reviewed and locked as design docs but deferred to v1.5.4 for implementation.

### Fixed
- **`diff_page`/`validate_frontmatter`/`validate_site` now return canonical `/posts/x/`-form slugs** (#519, PR #529), including for Hugo section-index pages, instead of the raw source-relative path.
- **`rate_limit_remaining` no longer reports a stale value inside nested `data` on write-tool errors** (#522, PR #530): the field is now derived from the same value mirrored at the root, so the two can never disagree.
- **`build_site` permission-denied hints now distinguish ownership drift from a missing `ReadWritePaths` entry** (#521, PR #532): a `chtimes ... operation not permitted` failure on an existing output file now points at file ownership specifically, not just the systemd write allowlist.
- **`build_site` now passes `--cleanDestinationDir` to Hugo** (#524, PR #539): previously, output for pages deleted since the last build (stale taxonomy list entries, orphaned static assets) was never removed from `site_root`. Live investigation traced a reported broken link on production not to any current content defect, but to twenty builds' worth of accumulated orphaned output from posts deleted over the prior week.

### Docs
- **Reader tool descriptions and docs no longer overstate bearerless access on OAuth-enabled deployments** (#518, PR #528): `search_pages`, `get_page`, `get_sitemap`, `search_content`, and other `read`-tier tools now say a Bearer token is required when OAuth is enabled, rather than "no authentication required."
- **Write-tool root/data field duplication documented as an explicit v1.x compatibility alias** (#520, PR #531), with regression tests proving root and `data` values can never drift apart.
- **`meta.release_version` mainline-build policy made explicit** (#523, PR #533): a normal `main` deploy reports `server_version=main-<sha>`/`build_channel=main` and omits `release_version` by design; only exact-tag deploys populate it.
- **`IsError` documented as a transport-only MCP signal** (#525, PR #534): the canonical JSON contract is `success`/`errors`, never a mirrored `is_error` field in the structured payload.

### Design (deferred to v1.5.4 — not yet implemented)
- **Compact response mode** (#526, PR #535): locks `response_mode=compact` as the single uniform shaping mechanism for the read surface, trimming `meta` to `schema_version` only.
- **Pre-mutation bundle consolidation** (#527, PR #536): locks `get_page_for_edit` as the aggregation point for `backlinks`/`impact`/`preview`, with an equality invariant against the standalone tools.
- **AI-readiness rubric** (#437, PR #538): locks a deterministic, source-oriented check family (heading hierarchy, section/paragraph length, metadata presence, link density, citation structure) for a future `check_ai_readiness` tool.
- **Execution planner scope** (#438, PR #537): locks the planner as an extension of the existing `plan_content_change`/`apply_content_plan` transactional-edit foundation, not a competing orchestration model.

## [v1.5.2] - 2026-07-18

Security-driven release from a same-day live production audit (external ChatGPT/Codex audit + Claude Code, 2026-07-18) of the v1.5.1 deploy, plus the resulting envelope-contract and observability follow-ups.

### Security
- **P0: public Dynamic Client Registration could mint `write`-scope tokens** (#497, PR #505): `resolveRegistrationScope` inherited the scope of any pre-registered client (e.g. `claude-admin`/`chatgpt-write`) whose redirect URI textually overlapped a DCR request — with no secret and no proof of redirect-URI ownership required. A caller could register a public client with a known privileged client's callback URI and obtain a `write` token directly. Public DCR clients now always get `read`; `write` is only obtainable by authenticating with a pre-registered client's own secret. Verified live (registering with Claude.ai's exact callback now returns `read`, not `write`) and covered by a new regression test proving a known `client_id` cannot mint a token without its secret.

### Changed
- **`/mcp` bearer verification now uses the Go MCP SDK's `auth.RequireBearerToken`** (#473, PR #493), via a local compatibility adapter that preserves the existing `WWW-Authenticate` challenge shape, per-tool JSON-RPC ACL, and scope/audit context enrichment — moves the riskiest parsing path onto the actively-maintained upstream primitive with no observable behavior change for existing clients.
- **Remaining top-level envelope duplication removed from 13 read tools** (#495, PR #511): `get_page_markdown`, `get_page_frontmatter`, `get_related_content`, `build_agent_context`, `export_agent_context`, `get_page_for_edit`, `list_content_types`, `list_page_assets`, `search_content`, `explain_structure`, `get_site_health`, `get_broken_links`, `get_backlinks`, `suggest_links`, `diff_page`, `inspect_rendered`, `validate_frontmatter`, `validate_site` now expose their payload only via `data.X` — continues #433's dedup to the rest of the read surface.
- **Mutation tools now populate `data.X` additively** (#508, PR #512): `create_page`/`update_page`/`delete_page`/`upload_page_asset`/`delete_page_asset`/`generate_hero_image` previously left `data` as an empty placeholder with the real payload only at the top level; `data.X` now mirrors the same fields, alongside the unchanged top-level fields (non-breaking, interim state).
- **`meta` now carries release identity** (#509, PR #513): `meta.release_version`, `meta.commit`, and `meta.build_channel` are exposed alongside the existing `meta.server_version`/`meta.schema_version`, so an external audit can confirm which named release it's testing without out-of-band GitHub context.
- **Reader-acquisition discovery metadata now reflects actual deployment mode** (#498, PR #516): `access_profiles.reader.acquisition`/`acquisition_mode` in OAuth discovery metadata previously said "anonymous or self-serve registration" unconditionally; it now derives from the live `oauth.dynamic_client_enabled`/`allow_reader_self_registration` config, so it never overstates bearerless anonymous access when the deployment actually requires self-serve OAuth registration.

### Fixed
- **`rate_limit_remaining` no longer reports a stale/zero value on write-tool error paths** (#510, PR #514): error responses from `create_page`/`update_page`/`delete_page`/`upload_page_asset`/`delete_page_asset` previously left the field at its Go zero value; it now always reflects the caller's real remaining quota, matching the success-path contract.
- **`get_page`'s source-fallback `html` field is now explicitly labeled** (#502, PR #515): new `html_origin` (`rendered_public`/`source_fallback`/`none`) and `rendered_html_available` fields let a caller distinguish real rendered public HTML from source-fallback content without inferring it from `state` alone.
- **Token response omitting granted scope on silent reader downgrade** (#499): resolved as a side effect of #505 — DCR clients always getting an explicit `read` scope (never the previously possible empty string) means `/token`'s `scope` field is never omitted by `omitempty`.
- **`docs/mcp-contract.md` §1.1 "flat envelope" description corrected** (#496, PR #507): the doc claimed flat tools have no `success`/`errors`/`warnings` fields, which was never true for any tool — clarified that "flat" only ever meant a top-level convenience-field duplicate of `data.X`.

## [v1.5.1] - 2026-07-18

Consolidation release driven by three live connector audits (ChatGPT x2, Claude.ai, 2026-07-17/18), focused on OAuth scope simplification, response-contract cleanup, and token-cost reduction on high-traffic tools.

### Changed
- **OAuth scope model collapsed to 2 tiers: `read`/`write`** (#450, PR #472): replaces the older `content.read`/`content.write`/`site.admin` model. `read` is fully ungated (no client secret needed, includes tools that used to require authentication); `write` requires a registered OAuth client and implies `read` plus every build/admin tool. All older scope strings remain accepted as compatibility aliases. `requestedScope` also now skips unrecognized scope tokens in a multi-scope request instead of rejecting the whole request (#449, PR #471).
- **`validate_site` defaults to invalid-only** (#456, PR #492): a no-argument call now returns only failing pages, instead of every page including all-valid ones — the common case (most pages pass) no longer pays full response cost to confirm nothing is wrong. `include_valid=true` (or `invalid_only=false`) opts into the full listing; `pages_checked`/`pages_passed`/`invalid` are unaffected.
- **Duplicate envelope payloads removed from 9 anonymous tools** (#433, PR #494): `list_pages`, `get_page`, `search_pages`, `get_recent_posts`, `list_tags`, `list_categories`, `get_sitemap`, `get_feed`, `get_site_information` previously carried their payload twice (once under `data.X`, once as top-level convenience fields); `data.X` is now the sole canonical location.
- **Root-level `version` field replaced with `meta.schema_version`** (#454, PR #494): the old `version` field was ambiguous — it read like it could mean the server version, but actually meant the response schema version. That signal now lives unambiguously alongside `meta.server_version`.
- **Deprecated `related`/`suggestions` aliases removed** (#453, PR #494): `related_pages`/`suggested_links` were always canonical; the aliases were kept pending #433/#454's resolution and are now gone.

### Added
- **`get_related_content` pre-mutation impact facet** (#434, PR #500): `include: ["impact"]` returns `taxonomy_orphans` (tags/categories with no other carrier), `sitemap_present`/`feed_present`, and this page's own redirect `aliases` — advisory only, opt-in, never blocks a mutation.
- **`inspect_rendered` pre-publish preview facet** (#435, PR #503): `include_preview: true` composes `diff_page`'s git-diff status, a page-scoped broken-link count (sharing the exact same doc-based scan as the tool's own `internal_links` check, so the two can never disagree), and `validate_frontmatter`'s per-page checks into one `risks` list — instead of chaining three separate calls before publishing.
- **`delete_page_asset` tool** (#460, PR #489): removes a single asset from a page bundle, with hash/revision preconditions, a referenced-by-body guard (bypassable with `force`), dry-run preview, and idempotency-key replay.
- **Rate-limit state surfaced on write tools** (#466, PR #488): `create_page`/`update_page`/`delete_page`/`upload_page_asset` responses report `rate_limit_remaining`; `rate_limit_exceeded` errors carry `resolution.retry_after_seconds`.
- **`get_page_for_edit` backlinks facet** (#465, PR #486): opt-in `include: ["backlinks"]` returns impact-analysis data (pages linking here) in the same call, before a risky edit/delete.
- **Structured empty-result explanations** (#458, PR #487): `get_related_content`/`suggest_links` return `empty_reason` (`reason`, `candidates_evaluated`, `minimum_score`) when their result list is empty, distinguishing "no qualifying candidates" from "nothing else exists to compare against."
- **`new_revision` returned directly from `create_page`/`update_page`** (#464, PR #485): usable immediately as `expected_revision` on a following `update_page`/`delete_page`, without an intermediate read.
- **Extended structured error resolution hints** (#461, PR #484) and **preserved request context on write-tool errors** (#455, PR #483).
- **Proactive `build_site` health surfacing** (#467, PR #474) via `get_runtime_status`.

### Fixed
- **`StartupSync` duplicate FTS rows for the same page** (#475, PR #490): a page present in both the public (built) index and the source index was indexed twice, once under each slug form; now deduped, with orphan cleanup for any pre-existing legacy duplicate.
- **`lang` populated immediately on unbuilt pages** (#476, PR #491): `get_page_for_edit`/`get_page_frontmatter`/`build_agent_context` no longer report an empty `lang` for a page read back right after `create_page`, before the next build — it no longer lags behind `resolved_lang`.
- **`get_page` empty slug reports `invalid_params`, not `content_not_found`** (#470, PR #480).
- **`explain_structure` no longer reports language prefixes as sections** (#459, PR #479).
- **Hugo section-index files separated from creatable content types** (#457, PR #478).
- **`search_content`/`get_page_frontmatter` categories regression coverage** (#463, PR #477).

### Docs
- Near-duplicate read tools cross-referenced instead of adding new ones (#436, PR #481).
- Canonical vs. deprecated-alias sibling fields documented (#453, PR #482, superseded by the removal above).

## [v1.5.0] - 2026-07-18

Consolidates v1.5.0-pre1 and v1.5.0-pre2 (see below for full detail) plus two live-production fixes found during connector interoperability testing (Claude.ai, ChatGPT, Le Chat):

### Fixed
- **Live OAuth outage: "reader" scope rejected** (PR #448): `requestedScope`/`normalizeConfiguredScope` didn't recognize the published `reader` scope token, so any client (observed: Claude.ai) that echoed the full advertised `scopes_supported` list back as its `/authorize` request's `scope` parameter had the entire request rejected with `invalid_scope`. Fixed by accepting `reader` as its own distinct canonical scope, kept separate from `content.read` (same rank, different string — `site.IsReaderProfile` and the reader-safe gate key on the literal `"reader"` value).
- **CORS missing on `/register`, `/authorize`, `/token`** (PR #468): these three OAuth endpoints returned a plain 405 with no CORS headers on an `OPTIONS` preflight, blocking any browser-based OAuth client calling them directly via `fetch`/`XHR`. Now matches the CORS policy already used on discovery endpoints.

### Interop
- **Le Chat (Mistral) confirmed working end-to-end** (#424, #341): connects, discovers tools, and completes a full multi-tool session against production. Gemini CLI and GitHub Copilot support is deliberately deferred, not attempted in this release.

See v1.5.0-pre1 and v1.5.0-pre2 below for the full list of underlying fixes and features included in this release.

## [v1.5.0-pre2] - 2026-07-18

Prerelease covering a live connector audit (ChatGPT, 2026-07-17) of
v1.5.0-pre1, plus the Le Chat OAuth discovery fix and write-tool version-
reporting bug found while triaging it.

### Added
- **Structured error recovery hints** (#428, PR #429): `revision_conflict` and `content_not_found` tool errors now carry a `resolution.recommended_tool` (`get_page_for_edit`, `search_pages`) alongside the existing `resolution.action`, so an agent can act on a failure without guessing which tool to retry with.
- **`validate_site` pagination and `invalid_only` filter** (#431, PR #440): `limit`/`offset` paginate the per-page detail rows independently of `pages_checked`/`pages_passed`/`invalid` (which always describe the full scan); `invalid_only` filters the paginated view to failing pages only.
- **Taxonomy cross-language alias detection** (#183, PR #442): `get_site_health`'s near-duplicate tag/category detector now distinguishes a `translation_pair` (the same page bundle tagged in two languages — the site's own localization) from a genuine `possible_duplicate`/`alias_mismatch`, via a new `kind` field. Each finding also carries a `severity` (#419, see below).
- **Published schema `enum`/`maximum` constraints** (#418, PR #443): `search_pages.match`/`response_mode`, `build_agent_context.response_mode`, and every paginated tool's `limit` now publish real JSON Schema constraints in `tools/list`, so a well-behaved client discovers the valid range instead of learning it from a runtime rejection. Deliberately publishes only `maximum` for `limit`, never `minimum` — `clampLimit` treats `limit: 0` as "use the default," a real accepted request, and a `minimum` would break it.
- **`get_site_health` explainable score** (#419, PR #444): additive `score_breakdown` (`{frontmatter, taxonomy}`, each `{score, weight, issues, advisories?}`) and per-finding `severity` (`info`/`warning`) explain *why* `score`/`status` are what they are, without changing their existing formula for any input.
- **Bounded post-mutation publication tracking** (#421, PR #446): `verify_publication` accepts an optional `wait_seconds` (clamped server-side to 20s) to poll internally for build/reindex catch-up instead of requiring multiple round trips; omitting it preserves the original single-check behavior.
- **Docs: `lang` may be empty caveat** (#430, PR #439) on `get_page_frontmatter`, `build_agent_context`, and `get_page_for_edit`, matching the existing caveat on `get_page`.

### Fixed
- **Le Chat MCP server-card discovery** (#424, PR #425): added a `/.well-known/mcp/server-card/mcp` alias and embedded `authorization_servers`/`protected_resource_metadata` pointers in the card itself, after production logs showed Le Chat never reaching the standard OAuth discovery chain. Issue left open pending live re-test.
- **Write-tool `meta.server_version` reported the wrong version** (#426, PR #427): `create_page`/`update_page`/`delete_page`/`upload_page_asset`/image tools were reporting the response *schema* version (`toolcontract.ToolResultVersion`) instead of the actual build/commit version (`buildinfo.Version`).
- **`content_only` no longer includes theme chrome nested inside `<article>`** (#432, PR #441): `ExtractArticleHTML` now prefers `id="content"` over the `<article>`/`<main>`/`<body>` fallback chain, since the LoveIt theme's title/TOC/post-meta/share-buttons/tags/nav live as siblings of the real body inside the same `<article>` wrapper.
- **`inspect_rendered` hreflang check flags an empty `href`** (#420, PR #445): a `<link rel="alternate" hreflang="...">` with no `href` is now reported as incomplete instead of silently accepted; the underlying DOM-based detection was already immune to attribute order/case.

## [v1.5.0-pre1] - 2026-07-17

Prerelease for live OAuth connector testing (Gemini, Le Chat) ahead of v1.5.0.

### Added
- **OAuth flow observability** (PR #412): `HandleRegister`/`HandleAuthorize`/`HandleToken` now emit structured `oauth_register`/`oauth_authorize`/`oauth_token` log lines (`client_id`, redirect URI host, PKCE usage, scope, grant type), correlatable end-to-end by `client_id`, without ever logging secrets (client_secret, auth code, PKCE verifier, tokens). Added to let real connector behavior (which OAuth path a given client actually takes) be reconstructed from server logs.
- **Per-caller mutation rate limits** (#378, PR #422): `create_page`/`update_page`/`upload_page_asset` now share a per-caller-IP budget (`rate_limit.create_update_per_min`, default 60/min), independent of `delete_page`'s existing budget (`rate_limit.destructive_per_min`, now config-driven instead of hardcoded) and independent of the pre-existing per-scope OAuth HTTP rate limiter. A misconfigured `0`/negative limit now clamps to the safe default instead of silently disabling the limiter.
- **Automated path-leak audit + regression test for read-only tools** (#376, PR #423): confirmed no read-only tool (anonymous, `content.read`, or read-only `site.admin`) leaks absolute host filesystem paths, on both success and error response paths. New `internal/contracttests` regression test runs on every `go test ./...`, so future regressions fail CI instead of requiring manual re-audit.
- **Runtime input validation for write tools** (#380, PR #415): `create_page`/`update_page` now validate slug format, title length (255 runes), body size (1MB), and reject null bytes/control characters, before writing to disk. Schema-level (client-side) validation is a documented follow-up (#418), not yet implemented.
- **Git trust model and transactional-edit design docs** (#379, #338, #340, PRs #413, #414): normative documentation of rollback/commit semantics and a full design (not yet implemented) for future `plan_content_change`/`apply_content_plan` and `publish_changes`/`rollback_change` tools.

### Fixed
- **`diff_page`/`get_runtime_status` git dubious-ownership failure** (#416, PR #417): the production git checkout's owner (`jm`) differs from the MCP service account (`mcp-hugo-server-go`), which Git's CVE-2022-24765 mitigation was rejecting outright. New `internal/gitutil` package centralizes all git invocation with a server-resolved `safe.directory`, fixing both tools against the real production repository.

## [v1.4.9] - 2026-07-17

### Added
- **`get_page_for_edit` compact edit-oriented read surface** (#339, PR #408): bundles frontmatter + markdown + lifecycle `state` + `quality` (validity, per-page broken-link count) + `revision` in one call, replacing 2-3 separate reads before an edit. `include` selects a subset; `max_body_chars` truncates the markdown body with a `warnings` entry. `quality.broken_links` scopes the scan to the single page (`site.Index.Classifier()`, a new O(1) cached-classifier accessor, plus a new `brokenLinksForPage` helper extracted from the existing site-wide scan) rather than re-scanning the whole site on every edit. `quality` is omitted for the `reader` profile (source-derived).
- **`list_content_types` content-type/archetype discovery** (#347, PR #409): reports each Hugo content type/section, its archetype template (if any), and expected front matter fields — the union of the archetype's declared keys and the keys actually observed on existing pages of that type, so archetype-less sites (the common case) still get real field guidance instead of an empty list. `page_count` and observed-page-derived fields are omitted for `reader`; archetype metadata (filesystem templates, not page content) remains visible.
- **`list_page_assets` / `upload_page_asset` page-bundle asset management** (#348, PR #410): `list_page_assets` (`content.read`) lists sibling files in a page bundle directory; `upload_page_asset` (`content.write`) writes a new asset into one, with MIME sniffing (never trusts a caller-supplied content type), a 10MB size cap, filename sanitization, exclusive-create (never overwrites), and advisory duplicate-content detection by hash. Allowed types: png, jpg, jpeg, gif, webp — **SVG is intentionally not supported**, since SVG XSS can't be safely neutralized by an allowlist or a hand-rolled sanitizer; that needs a real parser and is deferred to a follow-up. Single-file pages (no per-page directory) are rejected with `not_a_bundle` for both tools. `list_page_assets`'s payload is entirely source-derived (a content-root directory listing); `reader` gets an empty list for a public page rather than an error, and `content_not_public` for a non-public one.

### Fixed
- **`get_site_health` taxonomy inconsistency details now name the affected pages** (#324, PR #407): the existing `taxonomy_inconsistencies` string list explained *that* two tag/category terms looked inconsistent but never *which pages* used them. New additive `taxonomy_inconsistency_details[*]` carries the affected page slugs per finding; the original string list is unchanged for backward compatibility. Omitted for `reader` (source-derived).

## [v1.4.8] - 2026-07-17

### Changed
- **BREAKING: 6 canonical tool names shortened to fit MCP client truncation limits** (#329, PR #405): at least one MCP client connector was observed silently truncating and hash-suffixing tool names of 21+ characters (e.g. `get_full_page_markdown` rendered to the model as `get_ful_7c6ab376aa24`), destroying tool-selection legibility. Renamed in place rather than aliased — MCP clients re-fetch `tools/list` every session, so nothing is hardcoded client-side, but any saved prompts/automation that reference the old names by string must be updated:
  - `generate_featured_image` → `generate_hero_image`
  - `suggest_internal_links` → `suggest_links`
  - `get_full_page_markdown` → `get_page_markdown`
  - `explain_site_structure` → `explain_structure`
  - `validate_front_matter` → `validate_frontmatter`
  - `inspect_rendered_page` → `inspect_rendered`

  Verified scope enforcement is safe across the rename (name-keyed lookup, but the registry is populated fresh at every server start from the same source, and no per-tool grants are persisted). The 20-character length ceiling is inferred from the observed failures, not independently reconfirmed against a live connector; `TestToolNamesWithinConnectorTruncationBudget` enforces it mechanically going forward. Full migration table in `docs/tools.md`.

### Added
- **`search_pages` match scoring and exact-title mode** (#332, PR #404): each result now carries `score` (count of matching query terms), and a new `match: "title_exact"` param returns a strict case-insensitive full-title match — zero results instead of loosely related hits when there's no exact match (e.g. verifying a page's absence after deletion). `site.Index.Search` refactored into a thin wrapper over a new `SearchScored` method; existing callers/tests unaffected.
- **`validate_front_matter`/`validate_site` pagination clarity** (#333, PR #403): added `has_more`/`next_offset` so a global validation call with a small `limit` no longer conflates the full scan scope (`pages_checked`, always the complete matched set regardless of pagination) with the paginated detail-row view (`pages`). Both tool descriptions now document explicitly which counters mean what.

### Documented
- **`search_pages` vs `search_content` tool selection guidance** (#326, PR #402): both tool descriptions now cross-reference each other so an agent with `content.read` scope knows to prefer `search_content` (also matches body text, supports type/language/sort filtering); `search_pages` is for anonymous callers. Docs-only, no behavior change.

## [v1.4.7] - 2026-07-17

### Added
- **`export_agent_context` size guard** (#325, PR #399): new `include_body` param (default `true`) caps `limit` at 10 pages when full Markdown bodies are included, since a 28-page tag previously returned ~900KB with no server-side size guard and MCP has no response streaming. `include_body=false` returns frontmatter + state only, at a higher cap of 50 pages. A `warnings` entry is emitted when a requested `limit` is silently capped. Behavior change: callers that previously passed `limit` 11–50 with the default body-included mode now get a 10-page cap instead.
- **Shared response-shaping contract** (#337, PR #400): new `internal/toolcontract` vocabulary (`response_mode`, `fields`, `include_body`, `max_body_chars`) so read tools can return smaller payloads on request without a proliferation of ad hoc per-tool knobs. `response_mode: compact` implemented on `search_pages` (list/search) and `build_agent_context` (page-read); `fields` selection on `search_pages`; `max_body_chars` (rune-aware truncation) on `build_agent_context`. `full`/`ids_only` modes are reserved vocabulary, rejected as `invalid_params` rather than silently downgraded to `standard`. Omitting all shaping params is a verified no-op — existing callers get byte-identical output. Documented in `docs/mcp-contract.md` §5.2.

### Documented
- **v1.x envelope-nesting compatibility decision recorded** (#328, PR #398): `docs/mcp-contract.md` §5.1 documents why the structured envelope's `data`-nesting (flagged by mcpscan as "Non-Standard Response Wrapping") is a known, accepted tradeoff — live clients depend on the uniform envelope. Decision: no v1.x flattening; any flattened payload ships as an explicit new contract version, never a stealth v1.x patch. Docs-only, no code changes; the shape is already mechanically enforced by `internal/contracttests`.

## [v1.4.6] - 2026-07-17

### Added
- **`get_theme_status` read-only theme diagnostic** (#350, PR #390): reports the active Hugo theme(s)/module imports, on-disk presence, and (for classic `themes/` installs) pinned Git commit + dirty state via `hugo config --format json` and bounded git probes. Read-only — never installs, updates, or fetches theme code.
- **Mutation coordination model documented and regression-tested** (#374, PR #391): `docs/mutation-coordination-model.md` formalizes the existing `hugosite.ContentMu` lock model (write-lock vs read-lock per tool, retry/timeout behavior, the `build_in_progress:` error convention, interaction with `expected_revision`). No production code changes were needed — the existing model already satisfied the acceptance criteria; four new concurrency regression tests (`internal/tools/write/mutation_coordination_test.go`) prove it under `-race`.
- **Structured security audit event trail** (#371, PR #392): new `internal/audit` package layers a consistent `event_type`/`result` vocabulary onto the existing `log/slog` pipeline (no new logging stack). Covers `auth_rejected`, `scope_denied`, `operator_milestone`, `mutation`, and `admin_operation` events; the latter two ride on the existing per-call `tool_call` log line rather than duplicating it. Design and event-shape reference in `docs/security-audit-trail.md`.
- **`inspect_rendered_page` rendered HTML/SEO/link validation** (#351, PR #393): validates a page's *rendered* public output — title/meta-description length, canonical URL (checked against an independently-derived expected URL, not the canonical tag itself), hreflang presence on multilingual sites, internal links, missing local images, and a heuristic scan for Hugo shortcode/render-error markers. Complements `validate_front_matter` (source-only) and `get_broken_links` (site-wide, not per-page).
- **`verify_publication` source/build/public/index freshness + live HTTP check** (#346, PR #394): proves a page's source, build, public output, and index all agree on the same revision, and that the public HTTP surface is actually serving it — without requiring SSH access. The HTTP probe always targets `cfg.SiteURL` + the page's own slug, never the page's own `<link rel="canonical">` tag, to avoid a lower-privileged `content.write` actor being able to steer the probe at an arbitrary host.
- **`create_preview` temporary token-gated preview surface** (#345, PR #395): builds source (optionally including drafts) into an isolated directory — never `cfg.SiteRoot` — and exposes it at `{issuer}/preview/{preview_id}/{token}/`. `preview_id` is opaque, the 192-bit `token` is the sole confidentiality boundary (constant-time compared, enforced on every access), the URL expires after `ttl_seconds` (default 900s, max 3600s), and every response carries `X-Robots-Tag: noindex`. New `internal/previewstore` package; design in `docs/preview-workflow.md`. The preview build passes `--baseURL` pointed at its own mount so assets resolve correctly, and the request-logging middleware redacts the token from logged paths.

## [v1.4.5] - 2026-07-16

### Added
- **`build_site` validation-oriented safety signals** (#343, PR #377): `build_site` now hashes the output tree (`output_revision`) and reports `publish_ready`/`partial_success` status distinctly from a hard failure, so agents can tell a successful-but-degraded build (e.g. a post-build callback failure) from one that's actually safe to publish.
- **Local Git baseline model design anchor** (#356, PR #375): `docs/git-baseline-model.md` defines the `git_baseline` config section (`mode: auto|configured|disabled`, `repo_path`, `branch`, `remote`) and the baseline-state vocabulary later issues build on.
- **`get_runtime_status` compact runtime/build/git/site status surface** (#344, PR #389): a single `site.admin` tool reporting server version/commit (via Go's embedded VCS build info, no new `-ldflags` needed), hugo/git availability, and a `degraded` list explaining why other tools (`build_site`, `diff_page`) may be failing — instead of agents having to infer environment health from scattered error messages. Revision hashes are opt-in via `include_revisions` to keep the common case cheap to poll.

### Fixed
- **Partial-failure semantics normalized across write/build/reindex/publication paths** (#372, PR #382): mutation tools now consistently distinguish full success, full failure, and partial success, per `docs/partial-failure-matrix.md`.
- **Build and post-build hook execution isolated** (#373, PR #381): `build_site`/hooks now run with a bounded environment (`boundedCommandEnv`), redirect-rejecting HTTP client for webhooks, and proper child-process group cleanup on timeout.
- **`diff_page` ambiguous `git_not_available` status** (#322, PR #388): now distinguishes `git_unavailable` (no usable Git baseline at all — surfaces the real underlying error) from `git_untracked` (file just isn't committed yet, e.g. right after `create_page`) from `unchanged`/`modified`/`deleted` (a real diff was computed). Also wires `git_baseline.mode: disabled` into `diff_page` so it actually short-circuits instead of always probing the host.

## [v1.4.4] - 2026-07-16

### Added
- **Reader-safe read policy for all read-only tools** (#354, PR #365): introduced `site.AccessProfile` context propagation and `ReaderSafeResolvedPage`, which projects `Source`/`SourcePath` out of resolved pages for the `reader` scope while preserving the full response for `content.read`/`operator`/`site.admin`. Applied consistently at the DTO boundary across all read tools.
- **Self-service reader registration** (#353, PR #366): `registerAgentAnonymous` issues the `reader` scope directly (bypassing the manual claim/approval flow) when `AllowReaderSelfRegistration` is enabled in config. Scope is always server-determined — the client cannot request a higher scope via the exchange request (regression-tested by attempting to inject `scope=site.admin`). `reader` shares `content.read`'s OAuth rate-limit bucket.
- **Operator tool parity tests across clients** (#355, PR #369): added contract tests asserting the same `operator`-scoped tool set is exposed consistently regardless of which MCP client surface (ChatGPT, Claude.ai, Gemini, Le Chat, generic MCP) is negotiating capabilities.

### Fixed
- **Runtime `mcp.Implementation.version` regression coverage** (#327, PR #387): the underlying fix (wiring `internal/buildinfo.Version` into both `serverInfo.version` and `meta.server_version`) shipped in #361/v1.4.3; this closes the issue with the regression test (`TestInitializeExposesRuntimeBuildVersion`) and doc note that #361 had deliberately left out of scope.

## [v1.4.3] - 2026-07-16

### Fixed
- **`meta.server_version` reported a hardcoded schema constant instead of the deployed build version** (#323, PR #361): extracted `internal/buildinfo` to separate the response schema version (`ToolResultVersion`, a stable constant) from the runtime build version (`buildinfo.Version`, set via `-ldflags`). `meta.server_version` now carries the real deployed build; the envelope `version` field is pinned to the schema version. ldflags wiring updated across CI, deploy workflow, Makefile, and local scripts.
- **Tool responses exposed absolute host filesystem paths** (#334, PR #362): added `fileutil.LogicalContentPath` to project resolved source paths to `content/...` at the DTO boundary, applied consistently across anonymous, read, write, and diff tool responses. Internal I/O still uses real paths; only client-facing fields are projected.

### Added
- **Access model design anchor** (#352, PR #364): `docs/access-model.md` documents the verified 31-tool scope matrix, the target `reader`/`operator` external model, and migration decisions for `site.admin`/`system.admin` aliases. Matrix is checked against the real tool registry by `TestVerifiedToolScopeMatrix`, not just prose.
- **Discovery metadata for reader/operator profiles** (#357, PR #383): `access_profiles` (`reader`/`operator`) added additively to both OAuth authorization-server and protected-resource discovery documents, alongside the existing real `scopes_supported`. No authorization or token-issuance logic changed.

## [v1.4.2] - 2026-07-16

### Fixed
- **`create_page` silently overwrote existing content on duplicate slug** (#330, PR #367): switched to an atomic exclusive-create primitive (temp file + `os.Link`, which fails if the destination exists) instead of a stat-then-write path. Duplicate creates now fail with `already_exists`. Also fixed `dry_run` mode, which previously reported a false-positive "would succeed" preview for slugs that already existed.
- **Write mutations had no optimistic-concurrency protection** (#335, PR #359): added a stable `sha256` `revision` to all page-oriented read surfaces; `update_page` and `delete_page` now require `expected_revision` and reject stale values with `revision_conflict`. `delete_page` recomputes the revision under the content lock (not before it) to close a race window while waiting for the lock.

### Added
- **`idempotency_key` replay safety for write mutations** (#336, PR #360): `create_page`, `update_page`, and `delete_page` accept an optional `idempotency_key`; replaying the same request returns the original result without reapplying the mutation, and reusing the key with different input returns `idempotency_conflict`. The replay check runs under the content lock so genuinely concurrent retries can't both miss the cache.

## [v1.4.1] - 2026-07-13

### Added
- **`get_related_content` four-way editorial response** (#273, PR #315): the tool now returns all four editorial surfaces — `related_pages`, `backlinks`, `suggested_links`, and `translations` — in a single response. A new `collectBacklinks` helper wraps `idx.GetBacklinks`; `scoreLinkSuggestions` is reused for link candidates. Golden contract fixture and unit tests updated.
- **Explicit `Prompts` and `Resources` capability declarations** (#318, PR #321): `defaultServerCapabilities()` helper extracted from `server.New`; `Prompts{ListChanged:true}` and `Resources{ListChanged:true,Subscribe:true}` now match the capabilities the SDK was already advertising at runtime. Unit test and server-card contract test added.

### Fixed
- **Agent-ready smoke scripts required legacy `system.admin` scope** (#317, PR #319): `check-agent-ready.sh` was asserting `system.admin` must be present in `scopes_supported`, inverting the canonical contract. Added `expect_not_contains` helper and a 135-line regression harness (`test-check-agent-ready.sh`) wired into CI.
- **Public `www.arleo.eu` discovery aliases returned 403** (#316, PR #320): `/.well-known/oauth-protected-resource/mcp` and `/.well-known/mcp/server-card.json` were missing from the OpenResty reference config. Added redirect `location` blocks in both HTTP and HTTPS server blocks. Removed `system.admin` from the static `oauth-protected-resource` artifact. CI lint (`test-agent-ready-www-surface.sh`) added to prevent future drift.

## [v1.4.0] - 2026-07-13

### Added
- **Shared contentmodel and toolcontract foundations** (#276, PR #289): extracted `contentmodel.PageIdentity`, `toolcontract.ToolResponse[T]`, and `toolcontract.NewMeta` into dedicated packages; all read tools now emit versioned structured envelopes with canonical `success/data/errors/warnings/meta` fields.
- **Canonical page identity across all tools** (#271 #272, PR #291): every page read tool now returns `resolved_source_path`, `resolved_lang`, and `State` (lifecycle state) consistently. The page resolver uses a 3-tier source lookup: slug+lang → default-lang → any-slug.
- **Self-descriptive pagination metadata** (#295): all list responses include `returned_count`, `has_more`, and `next_offset` to remove the need for clients to compute pagination state.
- **Lifecycle state for page reads and writes** (#296): `source_state`, `build_state`, `public_state`, and `index_state` exposed on all page reads (`get_page`, `get_full_page_markdown`, `get_page_frontmatter`, `diff_page`, `get_related_content`) and populated by write operations.
- **`diff_page` explicit fallback state** (#287, PR #294): when git is unavailable the tool now returns a structured `git_unavailable` state rather than propagating an error, matching the production-VM scenario.
- **Translations separated from editorial relations** (#273, PR #301): `translations` field carries same-content/different-language variants; `related_pages`, `backlinks`, and `suggested_links` are distinct editorial/structural surfaces.
- **MCP schema resources published** (#299, PR #307): the server exposes a `mcp://schemas/` resource prefix with machine-readable JSON Schema for each tool's input and output.
- **Write tool idempotency annotations** (#298, PR #303): `create_page`, `update_page`, and `delete_page` carry `idempotent`/`non-idempotent` annotations in their MCP descriptions for agent-side retry safety.
- **Structured agent-readable tool errors** (PR #309): all tool errors include a machine-readable error code prefix (`content_not_found:`, `invalid_params:`, etc.) before the human-readable message.
- **Unified read tool envelopes with v1 aliases** (#278, PR #310): `searchContentEnvelope`, `brokenLinkOutput`, `getBacklinksOutput`, and `suggestInternalLinksOutput` all embed `toolcontract.ToolResponse[T]` and expose top-level v1 compatibility aliases for smooth client migration.
- **Lifecycle state across rich read tools** (#290, PR #311): `explain_site_structure`, `search_content`, `list_pages`, `get_recent_posts`, `get_related_content` all populate `State` via `site.StateForResolvedPage`.
- **Golden contract fixtures** (#277, PR #312): `assertGoldenJSON` test harness validates `get_page`, `list_pages`, and `get_related_content` output stability across refactors.

### Fixed
- **`get_sitemap` taxonomy exclusion** (#208, PR #292): `exclude_taxonomies` option now correctly omits taxonomy list pages from the sitemap output.
- **Two-space YAML list indentation in `update_page`** (#288, PR #293): front matter tags/categories lists are now written with the Hugo-standard `  - value` style instead of `- value`.
- **Multilingual source resolution across read tools** (PR #300): `list_pages`, `get_recent_posts`, `search_content`, and `explain_site_structure` now pass `siteRoot` to source enrichment so multilingual bundle pages receive correct lifecycle state and source paths.
- **Sitemap taxonomy exclusion correctness** (PR #302): `IsContent` classifier now correctly excludes taxonomy term list pages (e.g. `/tags/go/`) from content counts and broken-link scans.
- **Preferred language source variant** (#271 #272, PR #313): `source_index.rebuildMaps` now maintains a dedicated `bySlugLang` map so the resolver picks the language-specific bundle (`index.fr.md`) over the default-language fallback when both exist.

### Changed
- `pageDTO` gains `resolved_lang`, `resolved_source_path`, and `state` fields (all tools).
- `RegisterWithSourceIndex` accepts a variadic `dbs ...*db.DB` parameter for the optional SQLite index.
- `write.Register` accepts `siteDB *db.DB` for write-triggered DB invalidation.

## [v1.3.9] - 2026-07-13

### Added
- **OAuth refresh-token renewal** (#270, PR #283): `HandleToken` now dispatches on `grant_type`;
  `exchangeRefreshToken` validates client authorization against a new `GrantTypes` field on the
  `client` struct (RFC 6749 §10.4). The hollow `exchangeToken` stub is removed. DCR-registered
  clients receive `["authorization_code","refresh_token"]`; static-registry clients (no `GrantTypes`
  field) are treated as supporting all standard grants for backwards compatibility.
- **`delete_page` dry-run** (#267, PR #284): `delete_page` now accepts `dry_run: true` and returns
  the page content and backlink list without deleting, matching the contract of `create_page` and
  `update_page`. The `backlinks` field is typed `*[]backlink` so an empty backlink list serialises as
  `[]` (not omitted) while the field is absent on non-dry-run responses.

### Changed
- **`get_page` source-index fallback contract documented** (#268, PR #286): `SourceSlugCandidates`
  now carries an explicit contract comment (priority order, language-prefix stripping, callers must
  break on first match). The `get_page` tool description spells out that `html`, `lang`, and `url`
  fields come from the public index and may be absent for drafts or source-only pages.

### Fixed
- **Slug normalisation across write tools** (#265, PR #284): `create_page`, `update_page`, and
  `delete_page` all strip leading/trailing slashes from the input slug via a shared
  `normalizeInputSlug` helper, so agents that pass `/posts/foo/` and `posts/foo` reach the same
  content directory and source-index entry.
- **`delete_page` silent success on missing slug** (#266, PR #284): previously returned an empty
  success when the target was already absent; now returns a structured `not_found` error.
- **Categories/tags empty for non-default-language pages** (#264, PR #280): `list_pages`,
  `get_recent_posts`, and `explain_site_structure` now enrich pages whose public path carries a
  language prefix by stripping the prefix before the source-index lookup.
- **`explain_site_structure` recent pages bypassed source enrichment** (#258, PR #281): recent-pages
  path in `explain_site_structure` now goes through the same source-index category/tag enrichment
  used by `list_pages`.
- **MCP session lifecycle observability** (#259, PRs #269 #282): structured log lines emitted on
  session connect and disconnect; `withDefaultLogger` test helper carries a `t.Parallel()` safety
  warning; SSE flush hygiene improved to avoid buffered-writer stalls.
- **`update_page` dry-run diff label** (#257, PR #262): `update_page` dry-run header no longer
  hard-codes `index.md`; the resolved multilingual path is used instead.
- **Explicit `InputSchema`/`OutputSchema` on all tools** (#253, PR #261): all MCP tools now declare
  both schemas explicitly so static scanners (mcpscan.dev) can inspect them.

### Tests
- **Property-based invariant tests** (#250, PR #254): replayable property checks for
  create/update/delete write coherence; public ⊆ source invariant verified on each mutation.
- **Fuzz smoke** (#251, PR #255): targeted fuzz corpora for path safety, taxonomy slugs, and
  front-matter parsing.
- **Local soak harness** (#249, PR #256): long-running mutation and build stability harness
  exercisable locally without CI.
- **Core benchmarks and invariant matrix** (#252, PR #260): `BenchmarkCreatePage`,
  `BenchmarkUpdatePage`, `BenchmarkDeletePage`, plus a reference table of expected invariants.

### Refactored
- **Write-tool test helpers consolidated** (PR #285): five near-identical `newTestServer*` functions
  replaced by a single `newTestServer(t, root, ...testServerOpts)` accepting optional
  `SiteRoot/SiteDB/SiteIdx` overrides and returning the source index for post-call inspection.
- **`normalizeInputSlug` extracted** (PR #285): the repeated `strings.Trim(slug, "/")` expression
  now lives in one named helper with a clear contract comment.

## [v1.3.8] - 2026-07-12

### Added
- **SQLite-backed derived index** (#221): optional persistent index controlled by `db_path` in
  config (falls back to existing in-memory behaviour when unset). Phase 1: core `pages`, `page_tags`,
  `page_categories`, and `links` tables with write-triggered invalidation (`create_page`,
  `update_page`, `delete_page` sync to DB in-process after file write). Phase 2: FTS5 virtual table
  (`page_fts`) makes `search_content` use ranked full-text search with `<<highlighted>>` snippets
  instead of a linear keyword scan. Phase 3: `site_health_snapshots` table for history (written by
  `build_site` post-build callback). Startup reindex is hash-gated — unchanged pages are skipped.
  `build_site` triggers incremental reindex of the public index after each successful Hugo build.
  DB is always re-derivable from scratch by deleting the file.
- **MCP tool-call observability** (#226): `NewToolCallMiddleware` wired as receiving middleware on
  all four MCP servers (anonymous, content.read, content.write, site.admin). Emits one structured
  log line per `tools/call` with `tool_name`, `scope`, `duration_ms`, `result_class`
  (`success`/`tool_error`/`protocol_error`), and `response_bytes`. Prometheus counters added to
  `/metrics`: `mcp_tool_calls_total{tool,scope,result}` and `mcp_tool_call_duration_ms_total{tool,scope}`.
  No request arguments, page content, or tokens are logged.
- **`suggest_internal_links`** (`content.read`) — new tool that recommends existing published pages
  to link from a draft or page, ranked by shared tags/categories. Accepts `slug` (merges that
  page's taxonomy, including source-only drafts), `tags`, `categories`, and optional `body` (detects
  title mentions using phrase-boundary matching to avoid false positives). Returns structured
  envelope with `anchor_text`, `shared_tags`, `shared_categories`, `score`, and `body_mention`
  (#220).
- **`docs/mcp-contract.md`** — explicit MCP contract document covering both response envelope
  shapes (flat and structured), error model with `snake_case_prefix:` codes, pagination, naming
  conventions, versioning, and per-tool inventory table (#224, #210).
- **`docs/agent-tool-matrix.md`** — agent-first tool-selection matrix: scenario→tool quick
  reference, common workflow sequences (create/edit/delete/validate/link), a decision tree, and a
  disambiguation table for commonly confused tool pairs (#225, #227).

### Changed
- **Tool annotations — `OpenWorldHint` corrected for write and build tools**: `create_page`,
  `update_page`, `delete_page`, and `build_site` now declare `OpenWorldHint: true`, accurately
  reflecting that these operations interact with external systems (Cloudflare CDN purge, IndexNow,
  Google Search Console, filesystem). Read-only and anonymous tools remain `false`. This resolves
  SPEC_006 on mcpscan.dev.
- **`server.New` accepts `...ScopeExtension` hooks**: operators can now register additional MCP
  tools per scope without modifying core packages. Pass one or more `ScopeExtension` functions to
  `server.New`; each receives the scope name and the `*mcp.Server` for that scope, enabling
  `mcp.AddTool` calls at startup. Resolves EASE_004 on mcpscan.dev.
- `list_pages` description: clarifies it returns content pages only (not taxonomy list pages) and
  cross-references `get_sitemap` for the full URL inventory.
- `search_pages` description: cross-references `search_content` for filtered/paginated search.
- `get_sitemap` description: clarifies it includes taxonomy pages by default; cross-references
  `list_pages` for content-only browsing.
- `search_content` description: cross-references `search_pages` for unauthenticated keyword search.
- `validate_site` description: notes equivalence to `validate_front_matter` with no slug filter.
- **Explicit `ServerCapabilities` in `mcp.NewServer`** (#250): all four scope servers (anonymous,
  content.read, content.write, site.admin) now pass `&mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Logging, Tools}}`
  explicitly so static code analysis scanners (mcpscan.dev) can inspect declared capabilities.
  The SDK still auto-merges runtime-detected tool capabilities on top.

### Fixed
- **Build resilience** (#246): Hugo timeout/cancellation now kills the entire process group (not just
  the top-level process) so shell-wrapper scripts and their children are terminated. Post-build
  callbacks run in bounded goroutines with a 30s deadline; `partial_success` + warning is returned
  instead of blocking forever. Optional side-effect callbacks (Cloudflare purge, search indexing)
  swallow errors so only required callbacks can trigger `partial_success`. DB delete and
  public-output cleanup failures in `delete_page` are surfaced as `Warning` fields instead of being
  silently ignored (#238–#244).
- **TOCTOU symlink-swap** (#248): `create_page` and `update_page` now use `AtomicWriteChecked`
  which re-validates the parent directory via `pg.RevalidateForWrite` both before `os.CreateTemp`
  and before `os.Rename`, closing the write-time TOCTOU window. `generate_featured_image` uses a
  guard anchored at `HugoRoot` with `rejectSymlinks` forced `true`, validated before `MkdirAll`,
  to detect symlinked `static/images` regardless of the operator's `RejectSymlinks` config setting.
  `delete_page` audit-log failures are now surfaced as a `Warning` field instead of being silently
  discarded (#233–#235).
- **DCR anonymous scope default** (#249): RFC 7591 dynamic client registration now returns `""`
  (anonymous) scope when the requested redirect URIs don't match any pre-registered client, enabling
  MCP scanners to self-register and reach anonymous-only tools. The `if scope == "" { scope = "content.read" }`
  promotion in `exchangeToken` is removed so anonymous tokens remain anonymous through the full
  PKCE flow. Pre-registered clients (Claude.ai, ChatGPT) continue to inherit their configured scope
  via `resolveRegistrationScope`.

## [v1.3.7] - 2026-07-11

### Added
- **`get_backlinks`** (`content.read`) — new read tool that returns all pages linking to a given
  slug, built from a lazy reverse-link cache (`backlinkCache`) invalidated on every write mutation.
  Orphan pages (zero incoming links) are also surfaced in `get_site_health` (#217).
- **`get_page`**: new `allow_source_fallback` parameter (bool, default `false`) — opt-in to return
  source-index content for pages not yet built by Hugo (e.g. immediately after `create_page`).
  Draft pages are always excluded regardless of this flag. Default behaviour (published-only) is
  unchanged and the API contract is now explicit (#223).
- **`get_page`**: new `content_only` parameter (bool) — strips navigation, header, and footer from
  the rendered HTML of published pages, returning article-only HTML extracted from `<article>` /
  `<main>` (#209).
- **`update_page` / `create_page`**: new `dry_run` parameter (bool) — returns a unified diff
  preview without writing to disk. Uses in-process Wagner-Fischer LCS; no git dependency (#218).
- **`update_page`** now accepts `lang` parameter to target a specific language file on bilingual
  pages (e.g. `lang: "fr"` targets `index.fr.md`). Omitting `lang` on a page with multiple
  language files returns an explicit `ambiguous_language` error (#215).
- **`update_page`** now accepts `tags`, `categories`, `draft`, and `description` fields, enabling
  front matter updates without touching raw Markdown (#214).
- **`build_site`** now reloads the in-memory site index after a successful build so that
  `get_sitemap`, `get_broken_links`, and `search_pages` immediately reflect the rebuilt output
  without a server restart (#212).
- `site.Index.Reload(cfg)` with `sync.RWMutex` — atomic pointer swap of all index fields; read
  methods protected with `RLock` to eliminate data races during concurrent reload.
- Post-build webhooks: Cloudflare cache purge (full zone), IndexNow batch submission, and Google
  Indexing API `URL_UPDATED` notifications fire automatically after every successful `build_site`.
  All three are opt-in via host config only; credentials never committed to git. Taxonomy and
  search URLs are filtered before submission. Google plugin includes a daily quota guard (default
  180/day) with JSON state persistence (#216).
- CI: `TestTotalToolCount` asserts that `Defs()` sum across all packages equals the expected
  constant (30 tools) (#203).

### Fixed
- **`validate_front_matter`** returned silent success (`pages_checked: 0`) when a slug was
  provided but did not match any source page. Now returns `content_not_found` (#222).
- **`validate_front_matter`** false positive "missing date" immediately after `create_page` — the
  in-memory source-index entry now carries the correct `date` populated at creation time.
- **Public site index stale** after `update_page` / `delete_page` between Hugo builds.
  `update_page` now refreshes metadata in the public index when the entry already exists;
  `delete_page` removes it via `RemoveBySlug`; `create_page` no longer injects a premature stub
  (page is source-only until Hugo builds it) (#219).
- **`diff_page`** always returned an empty diff when git was unavailable in production. Fixed by
  falling back to in-process unified diff (#207).
- **`validateFrontmatterRoundTrip`** false positive: a Markdown thematic break (`---`) at the
  top of a body was incorrectly rejected as duplicated frontmatter. Now only triggers when a full
  YAML block (opening + closing `---` within 30 lines) is detected.

## [v1.3.6] - 2026-07-11

### Added
- `get_sitemap` now accepts `exclude_taxonomies: true` to omit Hugo-generated tag, category,
  and author listing pages, returning only content pages (#208).
- `generate_featured_image` uses local Go renderer by default (1200×675 JPEG, Unsplash photo
  background selected by title hash, dark gradient overlay). External API mode is optional.
  Output path corrected to `{hugo_root}/static/images/{slug}-featured.jpg` (#195).
- Operator guide: new "Known Pitfalls" section covering `generate_featured_image` write errors
  (`static/images` must be in `ReadWritePaths`) and stale index after `build_site` (#212).

### Fixed
- `update_page` now works on multilingual pages (`index.fr.md`, `index.en.md`). Previously it
  always resolved to `index.md` and failed with `read_error` on any bilingual bundle. Fixed by
  using `FilePath` from the source index, which is set to the actual discovered file path (#205).
- `delete_page` no longer leaves zombie pages in `public/` after a Hugo build. Previously,
  deleting a page removed the source but left the rendered `public/{slug}/` directory, which
  survived subsequent `build_site` calls because Hugo does not clean by default. Fixed by
  removing `cfg.SiteRoot/{slug}` atomically with the content dir (#213).
- `content/posts/csp-nonce/index.fr.md`: `aliases:` block was duplicated outside the YAML
  frontmatter, rendering as visible HTML text. Fixed on the live VM.
- `validate_front_matter` now returns `pages_checked: 80` (was 0 for valid published slugs) (#206).
- Taxonomy duplicate `postmortem`/`Post-mortems` resolved — list_categories no longer includes
  the stale `post-mortems` alias (#202).
- Broken Grav links in `migration-grav-hugo` article fixed (FR + EN) (#204).

## [v1.3.5] - 2026-07-10

### Added
- **Taxonomy alias map** (`taxonomy_aliases` in config): operators define a slug→slug map
  (e.g. `sécurité: security`) that folds alias terms to their canonical form in all listing
  and filter paths (`list_tags`, `list_categories`, `list_pages`, `search_pages`,
  `get_recent_posts`, `search_content`, `explain_site_structure`). Filtering by canonical
  tag/category now matches pages tagged with any alias form. Near-duplicate tag pairs are
  detected via Levenshtein distance ≤ 2 and reported in `get_site_health` (#183).
- `get_site_health` now includes a `taxonomy_inconsistencies` field listing alias-key terms
  in use and near-duplicate slug pairs that the operator should consolidate (#183).
- `validate_front_matter` now warns when a page's tags or categories use an alias slug
  instead of the canonical form (#183).
- `build_site` and `preview_build` now run a preflight write-check before invoking Hugo.
  A `build_precondition_failed` error is returned immediately when `public/` or
  `resources/_gen/` are not writable, with an `operator_hint` that names the missing
  `ReadWritePaths` entry and the exact `systemctl` command to fix it. Build errors caused
  by permission denial now also carry `suggestion` and `docs_url` fields pointing to the
  operator guide (#186).
- Added `docs/operator-guide.md#build-permissions` section documenting required writable
  paths per tool and the `ReadOnlyPaths` override precedence rule (#186, #190).

### Fixed
- `generate_featured_image` is no longer registered when `image_gen_url` is unset. MCP
  clients no longer see a confusing "available but broken" tool when image generation is
  not configured (#185).
- `list_pages`, `search_pages`, and `get_recent_posts` now populate `categories` from the
  Hugo source index frontmatter when the HTML index has none. Hugo does not emit
  `article:category` meta tags, so the HTML-only index always returned empty categories
  for per-page DTOs (#189).
- Systemd service `ReadWritePaths` configuration documented; deploy script template
  updated to include all paths Hugo needs to write (`content/`, `resources/`, `public/`)
  (#190).

## [v1.3.4] - 2026-07-06

### Added
- A secret-free staging profile is now versioned in-repo via `deploy/config-staging.yaml`,
  `deploy/systemd/mcp-hugo-server-go-staging.service.example`, `docs/staging-runbook.md`,
  and `scripts/staging-smoke-local.sh`. CI now exercises that synthetic staging profile before
  production deploys (#176).
- `internal/taxonomy` is now the shared normalization package for tags and categories. Read tools
  expose consistent `tag_terms` / `category_terms`, and the repo now documents the convention in
  `docs/taxonomy-convention.md` (#175).

### Fixed
- `build_site` and `preview_build` now work with the hardened systemd service layout and return
  actionable build diagnostics, including `exit_code`, `duration_ms`, `working_directory`,
  `build_id`, `log_hint`, and a useful `stderr_summary` even when Hugo only writes to stdout
  (#170).
- `check_sri_versions` now verifies data-driven SRI references correctly: it reads the configured
  SRI data source, decodes HTML entities, pairs hashes with the correct asset tags, and reports
  structured scan statistics instead of false `sri_checked=0` results (#171).
- `validate_front_matter` now computes aggregate counters before pagination, so `pages_checked` and
  `pages_passed` reflect the full scan instead of the current page size (#172).
- `export_agent_context` now uses the same source-markdown path as `build_agent_context`, removing
  theme chrome and HTML navigation artifacts from exported markdown (#173).
- `generate_featured_image` now returns structured, operator-actionable diagnostics when image
  generation is not configured or the output path is not writable, without changing the MCP tool
  contract (#174).
- The production deploy workflow now promotes refs without auto-creating a GitHub release, and the
  pre-release smoke gate runs from its own workflow instead of polluting push/PR checks with a
  skipped job state (#177, #178).

## [v1.3.3] - 2026-07-06

### Added
- `build_site` and `preview_build` now return a structured JSON error on Hugo failure containing
  `error`, `exit_code`, `command`, `working_directory`, `duration_ms`, `stderr_summary` (≤500 bytes,
  paths sanitised), `build_id` (`YYYYMMDD-HHMMSS-<4 hex chars>`), and `log_hint`. Full stderr is
  logged via `slog.Error` with the `build_id` key for log correlation (#160).
- `check_sri_versions` now returns a structured envelope `{files_scanned, sri_checked, summary,
  findings}` instead of a bare array. The `summary` field always contains a human-readable verdict
  ("No SRI attributes found", "All N passed", or "N/M passed, M mismatches"). **Breaking shape
  change:** existing code that destructures the flat `[]sriCheckEntry` array must be updated to
  access `.findings` (#162).
- `generate_featured_image` description in `tools/list` now appends
  `(not configured: set image_gen_url in config)` when `image_gen_url` is absent, so agents
  discover the configuration gap before calling. Operator guide documents `image_gen_url` and
  `image_gen_key` (#161).
- `get_page` accepts an optional `content_only=true` parameter that clears the `html` field
  (returns `html` as empty string) for lightweight metadata queries. Description now distinguishes
  `get_page` (rendered HTML) from `get_full_page_markdown` (raw Markdown, requires content.read)
  (#169).
- `frontMatterIssueDTO` (returned by `validate_front_matter` and `validate_site`) gains a `lang`
  field derived from the multilingual branch-bundle filename (`index.en.md` → `"en"`). `SourcePage`
  in the source index now carries a `Lang` field populated at index-build time (#168).

### Fixed
- `explain_site_structure` now uses `srcIdx.AllTags()` / `srcIdx.AllCategories()` when the source
  index is available, matching `get_site_health`. Previously reported 0 categories on sites where
  the HTML index carried no `article:section` meta tags (#163).
- `build_agent_context` now passes the raw public-index page to `computeRelated` (same pattern as
  `get_related_content`), preventing empty `related_pages` caused by source-merged tags not matching
  HTML-indexed sitemap entries (#164).
- `ContentClassifier` classifies `/404.html`, `/404/`, `/500.html`, `/500/` as `KindTechnical`,
  removing error pages from `get_feed` and `export_agent_context` output (#167).
- `get_broken_links` no longer reports false positives for `.md`-suffixed hrefs (LoveIt/PaperMod
  source-file links rendered as `<a href="./index.md">`) (#166).
- Smoke script `generate_featured_image` check now SKIPs instead of FAILing when the tool returns
  `config_error`, and the call now correctly includes the required `prompt` argument (#161).

### Changed
- **Breaking:** `validate_front_matter` and `validate_site` response `data` object field names
  renamed for clarity: `total` → `pages_checked`, `valid` → `pages_passed`. `invalid` unchanged.
  Update any agent prompts or custom tooling that references the old field names (#165).

## [v1.3.2] - 2026-07-06

### Fixed
- Rate limiter now only counts `tools/call` requests against the budget.
  Control-plane messages (`initialize`, `notifications/initialized`, `tools/list`,
  `resources/list`, etc.) pass through without consuming a token, so the
  configured rate limit reflects actual tool invocations rather than MCP
  handshake overhead (#156).
- When the rate limit fires inside an established MCP session
  (`Mcp-Session-Id` present), the server returns HTTP 200 with a JSON-RPC 2.0
  error body instead of HTTP 429. The go-sdk Streamable HTTP transport discards
  non-2xx response bodies before the MCP layer can surface the error; HTTP 200
  ensures the structured JSON-RPC error (`code: -32029`, `Retry-After`) reaches
  the MCP client (#155).
- `ContentClassifier` correctly classifies multilingual taxonomy slugs
  (`/en/tags/webhook/`, `/fr/categories/securite/`) via `stripLanguagePrefix`
  (added in v1.3.0); test coverage added in v1.3.1 confirms the fix. Closing
  #157 as resolved.
- `operator-guide.md`: new Pitfall 4 section documenting why OpenResty returns
  HTML 503 under rate-limit saturation and how to configure
  `proxy_intercept_errors` / `proxy_pass_header Retry-After` to forward the
  upstream JSON-RPC error body correctly (#158).
- `smoke-mcp-live.sh`: `generate_featured_image` is now called in the
  `MCP_SMOKE_ENABLE_WRITES=1` section (after `update_page`, while the page
  still exists); asserts `result.isError` via `classify_response` and verifies
  that `result.content[0].text` is non-empty (#159).

## [v1.3.1] - 2026-07-06

### Fixed
- Rate-limit 429 response body is now a valid JSON-RPC 2.0 error object
  (`code: -32029`, `message`, `data.retry_after_seconds`) so MCP clients can
  parse the structured error instead of seeing a generic "Error occurred during
  tool execution" (#153).
- Default rate limits raised to account for stateful Streamable HTTP transport
  consuming 2 HTTP requests per tool call: `site_admin_per_min` 10 → 60,
  `content_write_per_min` 30 → 60, `anonymous_per_min` 60 → 120,
  `content_read_per_min` 120 → 240 (#152, #140).
- `preview_build`, `create_page`, `update_page`, `delete_page` now use
  `TryLock`/`TryRLock` with a 10-second deadline instead of blocking
  indefinitely on `ContentMu`; lock events are logged via `slog` (#145).
- `get_related_content` resolves slugs through `PageResolver` instead of
  direct `idx.GetBySlug`, enabling correct multilingual branch-bundle lookup
  (#146).
- `matchContentFilters` in `search_content` no longer rebuilds
  `ContentClassifier` per page (O(n²) → O(n)) (#141).
- `isGitPathMissing` in `diff_page` now checks `exec.ExitError.ExitCode()==128`
  instead of locale-dependent English substring matching (#142).
- `get_sitemap` accepts `limit` (default/cap 200) and `offset`; returns an
  empty list when offset ≥ total instead of panicking (#147).
- Rate limiter bucket map now evicts idle entries (TTL 15 minutes, GC every
  5 minutes) and caps at 10,000 entries to prevent unbounded memory growth
  under sustained load from many distinct IPs (#150).
- `deploy.sh` no longer overwrites an existing `mcp-hugo-server-go.service`
  on upgrades — the distribution template carries no site-specific paths;
  a `service.d/override.conf` example is installed on first deploy and
  preserved on upgrades (#143).
- `--version` / `-version` / `version` flag prints the build version and
  exits without requiring the config file to be loaded (#148).
- Operator guide documents `ProtectSystem=strict`, the `ReadWritePaths`
  requirement, and the systemd drop-in override pattern (#149).
- `docs/client-compatibility.md` and `auth.md` document that
  `oauth.enabled: true` requires a Bearer token on all `/mcp` requests,
  including anonymous-scope tools (#154).
- `docs/client-compatibility.md` updated to v1.3.0 test results: Claude.ai
  admin token and stateful HTTP transport confirmed functional (#151).

### Added
- `smoke-agent-interop.sh` extended with `mcp_tool_call` helper (handles
  202+session-id two-phase flow) and live assertions for
  `get_site_information`, `get_recent_posts`, and optionally `get_site_health`
  (#144).

## [v1.3.0] - 2026-07-05

### Added
- `ContentClassifier` centralises Hugo page-kind detection (article, section, taxonomy, pagination, technical) replacing scattered `/posts/` prefix checks. Fixes `list_pages`, `get_feed`, `get_recent_posts`, `explain_site_structure`, and `get_broken_links` returning taxonomy and section pages as content (#127, #132, #133).
- `PageResolver` unifies public and source-index slug resolution. `diff_page`, `get_full_page_markdown`, `build_agent_context`, and `get_page` now look up pages through one code path: public HTML index for published metadata, SourceIndex for raw Markdown body (#130, #134, #137).

### Fixed
- Switch MCP transport from stateless to stateful mode. In stateless mode the server returned HTTP 405 for `GET /mcp`, causing Claude.ai and ChatGPT to immediately disconnect after tools discovery (tools briefly visible, then "not connected"). Stateful mode keeps the SSE session open so tool calls succeed. Sessions have a one-hour idle timeout for cleanup.
- `diff_page` and source-index lookup now correctly resolve multilingual branch-bundle slugs (`index.en.md`, `index.fr.md`) to the parent directory slug (`posts/slug`), matching how the public site index exposes those pages.
- `build_site` and `preview_build` now run Hugo from `hugo_root` (the Hugo project directory containing `hugo.toml`) instead of `site_root` (the generated `public/` output directory). Fixes `build_error: hugo exited with error` on every call (#135).
- `list_categories` and `list_tags` now return frontmatter taxonomies from the source index instead of the HTML `article:section` meta fallback, which was reporting "posts" as a category on sites without `article:category` meta tags (#136).
- `diff_page` returns `status: "git_not_available"` with raw source content instead of a hard error when the content directory is not inside a Git repository (#131).
- `get_broken_links` no longer reports false positives for pagination URLs (`/page/2/`), taxonomy term pages (`/tags/go/`), anchor-only links, `mailto:`, `tel:`, and non-HTTP scheme URIs (#139).
- `export_agent_context` now filters through `ContentPages()` (excluding taxonomy and section pages) and reads Markdown from the source index when available, consistent with `get_full_page_markdown`.
- Rate-limit `Retry-After` header and `retry_after_seconds` response field now reflect the actual token-bucket delay instead of a hardcoded 1-second value. For `site.admin` (10 req/min) the correct delay is 6 seconds (#140).
- Fixed a data race on the internal `ContentClassifier` pointer: `contentClassifier` is now initialised eagerly at index build time instead of lazily on first use, eliminating a concurrent-write hazard in the HTTP request goroutines.

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
