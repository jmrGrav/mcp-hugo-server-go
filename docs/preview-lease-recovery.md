# Preview lease recovery

Preview metadata may survive restart, but preview bearer and session tokens
must not be copied into the derived content-state SQLite database.

## Persisted facts

- preview ID;
- owner/principal fingerprint;
- directory identity or safe relative path;
- creation/expiry timestamps;
- build status and lease state.

## Restart reconciliation

At startup, validate each lease against its configured preview root. Expired,
missing, or unsafe-path records are revoked and their directories are removed
only after the path guard validates ownership. Valid records are restored as
metadata but require a fresh authenticated preview session: restart never
resurrects an old URL bearer token or cookie.

## Invariants

- Tokens remain process-memory secrets and are invalidated by restart.
- `revoke_preview` and owner-scoped `revoke_all_previews` remove lease state
  before filesystem cleanup.
- Metadata persistence cannot grant cross-principal access.
- Filesystem discovery is advisory; the database is never trusted to delete
  an unchecked absolute path.
