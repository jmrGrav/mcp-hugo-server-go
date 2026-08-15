# Operator Guide: mcp-hugo-server-go

This document describes how to deploy, configure, and operate the Hugo MCP server.

## Environment Configuration

The server reads its configuration from the path specified by the `MCP_HUGO_SERVER_CONFIG` environment variable.

```bash
export MCP_HUGO_SERVER_CONFIG=/etc/mcp-hugo-server-go/config.yaml
```

If the environment variable is not set or points to an empty path, the server uses built-in defaults.

### MCP_HUGO_*-namespaced env vars (stdio/MCPB installs without a config file)

For a local single-user `transport: stdio` install (e.g. an MCPB desktop extension) there is
typically no `config.yaml` at all — the deployer's `user_config` values arrive as environment
variables instead. When these fields are still empty after loading `MCP_HUGO_SERVER_CONFIG`
(or when that variable is unset entirely), the server falls back to:

| Variable | Fills |
|----------|-------|
| `MCP_HUGO_SITE_ROOT` | `site_root` |
| `MCP_HUGO_HUGO_ROOT` | `hugo_root` |
| `MCP_HUGO_CONTENT_ROOT` | `content_root` |
| `MCP_HUGO_SITE_URL` | `site_url` |
| `MCP_HUGO_SITE_NAME` | `site_name` |

A value already set in `config.yaml` always wins — these variables only fill gaps, they never
override a file. This is why a normal HTTP deployment (which sets all of these explicitly in
its `config.yaml`) is unaffected by this mechanism.

## Configuration Fields

Configuration is stored in YAML format. The following table lists all available fields, their types, defaults, and purposes.

### Core Site Settings

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `site_root` | string | (required) | Absolute path to the Hugo **build output** directory (Hugo's `publishDir`, normally `{hugo_root}/public`) — this is what gets indexed for content-page discovery/link graphs. It must **not** point at the Hugo project root: any vendored theme's raw `.html` layout templates under a project root's `themes/` directory get walked and mis-parsed as content pages (see the warning below). |
| `hugo_root` | string | (required) | Absolute path to the Hugo project root (contains `content/`, `themes/`, `hugo.toml`/`hugo.yaml`, etc.). |
| `content_root` | string | (required) | Absolute path to Hugo content directory (where `.md` files live). |
| `site_url` | string | (required) | Public URL of the Hugo site (e.g., `https://www.arleo.eu`). |
| `site_name` | string | (required) | Display name of the site. |
| `language_default` | string | `en` | Default language code for content. |

### Server Transport

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `transport` | string | `http` | Communication protocol: `http` (HTTP server, used for this self-hosted/systemd/nginx deployment model) or `stdio` (standard input/output, used for local single-user desktop installs — see the MCPB packaging docs). |
| `http_bind_addr` | string | `127.0.0.1` | IP address to bind the HTTP server to (used if `transport: http`). |
| `http_bind_port` | int | `8088` | TCP port for the HTTP server. |
| `streaming_enabled` | boolean | `true` | Enable streaming responses for large result sets. |

### Index and Request Limits

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `max_index_entries` | int | `5000` | Maximum number of pages to index from the site. |
| `max_result_items` | int | `50` | Maximum items to return in a single response. |
| `max_request_bytes` | int | `1048576` (1 MiB) | Maximum request body size in bytes. |

### Path Protection

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `reject_symlinks` | boolean | `true` | Reject requests for symlinked content (security). |
| `reject_hidden_paths` | boolean | `true` | Reject requests for paths starting with `.` |

### Image Generation

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `image_gen_url` | string | (empty) | External API URL for AI-powered image generation. Omit if not used. |
| `image_gen_key` | string | (empty) | API key for the image generation service. |

### Featured image generation

`generate_hero_image` is always registered. It renders a 1200×675 JPEG using a local
Go renderer: an Unsplash photo background selected deterministically by title hash,
composited with a dark gradient overlay, accent bar, title, and tag chips. No external
service is required by default.

The generated image is saved to **`{hugo_root}/static/images/{slug}-featured.jpg`**.
Because `static/` is served directly by Hugo, the file is available immediately after the
next build without a separate copy step.

Background photos are read from `{hugo_root}/static/images/featured-backgrounds/` (six
1200×675 JPEGs bundled with the repository). If that directory is empty or missing the
renderer falls back to a solid gradient.

**External API mode** (optional): when both `image_gen_url` and a `prompt` argument are
provided, the tool POSTs the prompt to that URL and saves the returned `image/*` body
instead of running the local renderer.

| Config key | Description |
|------------|-------------|
| `image_gen_url` | POST endpoint that accepts a plain-text prompt body and returns an `image/*` response |
| `image_gen_key` | Optional Bearer token sent in the `Authorization` header |

The generated image is saved to `{hugo_root}/static/images/{slug}-featured.jpg`. If the
tool returns `write_error`, verify:

- Unix ownership/mode on `{hugo_root}/static/images` allows writes by the MCP service user.
- systemd `ReadWritePaths` includes `{hugo_root}/static/images` (see Pitfall below).

### Build Configuration

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `build_timeout_seconds` | int | `120` | Maximum time (in seconds) to wait for Hugo build to complete. |
| `post_build_hooks` | array of strings | (empty) | URLs to POST a `{"event":"post_build"}` webhook to after successful site build. Only HTTPS endpoints and public DNS hostnames are allowed (SSRF protected); redirects are not followed and response bodies are bounded. |
| `preview_external_verification` | bool | `false` | When enabled, `create_preview` verifies its signed entry redirect, cookie-backed nested HTML route, one asset, and strict missing-route 404 through `oauth.issuer` before returning success. Served bytes must match the isolated build; a homepage fallback is rejected as `preview_unreachable` and the failed preview is revoked. |

### Operational SQLite Persistence

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `db_path` | string | (empty — disabled) | Absolute path to the server's *operational* SQLite database (derived search/link index, publication manifests, mutation/idempotency journal, restart-safe recovery journal, multilingual content shadow, preview lease persistence). This is a **different database from `oauth.storage_path`** (OAuth tokens) — the two are deliberately kept separate so revoking/rotating OAuth state never touches content-operations state or vice versa. Leaving `db_path` empty does not break anything: every feature it backs degrades to its pre-persistence, process-memory-only behavior instead of failing, so this is easy to forget to set on a fresh install or an in-place upgrade. See the pitfall below. |

> **Pitfall — `db_path` unset silently disables an entire release's worth of restart-safety work.** `db_path` was introduced across v1.8.6 (#1074) to make build/publication state, the mutation idempotency journal, crash recovery, multilingual identity, and preview leases survive a service restart. None of that requires `db_path` to function at all when it's absent — every one of those subsystems has an explicit, deliberate in-memory fallback — so a server without it configured runs correctly and shows no errors; it just silently gets none of the new restart-safety guarantees, and `get_runtime_status`'s `content_index_shadow`/`mutation_journal`/`build_reconciliation` fields are simply absent from the response instead of erroring. On the v1.8.6 production deploy, `db_path` was not part of the existing config (only `oauth.storage_backend: sqlite` was set, for the *different*, OAuth-only database), so the entire persistence programme silently ran degraded from the moment the release went live until this was noticed and fixed. **After any fresh install or upgrade that introduces new SQLite-backed features, confirm `db_path` is set and call `get_runtime_status` to check that the fields the new release advertises actually appear in the response** — do not assume a clean restart with no errors means a config option took effect.
>
> Fix: add `db_path` to `config.yaml` pointing at a file inside a directory the service unit already has write access to (reuse the same directory as `oauth.storage_path` if one is already allowlisted in `ReadWritePaths` — no systemd changes needed in that case), then restart:
> ```yaml
> db_path: /var/lib/mcp-hugo-server-go/site.db
> ```
> ```bash
> sudo systemctl restart mcp-hugo-server-go
> ```
> If `/var/lib/mcp-hugo-server-go` (or wherever you point `db_path`) is not yet in the service unit's `ReadWritePaths`, see Pitfall 1 below — the same "unable to open database file" failure mode applies to this database too.

### Managed Hugo Upgrade Configuration

Managed Hugo upgrades are disabled by default. `get_hugo_update` can report the
installed version without network access and only queries the official release
API when `check_latest:true` is explicit. Staging, activation, and rollback all
require `write` plus `hugo_upgrade.enabled:true`.

| Field | Default | Purpose |
|-------|---------|---------|
| `hugo_upgrade.enabled` | `false` | Enables staging and symlink activation. Status remains available when disabled. |
| `hugo_upgrade.managed_dir` | empty | Private root containing versioned binaries and verification records. Must be absolute and real, without symlink components. |
| `hugo_upgrade.binary_link` | empty | Symlink switched atomically by activation. Must be strictly inside `managed_dir`. |
| `hugo_upgrade.release_api_base_url` | official Hugo GitHub API | Release metadata source. Configuration loading requires HTTPS and an allowlisted host. |
| `hugo_upgrade.allowed_hosts` | official GitHub API/download hosts | Exact host allowlist applied to initial requests and every redirect. |
| `hugo_upgrade.max_download_bytes` | 128 MiB | Hard response bound for the release archive. |
| `hugo_upgrade.cache_ttl_seconds` | 3600 | Latest-release metadata cache lifetime. |
| `hugo_upgrade.require_extended` | `true` | Select and verify the official extended archive. |
| `hugo_upgrade.allow_downgrade` | `false` | Allows staging a version older than the installed version only when explicitly enabled. |
| `hugo_upgrade.minimum_version` / `maximum_version` | empty | Optional inclusive stable-version policy bounds, written as exact `vMAJOR.MINOR.PATCH` values. |

The initial managed installer supports official Linux `amd64` and `arm64`
`.tar.gz` releases. macOS releases use signed `.pkg` installers and are
intentionally rejected rather than unpacked incorrectly. A real stage verifies
the official SHA-256 manifest before extracting only the `hugo` executable,
then executes only `hugo version` against that staged path.

Activation never replaces `/usr/bin/hugo`, `/usr/local/bin/hugo`, or another
package-manager path. It only atomically changes `binary_link`, records the
previous managed target, and returns an explicit supervisor restart action.
Ensure the link's directory is on the service `PATH` and add `managed_dir` to
the unit's `ReadWritePaths`; the MCP never edits systemd or restarts itself.

On a deployment that has never activated a managed Hugo version, `rollback_hugo`
has nothing to restore on the very first real upgrade — the pre-existing
unmanaged binary was never itself a managed version, so that first
activation's record has no `previous_target`. Run `bootstrap_hugo` once,
before your first real upgrade, to close that gap:

```text
bootstrap_hugo(dry_run=false)   # re-downloads/verifies the currently-installed
                                 # version and activates it as the baseline
# operator restarts and confirms get_hugo_update still reports the same version
```

Recommended flow for every upgrade after bootstrapping:

```text
get_hugo_update(check_latest=true)
stage_hugo_upgrade(target_version="vX.Y.Z")
stage_hugo_upgrade(target_version="vX.Y.Z", dry_run=false)
activate_hugo(target_version="vX.Y.Z")
activate_hugo(target_version="vX.Y.Z", dry_run=false)
# operator restarts and validates the service
rollback_hugo(dry_run=false) # only if rollback is required
```

### Idempotency Configuration

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `idempotency_ttl_seconds` | int | `900` (15 minutes) | Retention window for the idempotency-key store backing `create_page`/`update_page`/`delete_page`/`upload_page_asset`/`delete_page_asset` and the `get_mutation_status` lookup (#586). A longer window gives an agent more time to positively confirm via `get_mutation_status` whether a mutation landed after a connector-level outage, instead of falling back to a blind (if always-safe) retry (#616). Deliberately a deployment-level setting only — never a per-call tool parameter, since a caller-supplied TTL could otherwise be used to shorten the window and evade duplicate-submission protection. A non-positive value (`0` or negative) is treated as a misconfiguration and clamped back to the 900-second default rather than silently disabling replay protection. |
| `force_dry_run_all` | bool | `false` | When `true`, overrides every mutation tool's per-call `dry_run` argument to `true` server-wide — `create_page`, `update_page`, `delete_page`, `upload_page_asset`, `delete_page_asset`, `apply_content_plan`, and `rollback_change` all become read-only previews regardless of what a caller passes (#611). Intended for safely exercising the full write-tool surface during a live audit or CI smoke run, without touching rate-limit quota (dry-run calls already don't consume it). Deliberately a single server-wide flag, not a per-caller/per-session mechanism — set it before a planned audit/CI run and unset it afterward. Each affected tool's response still reports `data.dry_run: true` as normal, so the override is directly visible to the caller. |
| `stale_test_content_threshold_hours` | int | `0` (disabled) | Age (in hours) past which a still-published page whose slug matches the reserved test-content prefix convention (`mcp-audit-`/`test-audit-`/`codex-`, #584) triggers a post-build advisory on every `build_site`/`publish_changes` (#608) — surfaced both in server logs and in that call's own `data.warning`, so a forgotten test page doesn't require an operator to think to call `validate_frontmatter`/`validate_site` themselves. Report-only: it never deletes or modifies anything. Off by default; set a positive value (e.g. `24`) to opt in. Independent of this setting, a page created via `create_page`'s own opt-in `test_content` parameter (#661) is always checked against its own `test_content_expires_at` — that per-page TTL keeps working even when this server-wide setting stays `0`. |

### Git Baseline Configuration

The `git_baseline` section defines the **local Git checkout** used as the
trusted baseline for `diff_page`, future runtime Git diagnostics, and later
publication verification.

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `git_baseline.mode` | string | `auto` | Baseline resolution mode: `auto`, `configured`, or `disabled`. |
| `git_baseline.repo_path` | string | (empty) | Absolute path to the local Git checkout when `mode: configured`. |
| `git_baseline.branch` | string | `main` | Expected branch name for diagnostics. |
| `git_baseline.remote` | string | `origin` | Expected remote name for diagnostics. |

Semantics:

- `auto`: current/runtime Git consumers may auto-detect a repository from
  `content_root`.
- `configured`: the server should use the explicit local checkout at
  `repo_path`.
- `disabled`: Git-backed diff/runtime features should degrade explicitly rather
  than probing the host.

See [docs/git-baseline-model.md](git-baseline-model.md) for the trust model,
state vocabulary, and non-goals.

### Rate Limiting

The `rate_limit` section controls per-scope logical MCP `tools/call` rates
(per minute). Streamable HTTP session-control traffic such as `initialize`,
`notifications/initialized`, and `tools/list` is not counted against the tool
call budget.

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `rate_limit.anonymous_per_min` | int | `120` | Logical tool calls per minute for anonymous (no-auth) scope. |
| `rate_limit.content_read_per_min` | int | `240` | Logical tool calls per minute for the `read` scope (config key name predates #450's scope rename). |
| `rate_limit.content_write_per_min` | int | `60` | Legacy key, effectively unreachable since #450: `write` (folding the old `content.write`/`site.admin` split into one scope) is rate-limited via `rate_limit.site_admin_per_min` instead. Kept only so a config carrying both old keys doesn't error. |
| `rate_limit.site_admin_per_min` | int | `60` | Logical tool calls per minute for site-operation calls under the `write` scope (config key name predates #450's scope rename). |
| `rate_limit.destructive_per_min` | int | `5` | Requests per minute for destructive operations. |

### OAuth Configuration

The `oauth` section configures OAuth 2.0 authentication (optional):

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `oauth.enabled` | boolean | `false` | Enable OAuth 2.0 server. When false, all other OAuth fields are ignored. |
| `oauth.issuer` | string | (required if enabled) | OAuth issuer URL (e.g., `https://mcp.arleo.eu`). |
| `oauth.resource` | string | (empty) | Resource URI for scopes. |
| `oauth.dynamic_client_registration` | boolean | `false` | Allow dynamic client registration (RFC 7591). |
| `oauth.client_registry_path` | string | (empty) | Optional host-local YAML file with preconfigured confidential clients and canonical scopes. |
| `oauth.require_pkce` | boolean | `false` | Require PKCE for authorization code flow. |
| `oauth.trusted_authorize_cidrs` | array of strings | (empty) | CIDR blocks allowed to call `/authorize` without authentication. |
| `oauth.auth_code_ttl_seconds` | int | (default) | Lifetime of authorization codes. |
| `oauth.access_token_ttl_seconds` | int | (default) | Lifetime of access tokens. |
| `oauth.refresh_token_ttl_seconds` | int | (default) | Lifetime of refresh tokens used for silent token renewal. |
| `oauth.storage_backend` | string | `memory` | Token persistence backend: `memory` (ephemeral), `json` (file-based), or `sqlite` (database). |
| `oauth.storage_path` | string | (empty) | Path to token storage file (required for `json` or `sqlite` backends). |

Access tokens persisted before principal-aware quota identity was introduced may
have an empty `principal`. Those legacy bearers now fail closed at `/mcp`
authentication; the client must use its refresh token or complete OAuth again
to obtain a bearer carrying its stable client principal. Refresh-token renewal
is the supported migration path and does not require deleting the refresh token.

## Tool Access Scopes

Since #450 (and extended by #1039/#1050), the server enforces exactly three canonical runtime scopes:

- `read`: full visibility, including drafts and other
  source-only/pre-publication content (an explicit operator
  risk-acceptance decision — see `docs/mcp-contract.md` §6.12). On
  OAuth-enabled deployments, callers obtain this through self-serve OAuth
  registration; `/mcp` still requires a Bearer token even for anonymous-tier
  tools.
- `write`: `read` plus mutations and site operations. Requires a registered
  OAuth client. `write` implies `read`.
- `admin`: `write` plus the four managed Hugo binary lifecycle tools
  (`stage_hugo_upgrade`, `activate_hugo`, `rollback_hugo`, and
  `bootstrap_hugo`). Requires an explicitly approved administrator token.
  Legacy `site.admin`/`system.admin` aliases continue to resolve to `admin`
  so already-issued administrator tokens retain their capability.

`revoke_all_previews` is also intentionally write-scoped. It is a bulk action
only over previews owned by the current caller (`RevokeAllOwned`), not a
cross-tenant administrative operation.

Some docs and discovery metadata use the descriptive profile labels
`reader`, `operator`, and `administrator`. Treat those as human-facing names
for the three runtime scopes above, not as separate ACL layers.

Legacy clients may still send any pre-#450 scope string (`mcp`, `reader`,
`content.read`, `content.write`, `site.admin`, `system.admin`, and other
aliases — see `docs/mcp-contract.md` §6.12 for the full table). They are
accepted as deprecated compatibility aliases resolved to `read`/`write`/`admin`
(`site.admin`/`system.admin` and their casing/underscore variants resolve to
`admin`, not `write`), but only `read`/`write`/`admin` are advertised as
canonical scopes and should be used by new clients.

Published discovery metadata now carries both:

- canonical runtime scope strings (`read`, `write`, `admin`) in `scopes_supported`
- additive `access_profiles.reader` / `access_profiles.operator` / `access_profiles.administrator` metadata as descriptive profile labels over that same three-scope model

To enable confidential OAuth clients for `write`, set `oauth.client_registry_path` to a root-readable YAML file on the host. Each entry may use either the legacy `client_id` / `client_secret` / `scope` fields or the canonical `id` / `secret` / `scopes` fields. Redirect URIs may be exact values or strict HTTPS path-prefix patterns such as `https://chatgpt.com/connector/oauth/*`. The loader upserts client records into the SQLite store when available; it never logs secrets and never deletes absent clients automatically.

`/mcp` bearer verification itself now goes through the Go MCP SDK's
`auth.RequireBearerToken` middleware, but via a local compatibility adapter
rather than a raw drop-in swap. That adapter deliberately preserves the
existing `WWW-Authenticate` challenge shape (`realm`, `resource_metadata`,
`error="invalid_token"`), because ChatGPT, Claude, Le Chat, and the external
scanner workflows were already validated against that exact on-wire behavior.
Per-tool ACL decisions still happen in this server after bearer verification,
because the SDK middleware authenticates the request but does not know this
project's JSON-RPC tool-scope model.

The server exposes a migration metric at `/metrics`:

- `mcp_legacy_scope_requests_total{scope="mcp"}` tracks legacy alias usage so the alias can be removed only after production usage reaches zero.

The authoritative tool inventory is documented in [docs/tools.md](tools.md) and should be treated as the source of truth for names, titles, and scope mapping.

## Deployment

### Prerequisites

- Go 1.22+ (if building from source)
- Hugo (any recent version, used at runtime for site builds)
- Systemd (for service management)

### Build and Deploy

To build and deploy the server:

```bash
bash deploy/deploy.sh
```

This script:
1. Builds the binary for Linux x86_64: `GOOS=linux GOARCH=amd64 go build -o mcp-hugo-server-go ./cmd/mcp-hugo-server-go/`
2. Uploads the binary to the remote machine (`hugo-vm` by default).
3. Installs the binary to `/usr/local/bin/mcp-hugo-server-go`.
4. Uploads and installs the systemd service file to `/etc/systemd/system/mcp-hugo-server-go.service`.
5. Reloads systemd and enables the service with `systemctl enable --now`.

### Production Deploy + Release (GitHub Actions)

Production deploys and releases both go through `.github/workflows/deploy.yml`
(`workflow_dispatch`) — a single call, with `release_version` set, builds,
deploys, tags, and publishes the GitHub release together:

```bash
gh workflow run deploy.yml -f ref=main -f release_version=v1.5.5 -f dry_run=false
# ...approve the production environment gate when prompted...
```

Always pass `release_version` matching the version you're about to tag; the
workflow's `validate` job gates on `CHANGELOG.md`/`npm/package.json`/
`manifest.json` already carrying that version before building anything, so
merge the changelog/version-bump PR first. The tag itself is created only
after the `deploy` job succeeds — so at build time the tag doesn't exist yet
and `meta.release_version` can't be derived from `git describe`. The explicit
`release_version` input lets the deployed build report its intended release
identity immediately, instead of waiting for the tag to exist: it sets
`meta.release_version` to the given value and `meta.build_channel` to
`"release"` (see `docs/mcp-contract.md` §5 for this field's naming history —
it was briefly called `server_version` between v1.5.7 and v1.5.8, #560/#563).
Omit it for an untagged mainline deploy (a hotfix ahead of the next release,
for example); the server then reports `meta.release_version = "main-<sha>"`
and `meta.build_channel = "main"`, and the `release` job is skipped entirely.

### Systemd Hardening and Override

The service runs under `ProtectSystem=strict`, which makes the entire filesystem
read-only for the process. You must declare any directory the server needs to
write to via `ReadWritePaths` in the systemd drop-in override. For Hugo admin
tools, that means more than `content/`: builds and generated images also need
`resources/` and `public/`.

The deploy script installs a template at:

    /etc/systemd/system/mcp-hugo-server-go.service.d/override.conf

Edit it after the first deploy to match your installation. At minimum you need:

```ini
[Service]
ReadOnlyPaths=/etc/mcp-hugo-server-go
ReadWritePaths=/var/lib/mcp-hugo-server-go /path/to/hugo-site/content /path/to/hugo-site/resources /path/to/hugo-site
Environment=PATH=/usr/local/bin:/usr/bin:/bin
```

After editing, reload systemd:

```bash
sudo systemctl daemon-reload && sudo systemctl restart mcp-hugo-server-go
```

The drop-in override survives subsequent `deploy.sh` runs (the script never
overwrites an existing override.conf).

Edit the `REMOTE` variable in `deploy/deploy.sh` to target a different host.

### Build Permissions

The `build_site` and `preview_build` tools run Hugo as the MCP service user.
Before invoking Hugo, the server performs a preflight write-check on the directories
it needs. If the check fails you will receive a `build_precondition_failed` error
with an `operator_hint` field explaining exactly what to add.

Required writable paths for each tool:

| Tool | Paths that must be writable |
|------|----------------------------|
| `build_site` | **the parent directory of `site_root`** (not `site_root` itself), `{hugo_root}/resources` |
| `preview_build` | `{hugo_root}/resources` (render-to-memory; no writes to `public/`) |
| `generate_hero_image` | `{hugo_root}/static/images` |

`build_site` builds atomically (#965): Hugo renders into a temporary
directory created as a *sibling* of `site_root` (`.mcp-build-output-*`), then
swaps it into place via `rename`, keeping the previous output as
`.mcp-public-backup-*` until cleanup. Both the create and the rename are
directory-entry operations on `site_root`'s **parent**, not on `site_root`
itself — under `ProtectSystem=strict`, listing `site_root` (e.g. `.../public`)
alone in `ReadWritePaths` is not sufficient; you must list its parent (e.g.
`.../hugo-site`, not `.../hugo-site/public`). This bit a production deploy
once (#981/#983) — the preflight check historically probed `site_root` only,
which passed while the actual rename against the unlisted parent failed with
`permission_denied`.

Add the missing paths to `ReadWritePaths` in the systemd override and reload:

```bash
sudo systemctl daemon-reload && sudo systemctl restart mcp-hugo-server-go
```

Do **not** add a directory to `ReadOnlyPaths` if it already appears in
`ReadWritePaths` — `ReadOnlyPaths` takes precedence and will silently undo the
write permission.

### Git Baseline Permissions

The Git baseline checkout used for `diff_page` and future runtime/publication
diagnostics is **read-only** in the current design.

The MCP service user must be able to read:

- the checkout directory configured by `git_baseline.repo_path` (or the
  auto-detected repository in `auto` mode);
- the `.git` directory or worktree metadata needed by `git -C <repo> ...`;
- the tracked source files used for diff inspection.

Do **not** add the Git baseline checkout to `ReadWritePaths` for this design.
If Git metadata is inaccessible, later runtime surfaces should report a degraded
state rather than broadening filesystem permissions silently.

### Known Pitfalls

#### `generate_hero_image` returns `write_error` after first deploy

The service unit's `ReadWritePaths` list usually covers `content/`, `resources/`, and
`public/`, but **`static/images/` is a separate tree that must be declared explicitly**.
The tool saves generated images there; without the entry, `ProtectSystem=strict` makes
the path read-only and every call fails.

Create a drop-in override for the unit:

```bash
sudo mkdir -p /etc/systemd/system/mcp-hugo-server-go.service.d/
sudo tee /etc/systemd/system/mcp-hugo-server-go.service.d/readwrite-static-images.conf <<'EOF'
[Service]
ReadWritePaths=/path/to/hugo-site/static/images
EOF
sudo systemctl daemon-reload && sudo systemctl restart mcp-hugo-server-go
```

Also verify that `{hugo_root}/static/images` is writable at the Unix level by the
service user (mode `0775` with the service user in the owning group, or `0755` with
the service user as owner).

Note: `site_root` (the build output, `{hugo_root}/public`) is always nested under
`hugo_root`, so a `ReadWritePaths` entry covering `hugo_root` already covers `site_root`
too — but `static/images` under `hugo_root` is a distinct path outside `site_root` that
needs its own entry regardless.

#### `build_site` succeeds but the site 403s afterward

`build_site`'s atomic swap (#965) builds into a temp directory created with
`os.MkdirTemp`, which defaults to mode `0700` — owner-only. Since v1.8.2
(#984), the server chmods that directory to `0755` immediately after
creating it, and self-heals any world-unreadable file or directory left
inside the swapped-in output before returning. If `build_site`'s response
ever includes an `output_unreadable: ...` warning, it names the exact paths
the server could not fix itself (its own chmod failed) — usually an
ownership mismatch, fixable with the `chown` command the warning suggests.
Prior to #984, this bit a production deploy: `build_site` reported success
while `site_root` ended up `0700`, and a reverse proxy running as a
different Unix user got `403` on every page until manually `chmod`'d.

#### `get_broken_links` (and other index tools) reported stale results after `build_site` — fixed (#212)

The site index is built once at startup by walking the public HTML directory.
`build_site` now reloads the public and source indexes automatically as one of
its post-build callbacks (`index_reload`), so `get_broken_links`,
`search_content`, and page-count results reflect the just-completed build
without any operator action. A manual restart is no longer required for this.

If `get_runtime_status`/`get_site_health` ever disagree with a fresh Hugo
rebuild after a restart or an out-of-band source edit, see the restart-safe
persistence layer added in v1.8.6 (#1074) — `data.build_reconciliation` and
`data.content_index_shadow` in `get_runtime_status` report exact filesystem
fingerprint drift, which is now the authoritative signal rather than
process-local `BuildPending` bookkeeping.

### Configuration File

Place the configuration file at the path referenced by `MCP_HUGO_SERVER_CONFIG`:

```yaml
site_root: /srv/hugo-site/public
hugo_root: /srv/hugo-site
content_root: /srv/hugo-site/content
site_url: https://www.arleo.eu
site_name: Arleo
language_default: en
transport: http
http_bind_addr: 127.0.0.1
http_bind_port: 8088
streaming_enabled: true
max_index_entries: 5000
max_result_items: 50
max_request_bytes: 1048576
reject_symlinks: true
reject_hidden_paths: true
image_gen_url: https://api.example.com/generate-image
image_gen_key: your-api-key
build_timeout_seconds: 120
preview_external_verification: false # enable when /preview/ is publicly routed to this service
post_build_hooks:
  - https://example.com/webhook/post-build
idempotency_ttl_seconds: 900 # 15 minutes; raise for longer outage-recovery windows (#616)
force_dry_run_all: false # set true before a live audit/CI smoke run to make every mutation tool read-only (#611)
stale_test_content_threshold_hours: 0 # set e.g. 24 to opt into a post-build advisory for forgotten test/audit pages (#608)
# Taxonomy alias map: maps non-canonical slugs to canonical ones.
# Keys and values are slugified on load (casing/whitespace-insensitive).
# Effect: list_tags, list_categories, and page DTOs return the canonical form.
# Agents filtering by canonical tag also match pages tagged with the alias form.
taxonomy_aliases:
  sécurité: security
  postmortem: post-mortems
rate_limit:
  anonymous_per_min: 60
  content_read_per_min: 120
  content_write_per_min: 30
  site_admin_per_min: 10
  destructive_per_min: 5
oauth:
  enabled: false
  issuer: https://mcp.arleo.eu
  resource: ""
  dynamic_client_registration: false
  require_pkce: false
  trusted_authorize_cidrs: []
  auth_code_ttl_seconds: 600
  access_token_ttl_seconds: 3600
  refresh_token_ttl_seconds: 2592000
  storage_backend: memory
  storage_path: ""
```

### Service File

The systemd service is installed to `/etc/systemd/system/mcp-hugo-server-go.service`. Key settings:

- **User/Group**: `mcp-hugo-server-go` (create this user before running).
- **Environment**: `MCP_HUGO_SERVER_CONFIG=/etc/mcp-hugo-server-go/config.yaml`.
- **Security**: `ProtectSystem=strict`, `ProtectHome=read-only`, `CapabilityBoundingSet=` (no capabilities).
- **Write Paths**: `ReadWritePaths=/var/lib/mcp-hugo-server-go /srv/hugo-site/content /srv/hugo-site/resources /srv/hugo-site/public` (adjust to match your Hugo tree and OAuth storage path).

To run in read-only mode (anonymous and `read` only):
1. Remove the `ReadWritePaths` lines.
2. Change `ProtectSystem=full` to `ProtectSystem=strict`.
3. Reload and restart: `sudo systemctl daemon-reload && sudo systemctl restart mcp-hugo-server-go`.

## Adding Post-Build Hooks

Post-build hooks allow you to trigger external systems after a successful Hugo build (e.g., cache invalidation, notification services).

1. **Edit the configuration file** and add a URL to the `post_build_hooks` array:

```yaml
post_build_hooks:
  - https://cdn.example.com/purge-cache
  - https://notify.example.com/deploy
```

2. **Validate the URLs**:
   - Only `http://` and `https://` schemes are allowed.
   - Private/link-local IP addresses are rejected (SSRF protection).
   - Hostnames must resolve to public IP addresses at load time.

3. **Reload the service**:

```bash
sudo systemctl reload mcp-hugo-server-go
```

4. **Trigger a build** to test:

```bash
# Call build_site (requires write scope)
mcp-hugo-server-go <options>  # invoke build_site tool
```

After a successful build, the server POSTs `{"event":"post_build"}` to each URL with a 10-second timeout. Responses and errors are returned to the caller.

## Enabling and Disabling OAuth

### Enable OAuth

To enable OAuth 2.0 authentication:

1. **Edit the configuration file** and set `oauth.enabled: true`:

```yaml
oauth:
  enabled: true
  issuer: https://mcp.arleo.eu
  resource: ""
  dynamic_client_registration: false
  require_pkce: false
```

2. **Set the issuer URL** to match your deployment (used for discovery and token validation).

Discovery surfaces:

- `/.well-known/mcp/server-card.json` is the canonical MCP Server Card endpoint.
- `/.well-known/mcp.json` is retained as a compatibility alias.
- `/.well-known/oauth-protected-resource/mcp` is retained as a compatibility alias for resource-specific discovery.
- Both return the same public discovery document.

3. **Choose a storage backend** for access tokens:

   - **`memory`** (default): Tokens are ephemeral and lost on restart. Good for testing.
   - **`json`**: Tokens are persisted to a JSON file. Requires `storage_path` to be set.
   - **`sqlite`**: Tokens are persisted to a SQLite database. Requires `storage_path` to be set.

   For production, use `json` or `sqlite`:

```yaml
oauth:
  enabled: true
  storage_backend: sqlite
  storage_path: /var/lib/mcp-hugo-server-go/tokens.db
```

4. **Update the systemd service** to allow write access to the storage path:

```ini
ReadWritePaths=/var/lib/mcp-hugo-server-go /srv/hugo-site
```

5. **Reload and restart**:

```bash
sudo systemctl daemon-reload && sudo systemctl restart mcp-hugo-server-go
```

### Disable OAuth

To disable OAuth:

1. **Edit the configuration file** and set `oauth.enabled: false`:

```yaml
oauth:
  enabled: false
```

2. **Reload the service**:

```bash
sudo systemctl reload mcp-hugo-server-go
```

When OAuth is disabled, `write` tools are rejected with a `not_authorized` error. `read` tools remain available without authentication — since #450, `read` is fully public (identical gating to the anonymous tier), so disabling OAuth does not hide them.

## Monitoring and Debugging

### Storage Health and One-Time Orphan Cleanup

`get_storage_health` (`write`, #861) is an **advisory-only** integrity
surface: it reports residue that accumulates *outside* a page's own content
bundle and **never deletes anything** (`data.auto_delete` is always `false`).
Each finding carries a stable `code`, `severity`, and `resource_class`.

There are two independent classes of `orphaned_generated_asset` finding
(a `generate_hero_image` `{slug}-featured.jpg` under `static/images` whose
slug has no owning page in the index). Understanding which class you are
looking at tells you whether it is a historical backlog or a live gap:

- **Deleted-page residue (historical only).** Before `delete_page` learned to
  cascade-clean the hero image (#606), deleting a page left its generated hero
  behind. As of #606, `delete_page` removes the `{slug}-featured.jpg` hero as a
  best-effort, non-fatal step whenever the whole bundle is removed (a hero
  shared across surviving translations is deliberately preserved until the last
  translation is deleted). **No new orphans of this class can form** — this
  path is closed and regression-tested
  (`TestDeletePageRemovesOrphanedHeroImage`,
  `TestDeletePageRemovesHeroImageForPublicFormSlug`,
  `TestDeletePageKeepsSharedHeroWhenTranslationSurvives`). Any findings of this
  class on a site that predates #606 are a **one-time backlog**; clean them up
  once with the procedure below.
- **Generated-but-never-attached (inherent, expected).** `generate_hero_image`
  only writes the image file — it never touches page frontmatter, by design
  (it has no language/locking awareness), and its own response warns you to
  call `update_page` with `featured_image=data.public_path` afterward. If you
  generate an image and never create/attach the owning page, that image is an
  orphan until you either attach a page for that slug or delete it. This is not
  a bug to fix in code; it is the advisory gap `get_storage_health` exists to
  surface.

**One-time cleanup procedure** (per `orphaned_generated_asset` finding). The
finding already gives you `slug` and `logical_path`; the filename to pass is
the basename of `logical_path` (e.g. `static/images/posts/x-featured.jpg` →
`x-featured.jpg`). For a *genuine* orphan the owning page is gone, so there is
no bundle to scan for references — `force` is **not** needed. A non-dry-run
delete still requires a concurrency guard, so read the current hash with a
`dry_run` first:

```jsonc
// 1. Read the sha256 (dry_run needs no guard, deletes nothing):
delete_page_asset { "slug": "posts/x", "filename": "x-featured.jpg",
                    "scope": "generated", "dry_run": true }
// -> data.sha256 = "<hash>"

// 2. Delete it, passing that hash as the expected_sha256 guard:
delete_page_asset { "slug": "posts/x", "filename": "x-featured.jpg",
                    "scope": "generated", "expected_sha256": "<hash>" }
```

`scope: "generated"` targets `{HugoRoot}/static/images/{slug}-featured.jpg`
directly (not the page bundle). `delete_page_asset` removes only the source
file, not any already-built public copy or CDN cache, so run `build_site` /
`publish_changes` afterward if the site was already built with the stale
reference. `expired_preview_residue` findings (leftover `mcp-preview-*`
directories with no live preview backing them) are handled separately via
`revoke_preview`. As of v1.8.6 (#1081), a restart with `db_path` configured
restores and reconciles preview leases automatically at startup instead of
orphaning them — this finding now mainly catches TTL expiry between health
checks, or process-local (no `db_path`) deployments where leases still don't
survive a restart.

### View Service Status

```bash
sudo systemctl status mcp-hugo-server-go --no-pager
```

### View Logs

```bash
sudo journalctl -u mcp-hugo-server-go -f
```

### Check Configuration

The server validates the configuration at startup. If the config file is invalid, the service will fail to start and log the error.

```bash
MCP_HUGO_SERVER_CONFIG=/etc/mcp-hugo-server-go/config.yaml /usr/local/bin/mcp-hugo-server-go
```

### Test Tools Locally

To test the server in stdio mode:

```bash
MCP_HUGO_SERVER_CONFIG=/etc/mcp-hugo-server-go/config.yaml /usr/local/bin/mcp-hugo-server-go
```

Then send MCP JSON-RPC requests over stdin.

### Live MCP Smoke Test

Use `scripts/smoke-mcp-live.sh` after staging or production deploys to verify
that MCP discovery, `tools/list`, and representative `tools/call` requests still
work through the real HTTP transport and reverse proxy.

The script is secret-safe:

- it contains no OAuth client secret and no Bearer token;
- it reads the Bearer token only from `MCP_ACCESS_TOKEN`;
- it prints tokens as `<redacted>`;
- it stores request state in a temporary directory that is deleted on exit.

Safe read-only run:

```bash
MCP_SMOKE_LIVE=1 \
MCP_BASE_URL=https://mcp.arleo.eu \
MCP_ACCESS_TOKEN="$MCP_ACCESS_TOKEN" \
scripts/smoke-mcp-live.sh
```

The default mode skips live mutations. To explicitly test create/update/delete
and build behavior, set `MCP_SMOKE_ENABLE_WRITES=1` and use a dedicated test
slug:

```bash
MCP_SMOKE_LIVE=1 \
MCP_BASE_URL=https://mcp.arleo.eu \
MCP_ACCESS_TOKEN="$MCP_ACCESS_TOKEN" \
MCP_SMOKE_ENABLE_WRITES=1 \
MCP_SMOKE_WRITE_SLUG=codex-mcp-live-audit-$(date -u +%Y%m%d-%H%M%S) \
scripts/smoke-mcp-live.sh
```

Before and after write-enabled runs, check for leftovers:

```bash
find /path/to/hugo-site -iname '*codex-mcp-live-audit*' -print
```

Optional burst probe:

```bash
MCP_SMOKE_LIVE=1 \
MCP_ACCESS_TOKEN="$MCP_ACCESS_TOKEN" \
MCP_SMOKE_BURST=1 \
MCP_SMOKE_BURST_COUNT=10 \
scripts/smoke-mcp-live.sh
```

The smoke classifies failures separately:

- HTTP 401/403 authentication failures;
- HTTP 429 rate-limit responses and `Retry-After`;
- JSON-RPC errors;
- `result.isError=true` tool failures;
- `unknown_tool` handling;
- OpenResty or reverse-proxy HTML 503 responses;
- transport success with malformed or missing MCP result payloads.

Do not run write-enabled smoke against production unless you have confirmed the
test slug does not already exist and you are ready to clean it manually if a
client disconnects mid-run.

## Deployment Pipeline

### Overview

The project uses a two-workflow promotion model. `deploy.yml` absorbed the
former standalone `release.yml` — one `workflow_dispatch` call with
`release_version` set now builds, deploys, tags, and publishes the GitHub
release together, so version numbers can't drift apart across the tag,
`npm/package.json`, and `manifest.json` the way they could when the two
were separately-triggered workflows.

```
main branch merge
      │
      ▼
  CI (ci.yml)
  ├── unit tests, vet, staticcheck, govulncheck
  ├── README release-metadata gate
  ├── boot-check (binary starts, 7 endpoints respond)
  └── secret scans (gitleaks + trufflehog)
      │
      ▼  (manually: run deploy.yml)
  deploy.yml
  ├── validate (build + tests)
  │   └── release-notes gate (only when release_version is set): fails
  │       fast, before anything is built, unless CHANGELOG.md/
  │       npm/package.json/manifest.json already carry that version
  ├── deploy (environment: production — requires reviewer approval)
  │   ├── self-hosted runner promotes the selected ref on the VM
  │   ├── systemctl restart
  │   └── post-deploy smoke (smoke-mcp-live.sh)
  ├── release (only when release_version is set, after deploy succeeds)
  │   ├── creates tag + GitHub release
  │   └── attaches GoReleaser binaries + the .mcpb bundle
  └── dry-run validation (no production environment, no deployment record,
      no release)
      │
      ▼  (optional/manual)
  Live Smoke workflow
  └── smoke-mcp-live.sh against live server (read-only)
  └── smoke-agent-interop.sh (OAuth discovery, DCR probe)
```

### GitHub Environments

| Environment | Protection | Purpose |
|-------------|-----------|---------|
| `production` | Required reviewer (jmrGrav) | Self-hosted deployment + post-deploy smoke |
| `staging` | None | Isolated operator-managed staging instance or local synthetic smoke |

> **Note:** The repository now keeps a secret-free staging profile and a local
> synthetic staging smoke. See [docs/staging-runbook.md](staging-runbook.md)
> for the isolated Hugo VM staging layout and the CI/local smoke flow.

### Manual Deployment Steps

1. **Merge the promotion candidate to `main`.**

2. **CI runs automatically** — watch the `test`, `boot-check`, local staging smoke,
   secret scans, and CodeQL checks for green.

3. **For a real release, merge the release-notes PR first** — a `CHANGELOG.md`
   entry (`## [vX.Y.Z] - date`) plus matching `version` fields in
   `npm/package.json` and `manifest.json`. `deploy.yml`'s gate checks these
   exist and match *before* building anything; it never writes this content
   itself. Skip this step for an ad-hoc hotfix deploy with no release cut.

4. **Trigger `deploy.yml` from GitHub Actions → Run workflow:**
   - Input the git ref to promote (default: `main`; SHA allowed)
   - Input `release_version` (e.g. `v1.7.9`) to cut a real release, or leave
     it empty for an ad-hoc mainline deploy with no tag/GitHub release
   - The workflow rejects refs that are not reachable from `origin/main`,
     and — when `release_version` is set — refuses to build at all unless
     `CHANGELOG.md`/`npm/package.json`/`manifest.json` already carry that
     version
   - Approve the `production` environment gate in the Actions UI
   - The workflow builds the selected ref, deploys it on the self-hosted
     runner, restarts the service, and runs the post-deploy smoke
   - If `release_version` was set, it then tags, creates the GitHub release,
     and attaches the GoReleaser binaries + `.mcpb` bundle — all in the same
     run, no second workflow to remember

   ```bash
   gh workflow run deploy.yml -f ref=main -f release_version=v1.7.9
   ```

5. **Close the milestone** on GitHub once the release is published.

### Required Secrets for deploy.yml

Configure these under **Settings → Secrets and variables → Actions**:

| Secret | Description |
|--------|-------------|
| `PRODUCTION_URL` | Base URL of the MCP server (e.g. `https://mcp.arleo.eu`) |
| `MCP_WRITE_ACCESS_TOKEN` | A current write-scoped bearer token used only for fresh post-deploy `tools/list` catalogue parity checks; keep it separate from the read smoke token. Required when `MCP_SMOKE_VERIFY_WRITE_CATALOGUE=1`. |
| `MCP_SMOKE_VERIFY_WRITE_CATALOGUE` | Set to `1` in release/deploy smoke jobs to compare a fresh write session with the versioned tool-registry manifest. Leave unset for secret-free synthetic fixtures. |
| `MCP_ACCESS_TOKEN` | Bearer token for post-deploy smoke read-only calls |

### Rollback

If the post-deploy smoke fails:
```bash
# On the production server:
sudo cp /usr/local/bin/mcp-hugo-server-go.prev /usr/local/bin/mcp-hugo-server-go
sudo systemctl restart mcp-hugo-server-go
```

To preserve the previous binary, add `cp /usr/local/bin/mcp-hugo-server-go{,.prev}` to
the deploy SSH block before the new binary is moved into place.

## Troubleshooting

| Issue | Cause | Solution |
|-------|-------|----------|
| Service fails to start | Config file not found or invalid YAML. | Verify `MCP_HUGO_SERVER_CONFIG` path and YAML syntax. |
| OAuth token endpoint returns error | `oauth.enabled: true` but `oauth.issuer` is not set or empty. | Set `oauth.issuer` to a valid URL. |
| Post-build hooks not firing | Hook URL is invalid or uses a private IP. | Validate the URL format and DNS resolution. |
| `build_site` timeout | Hugo build takes longer than `build_timeout_seconds`. | Increase the timeout value in config. |
| Permission denied when writing pages, builds, or featured images | Systemd service lacks write permissions. | Update `ReadWritePaths` for `content`, `resources`, and `public`, then reload. |
| OpenResty returns HTML 503 under load | Reverse proxy treats upstream 429 as a connection error. | See Pitfall 4 below. |

## Known Deployment Pitfalls

### Pitfall 1 — SQLite storage fails with "unable to open database file"

**Symptom:** Service crashes at startup with:
```
mcp-hugo-server-go: pragma journal_mode: unable to open database file (14)
```

**Cause:** `ProtectSystem=strict` in the service unit makes the entire filesystem read-only except paths listed in `ReadWritePaths`. Creating the directory with the right owner is not enough — the service unit must explicitly whitelist the path.

**Fix:** Two steps are both required:

```bash
# 1. Create the directory and set ownership
sudo mkdir -p /var/lib/mcp-hugo-server-go
sudo chown mcp-hugo-server-go:mcp-hugo-server-go /var/lib/mcp-hugo-server-go

# 2. Add it to ReadWritePaths in the service unit
sudo sed -i 's|ReadWritePaths=|ReadWritePaths=/var/lib/mcp-hugo-server-go |' \
    /etc/systemd/system/mcp-hugo-server-go.service
sudo systemctl daemon-reload && sudo systemctl restart mcp-hugo-server-go
```

Or edit `/etc/systemd/system/mcp-hugo-server-go.service` manually:
```ini
ReadWritePaths=/var/lib/mcp-hugo-server-go /home/user/hugo-site/content /home/user/hugo-site/resources /home/user/hugo-site/public
```

---

### Pitfall 2 — Write/build/image tools fail with "read-only file system"

**Symptom:** `create_page`, `update_page`, `delete_page`, `build_site`, `preview_build`, or `generate_hero_image` fail even though the service user has Unix access to the Hugo tree.

**Cause:** `ProtectHome=read-only` blocks all writes under `/home/`, including directories the service user owns or belongs to via group membership. Group membership is not sufficient — systemd's namespace isolation applies before Unix permissions.

**Fix:**

```bash
# Add all Hugo write paths to ReadWritePaths
sudo sed -i 's|ReadWritePaths=|ReadWritePaths=/home/user/hugo-site/content /home/user/hugo-site/resources /home/user/hugo-site/public |' \
    /etc/systemd/system/mcp-hugo-server-go.service
sudo systemctl daemon-reload && sudo systemctl restart mcp-hugo-server-go
```

Also ensure the service user has group write access to the relevant directories:
```bash
sudo usermod -aG <site-owner-group> mcp-hugo-server-go
```

If `build_site` still fails with `operation not permitted` on a specific file
under `public/` or `resources/`, do not assume `ReadWritePaths` is the only
cause. Hugo calls `chtimes` on existing output files, and that requires the MCP
service user to own those files. The live `mcp.arleo.eu` failure was:

```text
Error: error copying static files: chtimes /home/jm/hugo-site/public/auth.md: operation not permitted
```

That symptom usually means ownership drift inside the output tree, not a missing
systemd write allowlist. Inspect the exact path from the error, then repair
ownership recursively for the affected subtree before retrying `build_site`:

```bash
sudo chown -R $(systemctl show mcp-hugo-server-go -p User --value) /home/jm/hugo-site/public
sudo systemctl restart mcp-hugo-server-go
```

---

### Pitfall 3 — `validate_site` / `build_site` fail with "hugo: not found" or "Connection failed"

**Symptom:** `validate_site` returns `"Connection failed"` or `"hugo: command not found"`. The `hugo` binary is installed and works fine when run as a normal user.

**Cause:** Systemd services run with a minimal `PATH` that typically excludes `/usr/local/bin`. If Hugo was installed via the official installer (e.g., `snap`, direct download, or `go install`), it lands in `/usr/local/bin` which is absent from the service environment.

**Fix:** Add an explicit `PATH` in the service unit:

```bash
sudo systemctl edit mcp-hugo-server-go
```

Add under `[Service]`:
```ini
Environment=PATH=/usr/local/bin:/usr/bin:/bin
```

Or edit `/etc/systemd/system/mcp-hugo-server-go.service` directly, then:
```bash
sudo systemctl daemon-reload && sudo systemctl restart mcp-hugo-server-go
```

Verify:
```bash
sudo -u mcp-hugo-server-go env PATH=/usr/local/bin:/usr/bin:/bin which hugo
```

---

### Pitfall 4 — OpenResty / nginx returns HTML 503 after rate-limit saturation

**Symptom:** When a burst of MCP tool calls exhausts the rate limit, the reverse proxy returns a generic HTML page with `503 Service Temporarily Unavailable` instead of the JSON-RPC 429 body from the server. Smoke test prints `PROXY_FAIL ... html=true`.

**Cause:** Some OpenResty / nginx configurations treat upstream responses that arrive very quickly (including rate-limit 429s) as upstream errors, or the `proxy_pass` buffer is too small to forward the JSON body. The default `error_page 503` directive rewrites the body with OpenResty's built-in HTML page.

**Fix — forward the upstream 429 as-is:**

Add the following directives inside the relevant `location /mcp` block:

```nginx
location /mcp {
    proxy_pass http://127.0.0.1:8088;

    # Forward the upstream 429 body without modification.
    # Without this, nginx replaces upstream error bodies with its own HTML page.
    proxy_intercept_errors off;

    # Ensure the Retry-After header from the upstream reaches the MCP client.
    proxy_pass_header Retry-After;

    # Allow time for the MCP streaming response to complete.
    proxy_read_timeout 120s;

    # Keep response buffering off so streaming MCP responses flow immediately.
    proxy_buffering off;
}
```

If `proxy_intercept_errors` must remain `on` (e.g., to serve a custom 502 error page), add a passthrough for 429:

```nginx
proxy_intercept_errors on;
error_page 400 401 403 404 /4xx.html;  # custom pages for these codes
# 429 intentionally omitted — let the upstream JSON body flow through
```

**Verify the fix:**

```bash
# Should print JSON, not HTML
curl -sS -o /dev/null -w '%{content_type}' \
  -X POST https://mcp.arleo.eu/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_site_information","arguments":{}}}' \
  | grep -q application/json && echo OK || echo FAIL
```

---

### Pitfall 5 — restart-safe persistence (recovery journal, mutation journal, preview leases, build reconciliation) silently doesn't survive a restart

**Symptom:** No errors anywhere. The server starts, serves traffic normally, and every tool works — but a restart still loses in-flight mutation state, preview leases don't survive a restart, and `get_runtime_status`'s `content_index_shadow`, `mutation_journal`, and `build_reconciliation` fields are simply absent from the response instead of present-but-empty.

**Cause:** All of this is backed by the *operational* SQLite database at `db_path` (see [Operational SQLite Persistence](#operational-sqlite-persistence) above) — not `oauth.storage_path`, which is a separate database for a separate purpose (OAuth tokens). Every one of these subsystems was deliberately built with an in-memory fallback so a deployment without `db_path` set keeps working exactly as before v1.8.6, with no crash and no error. This bit the actual v1.8.6 production deploy: only `oauth.storage_backend: sqlite` was configured, `db_path` never was, so the whole restart-safety programme ran silently degraded for hours until caught by manually cross-checking `get_runtime_status`'s response shape against what the release notes said should be there.

**Since the follow-up fix, this is no longer a silent gap** — `get_capabilities` now reports it directly:

```json
"disabled_features": [
  {"name": "durable_persistence", "reason": "feature_disabled", "required_configuration": "db_path"}
]
```

and the server logs a startup warning on any http deployment with a `content_root` configured. Check `get_capabilities` first; the rest of this section is the fix once you've confirmed the gap.

**Fix:**

```yaml
# /etc/mcp-hugo-server-go/config.yaml
db_path: /var/lib/mcp-hugo-server-go/site.db
```
```bash
sudo systemctl restart mcp-hugo-server-go
```

If the target directory isn't already in the service unit's `ReadWritePaths` (check whether `oauth.storage_path` already lives there — if so, reuse that directory and skip this step), see Pitfall 1 above.

**Verify the fix actually took effect** — don't just check for a clean restart:

```bash
# durable_persistence must be gone from disabled_features
curl -sS -X POST https://mcp.arleo.eu/mcp \
  -H 'Content-Type: application/json' -H 'Authorization: Bearer <token>' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_capabilities","arguments":{}}}' \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); data=d["result"]["structuredContent"]["data"]; names={f["name"] for f in data.get("disabled_features",[])}; print("durable_persistence" not in names)'
```
Should print `True`. `get_runtime_status`'s `content_index_shadow`/`mutation_journal` fields are the equivalent older signal; `build_reconciliation` only appears after the first `build_site` call post-restart, so its absence alone is not diagnostic.

---

## References

- [mcp-hugo-server-go GitHub](https://github.com/jmrGrav/mcp-hugo-server-go)
- [Hugo Documentation](https://gohugo.io/documentation/)
- [OAuth 2.0 Specification](https://tools.ietf.org/html/rfc6749)
- [PKCE (RFC 7636)](https://tools.ietf.org/html/rfc7636)
