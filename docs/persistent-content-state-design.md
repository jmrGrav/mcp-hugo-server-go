# Persistent content-state design

This document records the design boundary and rollout for #1074. It does not
authorize replacing Hugo content, Git, or the public output tree with a
database.

## Authority model

| Data | Authority | Notes |
| --- | --- | --- |
| Source content | Markdown/Git on disk | Revisions are hashes of bytes read under the content lock. |
| Published output | `public/` on disk | It represents what Hugo actually rendered. |
| Server operations | Operational SQLite state | Builds, observations, journals, leases, and retention metadata. |
| Read indexes | In-memory caches | They are fully reconstructible and never the final authority. |

OAuth secrets and tokens use a separate storage lifecycle from operational
content-state data. Backups, retention, deletion, and recovery must not couple
the two domains.

## Durable facts, not flags

`BuildPending` is useful local bookkeeping but is not durable evidence that
source is unpublished. Durable records use comparable facts:

```
source_key, lang, representation
source_revision, last_built_source_revision, public_revision
build_id, publication_state, observed_at
```

The identity is multilingual by construction. A slug-only unique key is not
sufficient: it can collapse translations and recreate language-isolation bugs.
`representation` distinguishes source and public observations.

## Reconciliation

External SSH or Git writes do not update SQLite. File watchers may reduce
latency but are advisory: they can miss events. The server therefore performs
a deterministic source/public fingerprint scan at startup and after each build.

The reconciliation result reports facts rather than guessing from a dirty Git
worktree or a process-local flag. A full Hugo build can legitimately publish a
dirty worktree; dirty Git alone is not proof of stale public output.

The #1077 implementation stores one `build_runs` row and multilingual
`build_pages` rows for each attempted build. Before Hugo runs, `build_site`
reloads the source index from disk under the content lock and compares exact
source-byte revisions with the latest completed run. Its `pages.included`,
`pages.excluded_drafts`, and `pages.deleted_outputs` report that durable
comparison; `BuildPending` remains only a compatibility fallback when the
operational database is disabled. After the output swap and index reload, the
run records the executing Hugo version and the observed public revisions.

At startup and after every completed build, the server refreshes source and
public representations from disk and publishes aggregate-only reconciliation
facts through `get_runtime_status.data.build_reconciliation`. Per-page keys and
paths remain internal so runtime diagnostics do not expose site content.

## Filesystem and SQLite recovery

A filesystem rename and a SQLite transaction cannot be one atomic operation.
Every mutation or build that crosses this boundary needs a durable journal:

```
in_progress -> file_written -> committed
```

Startup recovery inspects unfinished journal entries, reads the filesystem under
the appropriate lock, and either finalizes the durable record or marks a
recoverable failure. It must never infer success solely from a database row.

## Rollout

1. Keep the immediate source/public reconciliation hotfix (#1071).
2. Implement restart-safe build/publication manifests (#1077).
3. Define and migrate multilingual operational identity (#1076).
4. Add mutation/idempotency journaling (#1078), recovery (#1079),
   plans/snapshots (#1080), and preview leases (#1081).
5. Shadow-compare cache and durable reconciliation for one release before
   moving additional readers to the durable view.

The rate limiter is deliberately out of scope. #962 concerned legacy tokens
without stable principals, not missing persistence for token buckets.

## Required verification

- restart after an external source edit;
- full build followed by source/public reconciliation;
- interrupted file/SQLite transitions;
- multilingual source/public identity;
- multi-principal ownership and idempotency isolation;
- WAL, backup, migration, corruption, and rollback recovery.

Historical incidents #212, #643, and #829 inform the regression suite, but do
not prove that SQLite itself fixes every logic error. #1066 is the immediate
behavioural anchor for this design.
