# ParadeDB 0.22.2 → 0.22.6 (PostgreSQL 17)

The production and development Compose files use `paradedb/paradedb:v0.22.6-pg17`.
The `langfuse-db-init` client uses the same image; Langfuse shares the Postgres
server and has its own database.

ParadeDB 0.22.2 compiles its Linux x86 extension for `x86-64-v3`, which can crash
with `Illegal instruction` on CPUs without AVX2. Version 0.22.6 lowers that
baseline to `x86-64-v2` (including SSE4.2 and POPCNT). This does **not** remove the
ARM `neoverse-n1` requirement or establish CPU compatibility for other services.

- [Upstream CPU fix](https://github.com/paradedb/paradedb/pull/4672)
- [0.22.6 release](https://github.com/paradedb/paradedb/releases/tag/v0.22.6)
- [Version-specific upgrade instructions](https://github.com/paradedb/paradedb/blob/v0.22.6/docs/deploy/upgrading.mdx)

## Existing deployments

Keep PostgreSQL on major version **17** and retain the existing data volume.
Do not use `docker compose down -v`, remove the volume, or substitute a PG18
image. This procedure covers the 0.22.2 → 0.22.6 patch upgrade; the Helm chart's
older 0.18.9 default is outside this validated upgrade path.

1. Stop application writers (`docker compose stop app` and, if enabled,
   `docker compose stop langfuse-web langfuse-worker`; stop a locally running
   development backend as well).
2. Take a restorable backup of all databases, including Langfuse and roles:

   ```bash
   umask 077
   docker compose exec -T postgres sh -c 'pg_dumpall -U "$POSTGRES_USER"' > paradedb-before-upgrade.sql
   ```

   Check the command succeeded and keep the backup outside the database volume.
   If the old server cannot start on this CPU, first preserve a cold snapshot of
   the stopped volume; make a logical backup on a compatible host or validate
   recovery from that snapshot before changing the only copy.
3. With the updated Compose file, replace only the database container:

   ```bash
   docker compose pull postgres
   docker compose up -d --no-deps --wait postgres
   ```

4. Complete the extension's SQL upgrade. Replacing the image alone leaves the
   existing database's `pg_extension` catalog at 0.22.2:

   ```bash
   docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1' <<'SQL'
   ALTER EXTENSION pg_search UPDATE TO '0.22.6';
   SELECT extname, extversion FROM pg_extension WHERE extname IN ('pg_search', 'vector');
   SELECT * FROM paradedb.version_info();
   SQL
   ```

   Run the same SQL in **every other database containing `pg_search`**, including
   `postgres` and any template or Langfuse database where it was installed.
   Do not install it into databases that do not use it. The catalog and
   `paradedb.version_info()` should both report 0.22.6; pgvector stays at 0.8.1.

   Migration `000099` also performs this step in the WeKnora database when the
   installed extension is 0.22.2–0.22.5 and the 0.22.6 package is available.
   It respects `app.skip_embedding` and leaves absent extensions and other
   release lines alone. If the migration
   ran before the new database image was installed, perform the SQL step above
   manually: applied migrations are not repeated. Older application releases
   without migration `000099` also require the manual step.
5. Resume the application writers, check migration status and run representative
   keyword/vector searches. No `REINDEX` or document re-ingestion is required
   by this patch upgrade. Preserve the backup until deployment checks pass.

For development use `docker compose -f docker-compose.dev.yml` in the commands
above. If rollback is necessary, restore the pre-upgrade backup/snapshot into a
separate volume with the old image. Merely changing the image tag back does not
reverse extension SQL changes; the migration's down file intentionally does not
attempt that.

## Reproducible verification

```bash
docker pull paradedb/paradedb:v0.22.2-pg17
docker pull paradedb/paradedb:v0.22.6-pg17
python3 scripts/test_paradedb_upgrade.py
```

The Python 3/Docker test uses uniquely named disposable containers and volumes,
publishes no ports, and does not access an existing WeKnora database. It saves
snapshots, query plans, a synthetic-data backup and logs in its printed output
directory, then removes only its own Docker resources.

It applies the real baseline migrations, seeds 720 embeddings across 798, 1024
and 3584 dimensions plus tenant/model/knowledge/session/message records, and
restarts the **same data volume** under the new image. It checks:

- All project table contents, columns, sequence values and index file identities.
- 104 keyword/vector queries: Chinese and English, missing/empty terms, KB,
  document and tag filters, disabled/NULL-enabled rows, Top-K and thresholds.
- Both normal planner choices and explicitly exercised HNSW index scans; BM25
  uses the production `|||` operator and `paradedb.score`. Equal BM25 scores use
  an ID tie-breaker to make comparisons reproducible.
- Extension migration, idempotence, skip-embedding, unavailable target and absent extension.
- Results after another restart; committed insert/update/delete and vector lookup.
- A fresh database with the full migration chain and a Chinese BM25 query.

This verifies the fixture and upgrade path, not every production corpus or
workload. Production backup/restore and representative retrieval checks remain
part of the deployment procedure.

### Validation recorded on 2026-09-17

The test passed on Linux ARM64 with PostgreSQL 17.9 and pgvector 0.8.1 in both
images: all 70 project table snapshots and all 104 retrieval results were
identical after upgrade and after restart, including scores and index file IDs.
A separate restore of the pre-upgrade dump into 0.22.2 also preserved all table
hashes/counts and sequence values.

The Linux AMD64 0.22.6 image was additionally started under QEMU user emulation
with `QEMU_CPU=Nehalem`. A CPUID probe confirmed AVX2 was absent; initialization,
BM25/HNSW index creation, keyword search and vector lookup passed. This is an
emulation check, not a test on the affected physical server or Kunpeng hardware.
