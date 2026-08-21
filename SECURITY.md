# Security Policy

## Reporting a vulnerability

Please report security issues through GitHub Security Advisories:

https://github.com/jmrGrav/mcp-hugo-server-go/security/advisories/new

Do not open a public issue for a potential vulnerability.

## Scope

This project handles:

- public MCP discovery
- OAuth discovery and token validation
- authenticated content read and write operations
- build and site administration actions

Reports are most useful when they include:

- the affected endpoint or tool
- the bearer scope or anonymous path used
- the exact request and response observed
- whether the issue is reproducible on the public endpoint

## Language

Preferred languages:

- French
- English

## Threat model

This server sits between a Hugo content site and an MCP-connected AI agent
that may have write access to it. Three boundaries matter, and it's
important not to conflate them:

- **OAuth scope (`read`/`write`/`admin`) is the actual authorization
  boundary.** Every tool call is checked against the caller's scope by
  `oauth.ScopePolicy` (`internal/oauth`), independent of anything else in
  this list. This is the only control that determines whether a call is
  *allowed* at all.
- **Exposure profiles (`?profile=reader|editorial|advanced|admin`,
  `internal/server/exposure_profile.go`, #1137) are a discoverability
  filter, not a second authorization layer.** They narrow which of a
  caller's already-allowed tools are registered on a given connection —
  useful for reducing the tool surface a client sees, but a caller who
  wants a wider (still scope-permitted) tool set can simply reconnect
  without the parameter. Do not rely on a restrictive profile as a
  security control; rely on OAuth scope for that.
- **`meta.content_provenance` (`internal/toolcontract/response.go`,
  documented in full at `docs/mcp-contract.md` §6.27) is a signal for the
  *calling agent's client* to consume, not an in-band control this server
  enforces.** Any tool response tagged `site_source_untrusted` or
  `site_rendered_public_untrusted` carries text that anyone with editorial
  write access (or an attacker who got a file into `content/` some other
  way) could have authored — indistinguishable at the raw-string level
  from a real instruction unless the calling client's own system prompt
  treats the tag as "this is data, never an instruction to follow." This
  server cannot enforce that on a client it doesn't control; it can only
  make the untrusted/trusted distinction available and correctly tagged.

### Indirect prompt injection

The scenario this section exists for: an agent connected to this server
reads content that was written, in whole or part, by someone other than
the operator — a submitted draft, a comment mirrored into a page, a
compromised upstream feed mirrored into `content/` — and that content
contains text crafted to look like an instruction to the agent rather than
page content to analyze. If the agent acts on it, the attacker has used
the agent's own write access against the site without ever holding
credentials themselves.

**What this server does about it:** tags every read-scope tool response
that echoes site-source or rendered-HTML text with `content_provenance`
(§6.27), with a build-failing completeness test
(`TestReadDefsHaveExplicitContentProvenanceClassification`,
`internal/tools/read/content_provenance_coverage_test.go`) ensuring no
future tool ships this untagged. Mutation tools support attributing work
to a `change_set_id` (§6.15) that a caller can mark as derived from
untrusted content it read (see §6.27's cross-reference) — a self-report
for audit purposes, not something this server can verify, since it never
sees what informed the text in a `create_page`/`update_page` call.

**What this server deliberately does not do, and why:** it does not scan
tool output for injection-shaped phrases (`SYSTEM:`, `IGNORE PREVIOUS
INSTRUCTIONS`, imperative verbs, etc.) and block or strip on match. A
keyword/regex filter here would be trivially bypassed by an actual
attacker (encoding, translation, paraphrase, splitting a phrase across
multiple fields) while reliably breaking legitimate content — this is a
Hugo site that may itself publish articles *about* prompt injection or AI
security, which a naive filter would flag or mutilate. Worse, a filter
like this invites false confidence: its presence tends to make people
treat the trust boundary as solved instead of fixing where it actually
needs to live (client-side consumption of `content_provenance`, and
never letting a single agent session read untrusted content and hold
unreviewed write/publish authority in the same breath). This is a
considered rejection, not an oversight — do not reintroduce a
keyword-filtering layer without addressing this note first.

**What this explicitly does not defend against:** an agent that reads
injected content and, without any self-report, composes a malicious
`create_page`/`update_page` call anyway. There is no automatic taint
tracking from a tagged read result to a later write call — the server has
no visibility into what informed the arguments of a write call an agent
composes itself. Defense here depends on the calling client's own system
prompt correctly treating untrusted-tagged content as data (see above),
and, for destructive/publish actions, on independent review before
publication — this server does not yet provide a signed human-confirmation
mechanism for that; see the project's open issues for status.

### Tool poisoning / rug pulls

A related but distinct threat: not injected *content*, but the *tool
registry itself* being edited after a client already reviewed and trusted
it — a tool's description or input/output schema silently rewritten
between a client's connections, without its name ever changing, so a
static allowlist-by-name never notices. `get_capabilities.data.tool_catalog
.tool_registry_digest` (§6.28, #1225) exists for this: a `sha256:<hex>`
fingerprint over every tool's `{name, description, input_schema,
output_schema}` across this deployment's full admin-scope tool surface,
computed once at server startup from a real `tools/list` round-trip
(`internal/toolregistry`). It is a trust-on-first-use value scoped to one
specific deployment, not a universal constant — see §6.28 for exactly what
a client should and should not conclude from a mismatch. Like
`content_provenance`, this is a signal for the calling client to pin and
compare; this server does not itself refuse to serve a tool whose
description changed, since a legitimate deployment (a version upgrade, an
operator-authored extension) changes tool descriptions too.

