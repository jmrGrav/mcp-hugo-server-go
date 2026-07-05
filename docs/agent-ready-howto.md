# AgentReady 100% HowTo

This page documents the working `www.arleo.eu` / `mcp.arleo.eu` setup that
returned IsItAgentReady to 100/100 on 2026-07-05.

It is a recovery checklist and reference snapshot. Keep it practical: if a
future change regresses AgentReady discovery, compare the live system with this
page first.

## Topology

```text
Internet / Cloudflare
  |
  v
host OpenResty
  |-- www.arleo.eu  -> http://192.168.122.69:80  -> hugo-vm Nginx -> /var/www/hugo
  |-- mcp.arleo.eu  -> http://192.168.122.69:8088 -> mcp-hugo-server-go
  |
  v
hugo-vm
  |-- /home/jm/hugo-site/static      source static discovery files
  |-- /home/jm/hugo-site/public      Hugo build output
  |-- /var/www/hugo                  symlink to /home/jm/hugo-site/public
```

Important: the real Hugo source is on `hugo-vm`, not the local checkout on the
operator host.

## AgentReady Invariants

These public URLs must stay coherent:

| URL | Expected behavior |
| --- | --- |
| `https://www.arleo.eu/auth.md` | `200 text/markdown`, contains `agent_auth_metadata` and ID-JAG credential types |
| `https://www.arleo.eu/.well-known/oauth-protected-resource` | `200 application/json`, resource is `https://www.arleo.eu`, authorization server is `https://mcp.arleo.eu` |
| `https://mcp.arleo.eu/.well-known/oauth-authorization-server` | `200 application/json`, issuer is `https://mcp.arleo.eu`, contains `agent_auth` |
| `https://mcp.arleo.eu/.well-known/oauth-protected-resource` | `200 application/json`, resource is `https://mcp.arleo.eu/mcp` |
| `https://mcp.arleo.eu/.well-known/mcp/server-card.json` | `200 application/json`, transport endpoint is `/mcp` |
| `https://mcp.arleo.eu/.well-known/mcp.json` | compatibility alias for server card |
| `https://www.arleo.eu/.well-known/agent-skills/index.json` | `200 application/json` |

The ID-JAG block is easy to break. Both OAuth metadata and `/auth.md` must keep:

```json
"identity_assertion": {
  "assertion_types_supported": ["urn:ietf:params:oauth:token-type:id-jag"],
  "credential_types_supported": ["urn:ietf:params:oauth:token-type:id-jag"]
}
```

See issue [#120](https://github.com/jmrGrav/mcp-hugo-server-go/issues/120).

## Host OpenResty

Reference examples live in:

- [`docs/examples/agent-ready/openresty-www.arleo.eu.conf`](examples/agent-ready/openresty-www.arleo.eu.conf)
- [`docs/examples/agent-ready/openresty-mcp.arleo.eu.conf`](examples/agent-ready/openresty-mcp.arleo.eu.conf)

Production paths on the host:

```bash
/etc/openresty/sites-enabled/www.arleo.eu
/etc/openresty/sites-enabled/mcp.arleo.eu
```

Key points:

- `www.arleo.eu` proxies static AgentReady files to the Hugo VM.
- `www.arleo.eu/mcp` redirects to `https://mcp.arleo.eu/mcp`.
- `www.arleo.eu/.well-known/oauth-authorization-server` redirects to the MCP issuer.
- `www.arleo.eu/.well-known/oauth-protected-resource` is a protected-resource document for the website resource.
- `mcp.arleo.eu` proxies all paths to `mcp-hugo-server-go` on the Hugo VM.

## Hugo VM Nginx

Reference example:

- [`docs/examples/agent-ready/nginx-hugo-vm.conf`](examples/agent-ready/nginx-hugo-vm.conf)

Production path on `hugo-vm`:

```bash
/etc/nginx/sites-available/hugo
/etc/nginx/sites-enabled/hugo -> /etc/nginx/sites-available/hugo
```

Working shape:

```nginx
server {
    listen 80 default_server;
    server_name _;
    root /var/www/hugo;
    index index.html;

    location / {
        try_files $uri $uri/ /fr/$uri /fr/$uri/ =404;
    }
}
```

`/var/www/hugo` is a symlink to `/home/jm/hugo-site/public`.

## Hugo Static Files

Reference examples live in:

- [`docs/examples/agent-ready/static/auth.md`](examples/agent-ready/static/auth.md)
- [`docs/examples/agent-ready/static/.well-known/oauth-protected-resource`](examples/agent-ready/static/.well-known/oauth-protected-resource)
- [`docs/examples/agent-ready/static/llms.txt`](examples/agent-ready/static/llms.txt)
- [`docs/examples/agent-ready/static/robots.txt`](examples/agent-ready/static/robots.txt)

Production source paths on `hugo-vm`:

```bash
/home/jm/hugo-site/static/auth.md
/home/jm/hugo-site/static/.well-known/oauth-protected-resource
/home/jm/hugo-site/static/.well-known/api-catalog
/home/jm/hugo-site/static/.well-known/agent-skills/index.json
/home/jm/hugo-site/static/.well-known/agent-skills/*.md
/home/jm/hugo-site/static/llms.txt
/home/jm/hugo-site/static/robots.txt
```

After changing them, rebuild:

```bash
ssh hugo-vm 'sudo -u hugo-mcp sh -lc "cd /home/jm/hugo-site && hugo --destination /var/www/hugo --cleanDestinationDir"'
```

Then purge Cloudflare for the exact changed URLs. Never print tokens in logs.

## MCP Server Generator

The MCP server also generates Auth.md metadata when serving `https://mcp.arleo.eu/auth.md`.
Keep these in sync:

```text
internal/server/discovery.go
internal/server/discovery_test.go
scripts/check-agent-ready.sh
scripts/smoke-agent-interop.sh
```

## Validation

Run:

```bash
go test ./...
go vet ./...
staticcheck ./...
govulncheck ./...
gitleaks detect --no-banner --redact --source .
SMOKE_LIVE=1 ./scripts/check-agent-ready.sh
SMOKE_LIVE=1 ./scripts/smoke-agent-interop.sh
```

Then scan:

```text
https://isitagentready.com/www.arleo.eu
```

Expected:

- score `100/100`
- `API, Auth, MCP & Skill Discovery = 7/7`
- `Auth.md agent registration` green
