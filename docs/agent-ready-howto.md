# AgentReady 100% HowTo

This page documents the working `www.arleo.eu` / `mcp.arleo.eu` setup that
returned IsItAgentReady to 100/100 on 2026-07-05 and was revalidated after the
2026-07-13 public discovery regression.

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
| `https://www.arleo.eu/.well-known/oauth-protected-resource/mcp` | compatibility alias for the MCP protected-resource document; must not degrade to HTML `403` |
| `https://www.arleo.eu/.well-known/mcp/server-card.json` | compatibility redirect to the canonical MCP server card on `mcp.arleo.eu` |
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
- `www.arleo.eu/.well-known/oauth-protected-resource/mcp` redirects to the MCP protected-resource alias on `mcp.arleo.eu`.
- `www.arleo.eu/.well-known/mcp/server-card.json` redirects to the canonical MCP server card on `mcp.arleo.eu`.
- `mcp.arleo.eu` proxies all paths to `mcp-hugo-server-go` on the Hugo VM.

Do not leave backup copies of active vhosts inside the include glob used by
OpenResty. On the current host, `sites-enabled/*` is loaded verbatim, so a file
such as `www.arleo.eu.bak-*` inside that directory can silently reintroduce an
old server block and serve stale discovery behavior in production.

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

After changing them, do not assume the documented Hugo service user can rebuild
successfully. Validate the real ownership of `public/`, `static/`, and
`/var/www/hugo` first:

```bash
ssh hugo-vm 'id hugo-mcp'
ssh hugo-vm 'stat -c "%U:%G %a %n" /home/jm/hugo-site/static /home/jm/hugo-site/public /var/www/hugo'
```

Then rebuild:

```bash
ssh hugo-vm 'sudo -u hugo-mcp sh -lc "cd /home/jm/hugo-site && hugo --destination /var/www/hugo --cleanDestinationDir"'
```

If that fails with a filesystem error such as `chtimes ... operation not
permitted`, fix the ownership or publish the exact static files intentionally;
do not pretend the build succeeded.

Then purge Cloudflare for the exact changed URLs. Never print tokens in logs.

## 2026-07-13 Regression Notes

The public regression that broke the `www.arleo.eu` discovery surface was not a
Go runtime bug. The effective causes were:

1. a stale backup vhost left inside the active OpenResty `sites-enabled/*`
   include path, which caused conflicting `server_name` blocks and served the
   wrong public discovery behavior;
2. stale Hugo static discovery files on the VM, still advertising
   `system.admin`;
3. a rebuild command that looked valid in docs but failed in reality because
   the Hugo output tree ownership did not match the `hugo-mcp` user.

The shortest reliable recovery path was:

1. remove the stale backup vhost from the active include path;
2. update the Hugo static discovery source files;
3. publish the corrected public files intentionally;
4. revalidate `www.arleo.eu` and `mcp.arleo.eu` with curl before blaming cache.

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
