# Preview Workflow

This document is the design anchor for issue `#345`.

## Why not just run a Hugo process and hand back a PID?

A remote MCP server has no "local browser" for an agent to point at a
`localhost:1313` preview process, and a raw PID is not something a remote
caller can usefully act on (kill it? poll it? it isn't even addressable over
HTTP). `create_preview` instead builds real files to an isolated directory
and exposes them at a temporary, token-gated URL the agent (or the human
behind it) can open directly.

## What `create_preview` does

1. Runs `hugo --destination <isolated-temp-dir>` against the current source
   tree (optionally with `--buildDrafts` when `include_drafts: true`) — never
   `cfg.SiteRoot`. The preview build and the public site are always separate
   directories; nothing this tool does can affect what the public site is
   currently serving.
2. Generates two random identifiers via `crypto/rand`: an opaque
   `preview_id` (not a PID, not a slug) and a `token` that is the sole
   confidentiality boundary for the preview's content (which may include
   unpublished drafts).
3. Registers `{preview_id -> {token, dir, expires_at}}` in an in-memory
   store (`internal/previewstore`).
4. Returns `preview_id`, a URL of the form
   `{issuer}/preview/{preview_id}/{token}/`, an `expires_at` timestamp, and
   the build result.

When `preview_external_verification: true`, the tool does not trust a build or
an HTTP 200 alone. Before returning the entry URL it exercises the public
`oauth.issuer` ingress with a temporary cookie session and checks:

- the signed entry URL redirects to the exact nested page route;
- the nested page bytes match the isolated `index.html`;
- an asset's bytes match the isolated file;
- an intentionally missing route returns 404 rather than the home page.

The probe session is reset before the URL is returned, so the caller can still
exchange the entry token normally. Any failed check revokes the preview and
returns `preview_unreachable`.

## How the URL is served

The plain-HTTP handler in `internal/server` recognizes any request path
under `/preview/` and delegates to `previewstore.Store.HTTPHandler()`, which:

- Parses `{id}` and `{token}` from the path.
- Rejects the request (`404`) if the id is unknown, the token doesn't match
  (constant-time compare — content may include drafts, so this is a real
  confidentiality boundary, not just a lookup key), or the entry has expired.
- Serves the matched directory via `http.FileServer(http.Dir(...))` wrapped
  in `http.StripPrefix` — path traversal is handled by the standard library,
  not by hand-joining the request path.
- Sets `X-Robots-Tag: noindex, nofollow` on every response, so even if a
  preview URL leaks, crawlers won't index it.

This route is intentionally **not** behind the OAuth bearer-token gate that
protects `/mcp` — the token embedded in the URL path is the preview's own,
purpose-built gate. Requiring an OAuth bearer token as well would defeat the
point of handing an agent (or a human) a single clickable link.

The reverse proxy must forward `/preview/` paths unchanged to the MCP service.
Do not configure `try_files ... /index.html` or another SPA fallback on this
location: missing preview routes must remain 404.

## TTL and cleanup

- `ttl_seconds` is clamped to `[60, 3600]`; the default is `900` (15
  minutes). A preview is a disposable, short-lived surface by design — there
  is no "make it permanent" option here (see `verify_publication` /
  `inspect_rendered` for the durable, no-TTL diagnostics).
- Expiry is enforced on **every access**, not only at creation time: `Get`
  checks `time.Now().After(entry.ExpiresAt)` before serving, deletes the
  entry, and removes its directory.
- `create_preview` also sweeps every already-expired entry before
  registering a new one, so disk usage from abandoned previews doesn't grow
  unbounded even if nobody ever revisits an expired link.
- With `db_path` configured, owner/TTL/build metadata and the dedicated
  `<db_path>.previews` directory survive restart. Startup reconciles missing,
  expired, unsafe, and orphaned entries. Bearer URL tokens and session cookies
  intentionally remain memory-only and are invalid after every restart.
- Without `db_path`, the registry remains process-local and restart drops the
  active leases, matching the previous fallback behaviour.

## Non-goals

- Persisting bearer URL tokens or session cookies. Only non-secret lease
  metadata is restart-safe.
- A background sweeper goroutine — lazy sweep-on-create plus
  expire-on-access is sufficient for the expected preview volume and avoids
  adding a long-running goroutine to the server's lifecycle.
- Feeding directly into a publish/rollback workflow (#340, a later
  milestone) — `create_preview` is meant to compose with that work later,
  not to implement it now.
