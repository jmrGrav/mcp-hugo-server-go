# Gemini Audit Report — Issues #24–#43

Audit performed by Gemini, triaged and fixed by Claude Code (claude-sonnet-4-6).
Validation: `go test ./...`, `go test -race ./...`, `go vet ./...` — all green.

## Summary

| # | Title (fr) | Decision | Commit | GH Status |
|---|------------|----------|--------|-----------|
| #24 | Les scopes content.write/site.admin/system.admin sont impossibles à atteindre | VALID — FIXED | `8e55d2d` | CLOSED |
| #25 | L'enforcement des scopes dépend du serveur choisi, pas du tool appelé | VALID — FIXED | `8e55d2d` | CLOSED |
| #26 | Les clients OAuth, codes et assertions Agent Auth disparaissent au redémarrage | VALID — FIXED | `8e55d2d` | CLOSED |
| #27 | Agent Auth délivre une assertion échangeable sans preuve de claim utilisateur | VALID — FIXED | `8e55d2d` | CLOSED |
| #28 | Les scopes OAuth annoncés et les scopes réellement émis divergent | VALID — FIXED | `8e55d2d` | CLOSED |
| #29 | L'ACL anonyme duplique manuellement la liste des tools au lieu de dériver du registre MCP | VALID — FIXED | `8e55d2d` | CLOSED |
| #30 | Dynamic client registration et Agent Auth ne sont pas rate-limités | VALID — FIXED | `72696a8` | CLOSED |
| #31 | `max_request_bytes` est ignoré sur `/mcp` et les endpoints OAuth | VALID — FIXED | `72696a8` | CLOSED |
| #32 | Le serveur HTTP n'a pas de ReadTimeout/WriteTimeout/IdleTimeout ni de shutdown borné | VALID — FIXED | `8e55d2d` | CLOSED |
| #33 | PathGuard ne protège pas les écritures contre les symlinks parents et le TOCTOU | VALID — FIXED | `8e55d2d` | CLOSED |
| #34 | Les écritures atomiques utilisent un nom `.tmp` prévisible sans O_EXCL, fsync ni cleanup | VALID — FIXED | `8e55d2d` | CLOSED |
| #35 | Les mutations Hugo utilisent un SourceIndex figé au démarrage | VALID — FIXED | `8e55d2d` | CLOSED |
| #36 | Les mutations de contenu, images et build ne partagent aucun verrou de cohérence | VALID — FIXED | `8e55d2d` | CLOSED |
| #37 | La validation des slugs est incohérente et ne protège pas des collisions case/unicode | PARTIAL — DEFER | — | OPEN (commented) |
| #38 | `generate_featured_image` lit une réponse non bornée et ne vérifie pas le statut HTTP | VALID — FIXED | `72696a8` | CLOSED |
| #39 | Les post-build hooks annoncent une protection SSRF sans validation d'URL au chargement | VALID — FIXED | `72696a8` | CLOSED |
| #40 | Le checker SRI utilise un parser regex fragile et fetch des URLs de templates sans allowlist | VALID — DEFER | — | OPEN (commented) |
| #41 | La configuration ne valide pas les invariants requis par le mode activé | VALID — FIXED | `72696a8` | CLOSED |
| #42 | Les index Hugo ignorent silencieusement les collisions de slugs | VALID — FIXED | `72696a8` | CLOSED |
| #43 | L'unité systemd est incompatible avec les outils write/admin annoncés | VALID — FIXED | `72696a8` | CLOSED |

## Fix Details

### Commit `8e55d2d` — P1 Mandatory

- **#24 / #26**: Five scoped MCP servers wired in `server.New()`: anonServer → readServer → writeServer → siteAdminServer → sysAdminServer. Routing by `tools.ScopeRank(scope)` from validated bearer.
- **#25 / #29**: `ScopePolicy` rewritten to be backed by `*tools.Registry`. `AllowRequest` reads the JSON body, extracts tool name, calls `reg.RequiredScopeFor(name)`, compares via `tools.ScopeRank`. Single source of truth for tools/list and tools/call.
- **#27**: `Claimed bool` field on `agentRegistration`. `exchangeAgentAssertion` returns `invalid_grant: claim_required` when unclaimed.
- **#28**: `tools.KnownScopes` is the single source for `scopes_supported` in discovery, OAuth metadata, and registration responses. Removed all hardcoded `"mcp"` scope references.
- **#32**: HTTP server: `ReadHeaderTimeout=5s`, `ReadTimeout=30s`, `WriteTimeout=60s`, `IdleTimeout=120s`. Bounded shutdown via `context.WithTimeout(ctx, 15s)`.
- **#33**: `rejectSymlinkComponents(path)` walks every ancestor from root using `os.Lstat`, rejects any symlink component. Error messages contain no raw filesystem paths.
- **#34**: `atomicWrite` and `atomicWriteBytes` use `os.CreateTemp(dir, ".mcp-write-*.tmp")` for a random non-predictable temp name. `defer os.Remove` cleans up on error before rename.
- **#35**: `SourceIndex.Upsert(page)` and `SourceIndex.Delete(slug)` keep the in-memory index live. All write tools call these after successful filesystem operations.
- **#36**: `hugosite.ContentMu sync.RWMutex` is the shared coherence lock. Write tools call `Lock()`, build calls `TryLock()` and returns `build_in_progress` if already held.

### Commit `72696a8` — P2

- **#30**: Per-IP counter (`oauthIPCounts`, max 100) on `/register` and `/agent/identity`. Returns HTTP 429 on excess.
- **#31**: `http.MaxBytesReader` applied in `rateLimitOAuth` closure on allocation endpoints. `/mcp` body limited via `io.LimitReader(r.Body, maxBody)`.
- **#38**: `fetchImage()` checks HTTP status code (rejects non-2xx) and reads at most `maxImageBytes = 10 MiB` via `io.LimitReader`.
- **#39**: `config.validate()` calls `validateHookURL()` on all `PostBuildHooks` and `ImageGenURL`. Rejects: non-http(s) schemes, private IPs (RFC 1918), loopback, link-local (169.254.x.x).
- **#41**: `config.validate()` enforces cross-field invariants: `oauth.enabled` without `oauth.issuer` is an error at load time.
- **#42**: `SourceIndex` and `site.Index` emit `slog.Warn` on duplicate slug detection during initial load.
- **#43**: systemd unit updated: `ProtectSystem=full`, `ProtectHome=true`, `ReadWritePaths=/var/lib/mcp-hugo-server-go /srv/hugo-site`.

## Deferred Issues

### #37 — Slug validation inconsistency

Slug collision detection now logs warnings (#42 fix covers runtime duplicates). However, a shared `Slug` value type with Unicode normalization, case-insensitive reserved-name checking, and cross-tool consistent validation was not implemented. Deferred post-v1.0.0 — requires a migration/audit mode for existing content.

### #40 — SRI regex fragility and fetch allowlist

`check_sri_versions` is a `system.admin` tool used by trusted operators on their own layouts. The current regex parser does not support single-quoted attributes or distant attribute ordering. An allowlist for CDN domains and an HTML/AST-based parser would improve robustness but are not blocking for v1.0.0 given the required scope. Deferred post-v1.0.0.
