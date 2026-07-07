# Database migration troubleshooting

This guide is linked from the system info page when WeKnora's startup database
migration fails. It covers the most common causes, how to diagnose them, and
how to recover without losing data.

If none of these match your situation, jump to
[Reporting an issue](#reporting-an-issue) at the bottom.

---

## What "migration failed" means

WeKnora auto-runs `golang-migrate` migrations on every startup. When a
migration fails, the application **still finishes starting up** (so the UI
remains reachable to help you diagnose the problem), but:

- The failing migration is rolled back, leaving the database at the previous
  version. Any tables / indexes introduced by that migration **are not
  created**.
- Downstream features depending on those tables (Wiki ingest, knowledge graph,
  task queues, …) may silently produce nothing.
- The system info page shows the partial DB version + a red "Migration failed"
  tag and the captured error.

The cached error message you see in the UI is the same one logged at startup
under `Database migration failed: ...`. Recent container logs are the
authoritative source — copy them before doing anything destructive.

## Supported database drivers

WeKnora selects the migration directory from `DB_DRIVER`:

| `DB_DRIVER` | Migration directory | Notes |
|-------------|---------------------|-------|
| `postgres` | `migrations/versioned` | Default path for PostgreSQL / ParadeDB. PostgreSQL extensions may be required. |
| `mysql` | `migrations/mysql` | MySQL 8.x path. New MySQL installs start from baseline version `64`. |
| `sqlite` | `migrations/sqlite` | Lite / embedded path. |

When `DB_DRIVER=mysql`, do not use `RETRIEVE_DRIVER=postgres`; the PostgreSQL
retriever depends on PostgreSQL / ParadeDB vector features. Use an external
vector store such as Qdrant, Milvus, Weaviate, Doris, Tencent VectorDB,
Elasticsearch, or OpenSearch.

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

### 2. MySQL baseline did not apply

MySQL deployments use a baseline schema migration instead of replaying every
PostgreSQL-specific historical migration. On a new MySQL database, the expected
schema migration version is `64`.

**Symptoms in the error**:

```
Error 1064 (42000): You have an error in your SQL syntax
Unknown database 'WeKnora'
Access denied for user ...
```

**Fix**:

1. Confirm the database exists and the configured role can create tables.
2. Confirm the application is using the MySQL driver:

   ```bash
   DB_DRIVER=mysql ./scripts/migrate.sh version
   ```

3. Check the recorded migration state:

   ```sql
   SELECT version, dirty FROM schema_migrations;
   ```

4. If the database is empty and no user data has been written yet, recreate it
   and run the MySQL migration path again. If it contains user data, do not
   drop tables; collect the migration error and open an issue.

### 3. Dirty migration state

If a migration crashed partway through (OOM, container kill, network blip)
`golang-migrate` marks the schema as "dirty" at the failing version. By
default, WeKnora's startup tries to auto-recover; if you disabled that with
`AUTO_RECOVER_DIRTY=false` you'll see:

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

After that, restart WeKnora.

Or set `AUTO_RECOVER_DIRTY=true` (the default in recent versions) and just
restart — startup will perform the same `force` + retry automatically.

### 4. Insufficient privileges on the database role

Some migrations create extensions or alter shared catalogs, which require
either superuser or `CREATEROLE` / `CREATEDB`. Errors look like:

```
ERROR: permission denied to create extension "pg_trgm"
ERROR: must be owner of database ...
```

**Fix**: grant the role used by `DB_USER` the necessary privileges, or
pre-create the extensions / objects as a superuser ahead of time, then
restart. The migration's `CREATE EXTENSION IF NOT EXISTS` will then no-op.

For MySQL, the configured role must be able to create, alter, index, and drop
tables in the selected `DB_NAME`.

### 5. Out-of-disk during `CREATE INDEX`

GIN / pgvector indexes can require significant temporary space. Errors:

```
ERROR: could not extend file ...: No space left on device
ERROR: cannot create temporary tables in transaction
```

**Fix**: free disk on the volume backing `PGDATA`, then restart. The
migration will retry the index build.

For MySQL, free disk on the volume backing the MySQL data directory, then
restart and retry the migration.

### 6. Schema drift from manual edits

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
   `make migrate-version`) points to a file under the directory selected by
   `DB_DRIVER`: `migrations/versioned/` for PostgreSQL, `migrations/mysql/`
   for MySQL, or `migrations/sqlite/` for SQLite. Open the matching
   `<version>_*.up.sql` file and look for the statement matching the error
   type (extension, index, function, foreign key, …).
3. **Run the failing statement manually** against the DB using `psql` for
   PostgreSQL or `mysql` for MySQL. The error will be far more specific than
   the migration wrapper's.
4. **Fix the underlying cause** (install extension, fix privileges, free
   disk, …), then either:
   - Restart WeKnora and let auto-recovery retry; **or**
   - Run `make migrate-up` from a checkout to apply migrations outside the
     server process.
5. **Verify**: the system info page should now show the DB version without
   the "Migration failed" tag, and the previously broken feature (Wiki, KG,
   …) should start producing output.

---

## Reporting an issue

If you've worked through the checklist and the migration still fails, please
open an issue at:

<https://github.com/Tencent/WeKnora/issues/new?template=bug_report.yml>

Include:

- WeKnora version + commit ID (from the system info page).
- The full error from the system info page (or container logs).
- `DB_DRIVER` and the database version (`SELECT version();` for PostgreSQL,
  `SELECT VERSION();` for MySQL), including how it was deployed (vanilla,
  ParadeDB, RDS, self-managed Docker, …).
- For PostgreSQL, the output of:
  ```sql
  SELECT extname, extversion FROM pg_extension;
  ```
- For MySQL, the output of:
  ```sql
  SELECT version, dirty FROM schema_migrations;
  SHOW TABLES;
  ```
- Any non-default values of `RETRIEVE_DRIVER`, `AUTO_MIGRATE`, and
  `AUTO_RECOVER_DIRTY`.

The "Report issue" link on the system info page pre-fills a body with the
captured error for you — clicking it is the fastest path.
