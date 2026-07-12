# Changelog

All notable changes to this project are documented here.

## [Unreleased]

### Added
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
- `list_pages` description: clarifies it returns content pages only (not taxonomy list pages) and
  cross-references `get_sitemap` for the full URL inventory.
- `search_pages` description: cross-references `search_content` for filtered/paginated search.
- `get_sitemap` description: clarifies it includes taxonomy pages by default; cross-references
  `list_pages` for content-only browsing.
- `search_content` description: cross-references `search_pages` for unauthenticated keyword search.
- `validate_site` description: notes equivalence to `validate_front_matter` with no slug filter.

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
