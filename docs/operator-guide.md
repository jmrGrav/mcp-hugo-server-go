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

### Build Configuration

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `build_timeout_seconds` | int | `120` | Maximum time (in seconds) to wait for Hugo build to complete. |
| `post_build_hooks` | array of strings | (empty) | URLs to POST a `{"event":"post_build"}` webhook to after successful site build. Only HTTPS endpoints and public DNS hostnames are allowed (SSRF protected). |

### Rate Limiting

The `rate_limit` section controls per-scope request rates (per minute):

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `rate_limit.anonymous_per_min` | int | `60` | Requests per minute for anonymous (no-auth) scope. |
| `rate_limit.content_read_per_min` | int | `120` | Requests per minute for `content.read` scope. |
| `rate_limit.content_write_per_min` | int | `30` | Requests per minute for `content.write` scope. |
| `rate_limit.site_admin_per_min` | int | `10` | Requests per minute for `site.admin` scope. |
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
| `oauth.storage_backend` | string | `memory` | Token persistence backend: `memory` (ephemeral), `json` (file-based), or `sqlite` (database). |
| `oauth.storage_path` | string | (empty) | Path to token storage file (required for `json` or `sqlite` backends). |

## Tool Access Scopes

The server exposes tools across five access tiers. Each tier is a superset of lower tiers: agents with `content.write` access can call all `content.read` tools, and so on.

Legacy clients may still send `mcp` as a scope. It is accepted as a deprecated alias for `content.read` for backward compatibility, but it is not advertised as a canonical scope and should not be used by new clients.

To enable confidential OAuth clients for `content.write`, `site.admin`, or `system.admin`, set `oauth.client_registry_path` to a root-readable YAML file on the host. Each entry must include a `client_id`, a `client_secret` or `client_secret_hash`, redirect URIs, and a canonical scope.

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

Edit the `REMOTE` variable in `deploy/deploy.sh` to target a different host.

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
  storage_backend: memory
  storage_path: ""
```

### Service File

The systemd service is installed to `/etc/systemd/system/mcp-hugo-server-go.service`. Key settings:

- **User/Group**: `mcp-hugo-server-go` (create this user before running).
- **Environment**: `MCP_HUGO_SERVER_CONFIG=/etc/mcp-hugo-server-go/config.yaml`.
- **Security**: `ProtectSystem=full`, `ProtectHome=true`, `CapabilityBoundingSet=` (no capabilities).
- **Write Paths**: `ReadWritePaths=/var/lib/mcp-hugo-server-go /srv/hugo-site` (adjust to match `site_root` and OAuth storage path).

To run in read-only mode (anonymous and `content.read` only):
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
# Call build_site (requires site.admin scope)
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

When OAuth is disabled, all authenticated tools (`content.read`, `content.write`, `site.admin`, `system.admin`) are rejected with a `not_authorized` error. Only anonymous tools remain available.

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

## Troubleshooting

| Issue | Cause | Solution |
|-------|-------|----------|
| Service fails to start | Config file not found or invalid YAML. | Verify `MCP_HUGO_SERVER_CONFIG` path and YAML syntax. |
| OAuth token endpoint returns error | `oauth.enabled: true` but `oauth.issuer` is not set or empty. | Set `oauth.issuer` to a valid URL. |
| Post-build hooks not firing | Hook URL is invalid or uses a private IP. | Validate the URL format and DNS resolution. |
| `build_site` timeout | Hugo build takes longer than `build_timeout_seconds`. | Increase the timeout value in config. |
| Permission denied when writing pages | Systemd service lacks write permissions. | Update `ReadWritePaths` in the service file and reload. |

## References

- [mcp-hugo-server-go GitHub](https://github.com/jmrGrav/mcp-hugo-server-go)
- [Hugo Documentation](https://gohugo.io/documentation/)
- [OAuth 2.0 Specification](https://tools.ietf.org/html/rfc6749)
- [PKCE (RFC 7636)](https://tools.ietf.org/html/rfc7636)
