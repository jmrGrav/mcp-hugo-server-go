# Public URL topology (arleo.eu)

Every public URL under `www.arleo.eu` and `mcp.arleo.eu` is served by one of
three layers, and it's easy to lose track of which:

1. **MCP-dynamic** — served by this repo's Go binary (`internal/server`),
   running as a systemd service on **hugo-vm** (192.168.122.69:8088).
   Config lives in *this* repo, in `deploy/config-production.yaml` /
   `/etc/mcp-hugo-server-go/config.yaml` on hugo-vm.
2. **Hugo-static** — pre-rendered files under `hugo-site`'s `public/`,
   served by plain **nginx on hugo-vm** (port 80). Config lives in the
   `hugo-site` repo (content/data/layouts) plus hugo-vm's own nginx vhost
   (`/etc/nginx/sites-available/hugo-public` on hugo-vm, not this repo).
3. **OpenResty-only** — the request never reaches hugo-vm at all: it's a
   redirect, a hardcoded `return`, or a content-type override entirely
   inside the gateway config. Config lives on the **local NUC**, and NOT
   in any git repo — see the gotcha below.

## The sites-enabled/sites-available gotcha

The NUC runs **OpenResty** (not plain nginx — confirmed via `which
openresty` / `systemctl status openresty`). For every vhost *except*
`www.arleo.eu` and `mcp.arleo.eu`, `/etc/nginx/sites-enabled/*` is a
symlink into `/etc/nginx/sites-available/*`, so editing one edits both.

**`www.arleo.eu` and `mcp.arleo.eu` are the exception.** Their
`sites-enabled` files are real, independently-maintained files —
`sites-available/{www,mcp}.arleo.eu` are frozen, unused, pre-OpenResty-
migration copies. Editing `sites-available` for these two hosts has
**zero effect** on production. Always edit `/etc/nginx/sites-enabled/`
directly, back it up first (`sudo cp ... sites-available/<name>.bak-<ts>-<reason>`
— never leave the backup in `sites-enabled/`, the `include sites-enabled/*`
glob will load it and OpenResty will refuse to reload with a "conflicting
server name" error), then `sudo openresty -t && sudo systemctl reload openresty`.

## mcp.arleo.eu

Single catch-all proxy — everything here is **MCP-dynamic**.

| Path | Layer | Notes |
|---|---|---|
| `/` (all paths) | MCP-dynamic | `location /` proxies straight to `hugo_public_mcp` (192.168.122.69:8088). No path-specific OpenResty logic. |

## www.arleo.eu

Config: `/etc/nginx/sites-enabled/www.arleo.eu` on the NUC (the real file,
not `sites-available`).

| Path | Layer | Notes |
|---|---|---|
| `/mcp`, `/mcp/*` | OpenResty-only | 308 redirect to `mcp.arleo.eu/mcp` — never reaches hugo-vm. |
| `/.well-known/mcp.json` | OpenResty-only | 308 redirect to `mcp.arleo.eu/.well-known/mcp.json`. |
| `/.well-known/mcp/server-card.json` | OpenResty-only | 308 redirect to `mcp.arleo.eu` equivalent. |
| `/.well-known/oauth-protected-resource/mcp` | MCP-dynamic | Reverse-proxied (not redirected) straight to `hugo_public_mcp`, `Host` header preserved as `www.arleo.eu`. Served by `handleOAuthProtectedResourceMCP` in `internal/server/discovery.go` — this alias's `resource` field never varies by host (RFC 9728 §3.1: its path fixes the resource identity to `<issuer>/mcp`). |
| `/.well-known/oauth-protected-resource` | MCP-dynamic | Same reverse-proxy pattern, but the **base** path *is* host-aware: `buildProtectedResourceMeta` reflects the queried host into `resource` only when it equals `cfg.SiteURL`'s hostname (`www.arleo.eu`) — fixed after the isitagentready.com regression (2026-08-22): a prior 308-redirect version returned `resource: mcp.arleo.eu/mcp` for a request that came in on `www.arleo.eu`, which several RFC 9728 validators reject as a mismatch. |
| `/auth.md` | MCP-dynamic | As of 2026-08-22, `proxy_pass` to `hugo_public_mcp` (previously proxied straight to hugo-vm:80's Hugo-static output). Served by `handleAuthMd` in `internal/server/discovery.go`, which reads the prose from `cfg.SiteRoot/auth.md` (still hand-maintained markdown in `hugo-site/static/auth.md`, copied by Hugo into `public/`) but **rewrites the `/register` and `/token` `"returns"` field lists in-place via reflection** over `oauth.RegistrationResponse`/`oauth.TokenResponse` before serving — those two structs are the ground truth, so the served surface can no longer drift from them even if the source markdown does. Everything else in the file (prose, other endpoints, examples) is served verbatim, unenforced. |
| `/.well-known/api-catalog` | Hugo-static | Proxied to hugo-vm:80, Lua-forced `Content-Type: application/linkset+json`. |
| `/.well-known/agent-skills/index.json`, `/.well-known/agent-skills/schema.json`, `/.well-known/agent-skills/*` | Hugo-static | Proxied to hugo-vm:80. `index.json`/`schema.json` get their own exact-match locations (JSON content-type) ahead of the `^~ /.well-known/agent-skills/` prefix block, which force-sets `text/markdown` for every `*.md` skill file. |
| `/.well-known/agent.json` | OpenResty-only | Hardcoded `return 404` — the canonical copy lives only at `mcp.arleo.eu/.well-known/agent.json`; www.arleo.eu deliberately never claims to have one. |
| `/.well-known/agent-card.json` | OpenResty-only | Hardcoded `return 404`. |
| `/.well-known/openid-configuration` | OpenResty-only | Hardcoded `return 404`. |
| `/.well-known/oauth-authorization-server` | OpenResty-only | 308 redirect to `mcp.arleo.eu` equivalent. |
| `/.well-known/ai-catalog.json` | Hugo-static | Proxied to hugo-vm:80, JSON content-type forced. |
| `/ping` | OpenResty-only | `alias /etc/nginx/pong.txt`, basic-auth gated — BetterStack monitoring target, never touches hugo-vm. |
| `/api/mcp*` | OpenResty-only | Hardcoded `return 444` (connection dropped) — legacy path deliberately blocked. |
| `/csp-report` | OpenResty-only | Lua-parsed CSP violation reports, logged locally, `204` returned. Never reaches hugo-vm. |
| `/nginx_status` | OpenResty-only | `stub_status`, LAN-only. |
| everything else under `/.well-known/` not listed above | OpenResty-only | Blanket `deny all` (`location ^~ /.well-known/`) below the specific overrides above — nginx picks the more specific `location` first, so this only catches unlisted paths. |
| `/.well-known/security.txt`, `/.well-known/mta-sts.txt`, `/security.txt` | Hugo-static | Proxied to hugo-vm:80, unmodified. |
| `/sitemap.xml` (and anything matching `sitemap\.xml$`) | Hugo-static | Proxied to hugo-vm:80, with `Link:` headers pointing at llms.txt / oauth-protected-resource added at the gateway. |
| `/robots.txt` | Hugo-static | Same pattern as sitemap.xml. |
| `/` (catch-all, everything else) | Hugo-static | Proxied to hugo-vm:80 — this is where actual site pages/posts are served from `hugo-site/public/`. |
| static assets (`*.css`, `*.js`, images, fonts) | Hugo-static | Matched by extension, proxied to hugo-vm:80 with long-lived caching. |

## Practical rule of thumb

- Changing a **tool's behavior, an OAuth/MCP JSON document's *content*, or
  adding a new `/.well-known/...` or `/agent/...` endpoint** → edit this
  repo (`internal/server/*.go`), redeploy via `deploy.yml`, restart the
  systemd service on hugo-vm.
- Changing **page content, post text, site data/config (`data/*.yaml`),
  or theme layout** → edit the `hugo-site` repo, then `build_site` via
  this repo's MCP tool (never `hugo` by hand as `jm` — `public/` is
  owned by the `mcp-hugo-server-go` service account once `build_site`
  has run at least once; see `check-sri-versions.sh`'s own workaround
  of running as `sudo -u mcp-hugo-server-go`).
- Changing **which host serves a path, redirect targets, hardcoded
  404s/410s, or response headers/content-type overrides that aren't
  coming from Hugo or the Go server at all** → edit
  `/etc/nginx/sites-enabled/www.arleo.eu` or `.../mcp.arleo.eu` on the
  NUC directly (never `sites-available` for these two hosts — see the
  gotcha above). Not tracked in any git repo; back up by hand before
  editing.

## SRI/CDN version cron (hugo-vm)

`/home/jm/scripts/check-sri-versions.sh` (weekly, `0 8 * * 1` via
`crontab -l` as `jm` on hugo-vm) checks `hugo-site/data/sri.yaml`
(pinned SRI hashes) and `hugo-site/assets/data/cdn/jsdelivr.yml`
(pinned CDN versions) against live jsDelivr metadata:

- **Hash mismatch** or **major version bump** → BetterStack incident,
  never auto-fixed (security-sensitive / breaking-change judgment calls
  need a human).
- **Minor/patch bump** → auto-fixed: bumps the pin, re-fetches the SRI
  hash, rebuilds (`hugo --minify --cleanDestinationDir` as
  `mcp-hugo-server-go`, bypassing this repo's MCP build pipeline
  entirely — it writes straight to `public/`), deploys, purges
  Cloudflare, re-verifies the live hash, and — as of 2026-08-22 — commits
  and pushes the `sri.yaml`/`jsdelivr.yml` diff to the `hugo-arleo.eu`
  mirror repo. The commit/push only fires after a fully-verified
  success; a mid-fix failure rolls the files back first and no commit
  ever happens for a rollback. If a fix succeeds but the git push itself
  fails, the deploy is *not* rolled back (it's already live) — instead a
  distinct warning is raised (`git commit/push failed ... repo out of
  sync`) so the drift gets noticed without pretending the site update
  failed.
- Rebuild only ever runs when there's an actual version bump to apply —
  a clean check (`OK` on every lib) never touches `public/`, never
  redeploys, never pings anything except (on success) the heartbeat.

This bypasses this repo's own `build_site` MCP tool and its callback
pipeline (`db_reindex`, `search_index_submit`, `index_reload`, etc.) —
those still only run on the next real `build_site` call. In practice this
has been harmless so far (the raw `hugo` output is what nginx serves
either way), but it does mean the MCP server's own build-state tracking
(`source_revision`/`output_revision`) can be behind what's actually live
until the next `build_site` run notices the tree changed underneath it.
