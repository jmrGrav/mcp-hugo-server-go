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
