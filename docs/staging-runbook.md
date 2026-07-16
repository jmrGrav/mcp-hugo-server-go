# Staging Runbook

This repository keeps a secret-free staging profile and smoke workflow so the
isolated Hugo VM staging instance can be recreated without relying on shell
history.

## What is versioned

- `deploy/config-staging.yaml`
- `deploy/systemd/mcp-hugo-server-go-staging.service.example`
- `scripts/staging-smoke-local.sh`
- `scripts/smoke-mcp-live.sh`

None of those files contain OAuth client secrets or bearer tokens.

## Current operator-managed staging layout

The current staging instance is isolated on the Hugo VM under:

- `/var/lib/mcp-hugo-server-go-staging/bin`
- `/var/lib/mcp-hugo-server-go-staging/config.yaml`
- `/var/lib/mcp-hugo-server-go-staging/site`
- `/var/lib/mcp-hugo-server-go-staging/tokens.db`

The staging HTTP bind is local-only:

- `http://127.0.0.1:18088`

OAuth client secrets remain host-local in:

- `/etc/mcp-hugo-server-go/oauth-clients.yaml`

They are not copied into the repo and should never be pasted into CI logs,
issues, comments, or docs.

## Provision or refresh the isolated VM staging instance

1. Build the branch binary locally:

```bash
GOOS=linux GOARCH=amd64 \
go build -ldflags "-X github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo.Version=staging-$(git rev-parse --short HEAD)" \
  -o /tmp/mcp-hugo-server-go-staging ./cmd/mcp-hugo-server-go
```

2. Copy the binary and staging assets to the Hugo VM.
3. Install the sample config from `deploy/config-staging.yaml` as
   `/var/lib/mcp-hugo-server-go-staging/config.yaml` and adjust only host-local
   values.
4. Install a staging service using
   `deploy/systemd/mcp-hugo-server-go-staging.service.example`.
5. Ensure the service user can write:

```ini
ReadWritePaths=/var/lib/mcp-hugo-server-go-staging \
               /var/lib/mcp-hugo-server-go-staging/site/content \
               /var/lib/mcp-hugo-server-go-staging/site/resources \
               /var/lib/mcp-hugo-server-go-staging/site/public
```

6. Restart the service and verify:

```bash
sudo systemctl restart mcp-hugo-server-go-staging
sudo systemctl status --no-pager mcp-hugo-server-go-staging
curl -sf http://127.0.0.1:18088/.well-known/oauth-authorization-server | jq .
```

## Secret-free local staging smoke

For CI and local developer verification, the repository contains a synthetic
staging boot script:

```bash
bash scripts/staging-smoke-local.sh
```

What it does:

- builds the current branch binary if needed
- boots an isolated local HTTP server against repo fixtures
- mints a local OAuth token from a temporary registry
- runs `scripts/smoke-mcp-live.sh` in read-only mode

What it does not do:

- no external endpoint
- no host secrets
- no write/build smoke

The local smoke is intentionally read-only. Full write/build smoke still belongs
to the isolated Hugo VM staging instance because it needs a real Hugo runtime,
writable output paths, and the operator's local OAuth registry.

## VM staging smoke

Once the isolated VM staging instance is running, acquire a local token from the
host-local registry and run:

```bash
MCP_SMOKE_LIVE=1 \
MCP_BASE_URL=http://127.0.0.1:18088 \
MCP_ACCESS_TOKEN='<redacted>' \
bash scripts/smoke-mcp-live.sh
```

For write/build checks:

```bash
MCP_SMOKE_LIVE=1 \
MCP_BASE_URL=http://127.0.0.1:18088 \
MCP_ACCESS_TOKEN='<redacted>' \
MCP_SMOKE_ENABLE_WRITES=1 \
MCP_SMOKE_WRITE_SLUG=codex-mcp-live-audit-$(date -u +%Y%m%d-%H%M%S) \
bash scripts/smoke-mcp-live.sh
```

If `generate_featured_image` is intentionally disabled because `image_gen_url`
is absent, the tool should return a structured `config_error`. That is expected
operator feedback, not a secret or a crash.

Always confirm that no `codex-mcp-live-audit-*` residue remains in staging
content after write smoke.
