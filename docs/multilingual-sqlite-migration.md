# Multilingual SQLite migration

## Decision

The current derived `pages` index uses `slug UNIQUE`. It must not become more
authoritative until source and public representations have an explicit,
language-aware identity.

The target logical key is:

```
source_key + lang + representation
```

where `representation` is `source` or `public`. `source_key` is the Hugo
bundle identity, not a URL-shaped language-prefixed slug.

## Safe rollout

1. Add a new versioned table beside `pages`; do not mutate the current table
   in place.
2. Backfill from bytes read from disk and both existing indexes.
3. Run old and new indexes in shadow comparison for one release, recording
   only aggregate mismatch diagnostics.
4. Switch readers only after zero unexpected multilingual mismatches.
5. Retain the old table for one rollback release, then remove it in a later
   migration.

## Operational lifecycle

This database is the derived operational-state database at `db_path`; it is
not the OAuth token database. Both may use SQLite/WAL, but they have separate
files, backups, access permissions and retention policies. A loss of this
database must never make source content unreadable or make public output
unservable.

- Open with WAL and foreign keys enabled, then run versioned migrations inside
  one SQLite transaction. A failed migration leaves the preceding schema
  intact and causes the server to start in its existing filesystem-backed
  mode rather than treating a partial derived index as authoritative.
- Back up the database with SQLite's online backup API (or a consistent
  `VACUUM INTO` snapshot), including WAL state; do not copy only the main file
  while the service is running. Backups are operational metadata, never a
  substitute for Git/source backups.
- On a corrupt or unreadable derived database, preserve the failed file for
  operator inspection, create a fresh database, then rescan source and public
  bytes under the normal content lock. Report degraded derived-state health
  until the scan completes; do not delete or rewrite Hugo content as recovery.
- Rollback means selecting the previous application schema and reconstructing
  the derived tables from disk. It does not mean replaying stale database
  content over Markdown or `public/`.

## Schema and migration contract

The versioned table must use an explicit composite identity, for example:

```sql
CREATE TABLE content_representations (
  source_key TEXT NOT NULL,
  lang TEXT NOT NULL,
  representation TEXT NOT NULL CHECK (representation IN ('source', 'public')),
  revision TEXT NOT NULL,
  observed_at TEXT NOT NULL,
  PRIMARY KEY (source_key, lang, representation)
);
```

`source_key` is derived from the resolved source path; it is never inferred
from a public URL. A legacy public row that cannot yet be correlated is placed
under an opaque `@public/<digest>` key and counted as a counterpart gap; it is
never presented as a source identity. Startup reconciliation resolves public
rows against the source index, including explicit frontmatter `url`/`permalink`
values, and refuses ambiguous guesses. Unlabelled `index.md` sources are
canonicalized to the configured default language during that reconciliation.
Backfill must read the bytes for each source and public
representation under the content lock, calculate their revisions, and record
only the facts observed at that time. It must not copy the old `pages.slug`
uniqueness into the new table.

## Shadow comparison and cutover gates

During one release, the legacy index remains the serving index. The new table
is refreshed from the same disk scan and produces aggregate diagnostics only:
row count by representation/language, missing counterpart count, and a
hashed mismatch key suitable for logs. Diagnostics must contain no page body,
token, or absolute host path.

Cutover requires all of the following:

1. a clean backfill on a multilingual fixture;
2. no unexplained shadow mismatches through a full build/restart cycle;
3. a migration rollback drill that reconstructs derived state from disk;
4. unchanged authorization and caller isolation for plans, snapshots and
   mutation journals.

## Invariants

- Markdown/Git remains source truth; SQLite is derived state.
- A source revision is a hash of bytes read under the content lock, never a
  hash trusted solely from an earlier database row.
- Source and public rows may coexist for the same source key and language.
- Cross-language fallback must be explicit in the resolver, never implicit in
  a SQL uniqueness conflict.
- OAuth storage remains separate from this derived content-state database.

## Verification fixtures

The migration needs fixtures for default-language and secondary-language leaf
bundles, translation-only deletion, an external source edit while the process
is stopped, and source/public representation drift after a completed build.
It also needs a corrupt-db recovery fixture, a WAL-consistent backup/restore
drill, and a shadow comparison assertion proving French and English rows never
collide when their public slugs are similar.
