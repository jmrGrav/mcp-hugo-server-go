# Security Audit Trail

This document is the design anchor for issue `#371`.

It describes the structured audit event stream this server emits for
authentication/authorization/mutation/admin events, and what an operator
should expect from it — without introducing a separate logging stack.

## Design principle: reuse the existing structured logging pipeline

There is no separate audit log file, database, or agent. Every audit event
is emitted through the same process-wide `log/slog` default logger that
already backs every other structured log line in this server
(`internal/observability.NewLogger()`, a JSON handler writing to `stderr`).
Wherever the operator already collects this server's stderr — `journalctl
-u mcp-hugo-server-go`, a log-shipping agent watching the systemd journal,
or a redirected file — audit events are already there, mixed in with
ordinary request/tool-call logs but mechanically distinguishable (see
below).

`internal/audit` is a thin, deliberately minimal package: it does not open
files, does not buffer, does not run a background worker. It exists only to
pin down a consistent vocabulary (`event_type`, `result`) so operators don't
have to reverse-engineer free-text messages to build alerts.

## Event shape

Audit events are emitted through two log lines, not one — `event_type` is
the field that unifies them, not `msg`:

- `auth_rejected`, `scope_denied`, and `operator_milestone` are emitted as
  their own `"msg":"audit"` line by `internal/audit`.
- `mutation` and `admin_operation` are tagged onto the existing per-call
  `"msg":"tool_call"` line (see `internal/observability`) rather than a
  duplicate log entry, since one line per tool call is already emitted for
  metrics/latency purposes.

An operator building a filter for "all security-audit events" should filter
on **presence of `event_type`**, not on `msg`. Every audit event, regardless
of which line it's attached to, carries:

| Field | Meaning |
| --- | --- |
| `event_type` | One of the five values below. Always present. |
| `result` | Outcome, e.g. `"denied"`, `"success"`, `"claim_approved"`, `"revision_conflict"`. Always present on every `event_type`-tagged line, including `mutation`/`admin_operation` (there it mirrors `result_class`: `"success"`, `"tool_error"`, `"protocol_error"`). |
| (event-specific attrs) | See below — never a raw bearer token, never an absolute host filesystem path. |

### `event_type` values

- **`auth_rejected`** — a request was rejected before any scope was
  established: missing bearer token, malformed `Authorization` header, or
  a token that failed validation. Emitted by the `/mcp` HTTP entry point
  (`internal/server`) and by the agent-claim-approval endpoint
  (`internal/oauth`).
- **`scope_denied`** — a request carried a *validated* token, but that
  token's scope was insufficient for the requested tool/operation. This is
  mechanically distinct from `auth_rejected` (bad/no credential) and from an
  ordinary `tool_error` (a tool ran and failed on its own terms) — an
  operator alerting on repeated `scope_denied` events is looking for a
  misconfigured or probing client, not a broken tool.
- **`operator_milestone`** — reader self-registration and the
  operator-approval claim flow: `pending_operator_claim` (anonymous
  registration awaiting approval), `reader_self_registered` (auto-approved
  when `allow_reader_self_registration` is enabled), `claim_approved` (an
  operator approved a pending claim), `claim_failed` (invalid/expired claim
  token, or an operator token with insufficient scope attempted approval).
- **`mutation`** — a `content.write` tool call outcome (`create_page`,
  `update_page`, `delete_page`). Tagged onto the existing per-call
  `tool_call` log line (see `internal/observability`) rather than a
  duplicate log entry.
- **`admin_operation`** — a `site.admin` tool call outcome (`build_site`,
  `preview_build`, `run_post_build_hooks`, `generate_featured_image`,
  `check_sri_versions`, `get_runtime_status`, `get_theme_status`). Same
  tagging mechanism as `mutation`. Also carries `"degraded":true` when a
  successful call's structured result reports a `partial_success` status
  (e.g. `build_site` succeeding with a failed post-build callback) — this
  is the "degraded admin operation" the issue's scope explicitly asks for.

`content.read`/anonymous tool calls are **not** tagged with an `event_type`
— they still produce the existing `tool_call` log line (tool name, scope,
duration, result class), but are not treated as security-audit events. This
matches the issue's own non-goal: "no requirement to log every successful
token validation at high volume." Read traffic on a public MCP server can be
frequent; tagging every read as an "audit" event would bury the
security-relevant signal in noise.

## What is deliberately never logged

- Raw bearer tokens or any other secret. Only `scope` (a coarse tier name
  like `content.write`) is logged, never the token value.
- Absolute host filesystem paths. `target`-shaped fields are logical
  identifiers (a slug, a tool name), consistent with the rest of this
  server's response contract (see `docs/mcp-contract.md`).
- Full request/response bodies or page content.

## Correlation

`request_id`/`operation_id` style correlation, where available, comes from
fields already present on the surrounding log line rather than a new ID
scheme: the HTTP `Mcp-Session-Id` header (present on `auth_rejected` events)
and per-operation identifiers already emitted by specific tools (e.g.
`build_site`'s `build_id`). This server does not (yet) generate a
request-scoped correlation ID for every single call; introducing one is a
reasonable future enhancement but out of scope here — see "Non-goals".

## Retention

This server does not manage log retention itself. Retention is whatever the
operator's existing `stderr`/journald configuration already provides (e.g.
`journalctl` vacuum settings, or a forwarding log-shipping agent's own
retention policy). Operators who need long-term security audit retention
should point their existing log collection at this process's `stderr`, the
same way they already collect ordinary request logs — no new collection
path is introduced by this feature.

## Non-goals

- A dedicated audit log file, database, or write-ahead log — deliberately
  avoided per the issue's own guidance to reuse existing observability
  patterns.
- A new request-scoped correlation ID generated for every call. Today's
  correlation is opportunistic (session ID, per-operation IDs already
  emitted by individual tools).
- Log retention/rotation policy enforcement — left to the operator's
  existing log collection, as described above.
- Emitting an audit event for every successful read-tool call — explicitly
  out of scope per the issue text ("no requirement to log every successful
  token validation at high volume").
