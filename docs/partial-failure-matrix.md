# Partial-Failure Matrix

This document is the design and operator reference for issue `#372`.

It defines when `mcp-hugo-server-go` should return:

- a hard failure;
- `status: "ok"`;
- `status: "partial_success"` plus a warning.

## Primary rule

If the **primary operation** did not commit, return a hard error.

If the primary operation **did commit**, but one or more **derived follow-up
steps** failed, return a usable result with:

- `status: "partial_success"`
- a non-empty `warning`
- a lifecycle `state` that reflects the degraded downstream reality

The server must not claim a clean success when the committed primary state and
the derived/read/public state diverge.

## Classification matrix

| Tool family | Primary operation | Blocking failures | Downgraded follow-up failures |
| --- | --- | --- | --- |
| `create_page` | source file created and source index updated | path validation, write failure, frontmatter validation failure, content lock timeout | derived DB sync failure |
| `update_page` | source file updated and source index updated | path validation, read failure, write failure, frontmatter validation failure, content lock timeout | derived DB sync failure |
| `delete_page` | source directory removed and source index updated | path validation, delete failure, content lock timeout, rate-limit rejection, not-found | public output cleanup failure, derived DB cleanup failure, audit log append failure |
| `build_site` | Hugo build completed successfully | Hugo process failure, timeout, preflight failure | post-build callback timeout/failure, output-state hashing failure, later index/public verification failures |

## Lifecycle-state rule

When a follow-up step fails after commit, `state` must describe the degraded
view rather than pretending everything is fresh:

- `public_state: stale` when public cleanup or publication refresh failed
- `index_state: stale` when a derived DB/index update failed
- `build_state: pending` when source changed but public output is still behind

## Non-goals

This issue does not add rollback or multi-step transactions. It only defines
how already-committed operations report degraded follow-up work.

Those broader workflows belong to later issues such as `#340` and `#346`.
