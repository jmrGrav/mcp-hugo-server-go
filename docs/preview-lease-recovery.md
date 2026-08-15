# Preview lease recovery

Preview metadata may survive restart, but preview bearer and session tokens
must not be copied into the derived content-state SQLite database.

When `db_path` is configured, preview files live under the dedicated sibling
directory `<db_path>.previews`. Reconciliation never scans or removes arbitrary
entries from the process-wide temporary directory.

## Persisted facts

- preview ID;
- owner/principal fingerprint;
- directory identity or safe relative path;
- creation/expiry timestamps;
- build status and lease state.

## Restart reconciliation

At startup, validate each lease against its configured preview root. Expired,
missing, or unsafe-path records are revoked and their directories are removed
only after the managed-root containment guard validates the directory. Valid
records are restored as
metadata with `access_status: restart_invalidated`: restart never resurrects an
old URL bearer token or cookie. The owner can still inspect through MCP or
revoke the recovered lease, and creates a new preview for browser access.

## Invariants

- Tokens remain process-memory secrets and are invalidated by restart.
- `revoke_preview` and owner-scoped `revoke_all_previews` remove lease state
  before filesystem cleanup.
- Metadata persistence cannot grant cross-principal access.
- Filesystem discovery is advisory; the database is never trusted to delete
  an unchecked absolute path.
