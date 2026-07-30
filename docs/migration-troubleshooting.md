# Database migration troubleshooting

This guide is linked from the system info page when WeKnora's startup database
migration fails. It covers the most common causes, how to diagnose them, and
how to recover without losing data.

If none of these match your situation, jump to
[Reporting an issue](#reporting-an-issue) at the bottom.

---

## What "migration failed" means

WeKnora auto-runs `golang-migrate` migrations on every startup. MySQL migration
failure is fail-closed: the backend does not finish starting, because MySQL DDL
may already have committed and serving against a partially upgraded schema is
unsafe. PostgreSQL and SQLite retain the existing best-effort startup behavior:
the failure is logged and cached for system diagnostics while startup continues.

PostgreSQL and SQLite may roll back transactional migration statements. Never
assume that MySQL objects from a failing migration were rolled back.

The app container's startup error containing `database migration failed`
(capitalization and outer error wrapping may vary) is the authoritative source.
Copy the log and inspect the live schema before changing the recorded migration
version.

---

## Common causes

### 1. Missing PostgreSQL extension

Many migrations require extensions (`pg_trgm`, `vector`, `pg_search`) created
by `CREATE EXTENSION IF NOT EXISTS`. **`IF NOT EXISTS` does not validate that
the extension is actually installed** — it only checks the catalog. If the
extension's shared library is missing or the role lacks `CREATE` privilege,
the statement may succeed in the migration that nominally creates it but a
later migration that uses the extension (e.g. building a `gin_trgm_ops` index)
will fail.

**Symptoms in the error**:

```
ERROR: operator class "gin_trgm_ops" does not exist for access method "gin"
ERROR: type "vector" does not exist
ERROR: function ... does not exist
```

**Fix**:

```sql
-- Connect as a superuser (typically `postgres`):
\c your_weknora_database
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS vector;       -- if RETRIEVE_DRIVER includes pgvector
CREATE EXTENSION IF NOT EXISTS pg_search;    -- only on ParadeDB

-- Verify they are actually loaded:
SELECT extname, extversion FROM pg_extension WHERE extname IN ('pg_trgm','vector','pg_search');
```

Then restart WeKnora. The next startup will pick up where the failing
migration left off.

If `CREATE EXTENSION` itself errors with **"could not open extension control
file"** or **"permission denied"**, the extension is not installed on your
PostgreSQL server — install the corresponding OS package (e.g.
`postgresql-contrib` for `pg_trgm`) or switch to an image that ships it
preinstalled, then retry.

### 2. Dirty migration state

If a migration crashed partway through (OOM, container kill, network blip)
`golang-migrate` marks the schema as "dirty" at the failing version. MySQL
always stops startup and requires the partial migration to be inspected before
its recorded version is changed:

```
database is in dirty state at version N. ...
```

**Fix** — use the bundled helpers:

```bash
# Check the recorded version
make migrate-version

# Force the version to the last successful migration (N - 1 in the message)
make migrate-force version=<N-1>

# Re-run pending migrations
make migrate-up
```

After that, restart WeKnora. PostgreSQL and SQLite preserve the historical
`AUTO_RECOVER_DIRTY=true` default. MySQL ignores that setting and never performs
automatic force/retry because its DDL may already have committed; inspect and
repair the partial schema before using `migrate-force`.

### 3. Insufficient privileges on the database role

Some migrations create extensions or alter shared catalogs, which require
either superuser or `CREATEROLE` / `CREATEDB`. Errors look like:

```
ERROR: permission denied to create extension "pg_trgm"
ERROR: must be owner of database ...
```

**Fix**: grant the role used by `DB_USER` the necessary privileges, or
pre-create the extensions / objects as a superuser ahead of time, then
restart. The migration's `CREATE EXTENSION IF NOT EXISTS` will then no-op.

### 4. Out-of-disk during `CREATE INDEX`

GIN / pgvector indexes can require significant temporary space. Errors:

```
ERROR: could not extend file ...: No space left on device
ERROR: cannot create temporary tables in transaction
```

**Fix**: free disk on the volume backing `PGDATA`, then restart. The
migration will retry the index build.

### 5. Schema drift from manual edits

If you previously edited tables / columns by hand and a later migration
expects the original shape, it will fail with mismatched-type errors. The
safest recovery is to align the live schema with the previous successful
migration's `*.up.sql` and then re-run pending migrations.

---

## Generic diagnostic checklist

1. **Read the full error**: the cached message in the UI is truncated only by
   your browser scroll — it is the complete `golang-migrate` error. The
   container log shows the same content with stack context.
2. **Identify the failing migration**: the version number in the error (or
   `make migrate-version`) points to `migrations/versioned/` for PostgreSQL,
   `migrations/mysql/` for MySQL, or `migrations/sqlite/` for SQLite. Open the
   matching `*.up.sql` and find the failing statement.
3. **Run the failing statement manually** with the database's native client
   (`psql`, `mysql`, or `sqlite3`). The server error is usually more specific
   than the migration wrapper's.
4. **Fix the underlying cause** (install extension, fix privileges, free
   disk, repair partial MySQL DDL, …), then run `make migrate-up` from a
   checkout or restart WeKnora to retry a clean, non-dirty version. Do not
   enable automatic dirty recovery until the live schema is known to match the
   version you force.
5. **Verify**: the backend becomes healthy, the recorded DB version reaches the
   expected head, and the previously blocked feature (Wiki, KG, task queue, …)
   works.

---

## Reporting an issue

If you've worked through the checklist and the migration still fails, please
open an issue at:

<https://github.com/Tencent/WeKnora/issues/new?template=bug_report.yml>

Include:

- WeKnora version + commit ID (from the system info page).
- The full error from the system info page (or container logs).
- Database driver and server version (`SELECT version();`) and how it was
  deployed (MySQL, Percona Server, PostgreSQL, ParadeDB, Aurora, Aliyun RDS, …).
- The output of:
  ```sql
  -- PostgreSQL only
  SELECT extname, extversion FROM pg_extension;
  ```
- Any non-default values of `RETRIEVE_DRIVER`, `AUTO_MIGRATE`, and
  `AUTO_RECOVER_DIRTY`.

If the backend can start after recovery, the "Report issue" link on the system
info page can pre-fill diagnostic context.
