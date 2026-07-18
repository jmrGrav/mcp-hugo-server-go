# Operator Guide: mcp-hugo-server-go

This document describes how to deploy, configure, and operate the Hugo MCP server.

## Environment Configuration

The server reads its configuration from the path specified by the `MCP_HUGO_SERVER_CONFIG` environment variable.

```bash
export MCP_HUGO_SERVER_CONFIG=/etc/mcp-hugo-server-go/config.yaml
```

If the environment variable is not set or points to an empty path, the server uses built-in defaults.

## Configuration Fields

Configuration is stored in YAML format. The following table lists all available fields, their types, defaults, and purposes.

### Core Site Settings

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `site_root` | string | (required) | Absolute path to the Hugo site root directory. |
| `hugo_root` | string | (required) | Absolute path to the Hugo installation or theme root. |
| `content_root` | string | (required) | Absolute path to Hugo content directory (where `.md` files live). |
| `site_url` | string | (required) | Public URL of the Hugo site (e.g., `https://www.arleo.eu`). |
| `site_name` | string | (required) | Display name of the site. |
| `language_default` | string | `en` | Default language code for content. |

### Server Transport

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `transport` | string | `stdio` | Communication protocol: `stdio` (standard input/output) or `http` (HTTP server). |
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

## Tool Access Scopes

Since #450, the server enforces exactly two internal scopes, which map
directly onto the two external profiles operators should think in:

- `reader` / `read`: full visibility, including drafts and other
  source-only/pre-publication content (an explicit operator
  risk-acceptance decision — see `docs/mcp-contract.md` §6.12). Requires no
  secret; auto-registrable.
- `operator` / `write`: reader capability plus mutations and site
  operations. Requires a registered OAuth client. `write` implies `read` —
  there is no third tier; every tool that used to require a separate
  `site.admin` scope now just requires `write`.

Legacy clients may still send any pre-#450 scope string (`mcp`, `reader`,
`content.read`, `content.write`, `site.admin`, `system.admin`, and other
aliases — see `docs/mcp-contract.md` §6.12 for the full table). They are
accepted as deprecated compatibility aliases resolved to `read`/`write`, but
are not advertised as canonical scopes and should not be used by new
clients.

Published discovery metadata now carries both:

- canonical internal scope strings (`read`, `write`) in `scopes_supported`
- additive `access_profiles.reader` / `access_profiles.operator` metadata for the simplified external model

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
ReadWritePaths=/var/lib/mcp-hugo-server-go /path/to/hugo-site/content /path/to/hugo-site/resources /path/to/hugo-site/public
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
| `build_site` | `site_root` (site root), `{hugo_root}/resources` |
| `preview_build` | `{hugo_root}/resources` (render-to-memory; no writes to `public/`) |
| `generate_hero_image` | `{hugo_root}/static/images` |

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

Note: when `hugo_root` and `site_root` point to the same directory (single-root
deployments), you may already have `ReadWritePaths` covering the parent — but if they
differ, `static/images` under `hugo_root` is a distinct path that needs its own entry.

#### `get_broken_links` (and other index tools) return stale results after `build_site`

The site index is built once at startup by walking the public HTML directory. A
`build_site` call regenerates those files on disk, but the in-memory index is **not
refreshed automatically** until issue #212 is resolved.

Workaround: restart the service after every Hugo build to force a fresh index scan.

```bash
sudo systemctl restart mcp-hugo-server-go
```

Until #212 ships, any `get_broken_links`, `search_content`, or page-count result
obtained immediately after `build_site` reflects the index from the previous build.

### Configuration File

Place the configuration file at the path referenced by `MCP_HUGO_SERVER_CONFIG`:

```yaml
site_root: /srv/hugo-site
hugo_root: /srv/hugo-site
content_root: /srv/hugo-site/content
site_url: https://www.arleo.eu
site_name: Arleo
language_default: en
transport: stdio
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
post_build_hooks:
  - https://example.com/webhook/post-build
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

The project now uses a three-workflow promotion model:

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
  ├── deploy (environment: production — requires reviewer approval)
  │   ├── self-hosted runner promotes the selected ref on the VM
  │   ├── systemctl restart
  │   └── post-deploy smoke (smoke-mcp-live.sh)
  └── dry-run validation (no production environment, no deployment record)
      │
      ▼  (manually: run release.yml)
  release.yml
  ├── requires current origin/main HEAD
  ├── requires successful production deployment for that SHA
  ├── checks CHANGELOG.md and README release metadata
  └── creates tag + GitHub release
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

3. **Trigger `deploy.yml` from GitHub Actions → Run workflow:**
   - Input the git ref to promote (default: `main`; SHA allowed)
   - The workflow rejects refs that are not reachable from `origin/main`
   - Approve the `production` environment gate in the Actions UI
   - The workflow builds the selected ref, deploys it on the self-hosted runner,
     restarts the service, and runs the post-deploy smoke

4. **Trigger `release.yml` only after the deploy succeeds:**
   - Input the release version (for example `v1.3.5`)
   - Input the release ref (normally `main`)
   - The workflow refuses to publish unless the target ref:
     - resolves to the current `origin/main` HEAD;
     - already has a successful `production` deployment record;
     - passes the `CHANGELOG.md` gate;
     - passes the machine-checkable README release metadata gate.

5. **Close the milestone** on GitHub once the release is published.

### Required Secrets for deploy.yml

Configure these under **Settings → Secrets and variables → Actions**:

| Secret | Description |
|--------|-------------|
| `PRODUCTION_URL` | Base URL of the MCP server (e.g. `https://mcp.arleo.eu`) |
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

If `build_site` still fails with `operation not permitted` on `public/`, fix the
directory mode/ownership too. The live `mcp.arleo.eu` failure was:

```text
Error: error copying static files: chtimes /home/jm/hugo-site/public: operation not permitted
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

## References

- [mcp-hugo-server-go GitHub](https://github.com/jmrGrav/mcp-hugo-server-go)
- [Hugo Documentation](https://gohugo.io/documentation/)
- [OAuth 2.0 Specification](https://tools.ietf.org/html/rfc6749)
- [PKCE (RFC 7636)](https://tools.ietf.org/html/rfc7636)
