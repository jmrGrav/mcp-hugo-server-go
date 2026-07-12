# Soak testing

The local soak harness exists to exercise repeated MCP mutation and build flows
for longer periods than ordinary unit tests.

## What it covers

- repeated `create_page` / `update_page` / `delete_page`
- periodic `build_site`
- periodic `preview_build`
- mixed read calls (`get_page`, `list_pages`) during the run
- optional derived DB mode through `SOAK_WITH_DB=1`

## Safety

The harness is local-only by default:

- it creates temporary Hugo roots
- it uses an in-memory MCP transport
- it uses a mock `hugo` binary
- it never targets production or staging

## Run

```bash
make soak-local
```

Optional environment overrides:

```bash
SOAK_DURATION=2m
SOAK_CONCURRENCY=8
SOAK_WITH_DB=1
SOAK_SUMMARY_PATH=/tmp/mcp-hugo-soak.json
make soak-local
```

## CI policy

The soak harness is intentionally excluded from default fast CI.

- default CI: compile/run the regular unit, race, vet, and static checks
- manual or scheduled robustness runs: `make soak-local`

## Summary artifact

The harness writes a compact JSON summary containing:

- per-tool success counts
- per-tool error counts
- per-tool latency samples
- goroutine start/end counts
- heap allocation start/end
- invariant failure list

The run fails if a checked invariant breaks.
