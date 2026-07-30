# WeKnora MySQL 8 Implementation Plan

[Chinese version](MYSQL_8_IMPLEMENTATION_PLAN_CN.md) | **English version**

## 1. Goal

Make MySQL 8 a first-class optional application database backend alongside
PostgreSQL and SQLite. Select it with:

```env
DB_DRIVER=mysql
```

The application database stores business data such as users, workspaces,
knowledge bases, sessions, messages, tasks, and configuration. Vector retrieval
continues to use one of WeKnora's supported retrieval engines, such as Qdrant,
OpenSearch, Milvus, Weaviate, or Doris. MySQL does not replace PostgreSQL's
`pgvector` capability.

## 2. Scope

### Included

- A fresh MySQL 8 deployment path.
- The complete current application schema, rather than a small subset of early
  tables.
- The JSON queries, search behavior, soft-delete uniqueness, and task-queue
  concurrency needed by existing application features.
- An independent MySQL Docker Compose stack and deployment documentation.
- Unchanged PostgreSQL and SQLite code paths, migrations, and default behavior.

### Excluded

- Migrating historical PostgreSQL or SQLite data into MySQL.
- A MySQL-native vector retrieval implementation or a `pgvector` compatibility
  layer.
- Changing existing PostgreSQL data, migration records, or Docker volumes.

## 3. Design

At startup, the application selects the database implementation from
`DB_DRIVER`:

| Configuration | Application database | Migration source |
| --- | --- | --- |
| `postgres` | PostgreSQL | `migrations/versioned` |
| `sqlite` | SQLite | `migrations/sqlite` |
| `mysql` | MySQL 8 | `migrations/mysql` |

MySQL does not replay PostgreSQL's historical migrations because they contain
PostgreSQL-specific syntax and features such as `jsonb`, `pg_trgm`,
`RETURNING`, and PostgreSQL indexes and extensions. A fresh MySQL deployment
instead applies the version-74 baseline:

```text
migrations/mysql/000074_baseline.up.sql
```

The baseline creates the complete current schema in one operation and records
migration version `74`. Every future schema version must add a matching MySQL
migration.

## 4. Completed Work

### 4.1 Database startup and migrations

- `internal/container/container.go` accepts `DB_DRIVER=mysql`.
- The GORM MySQL driver connects using `utf8mb4`, `utf8mb4_0900_ai_ci`, and UTC
  time parsing.
- `internal/database/migration.go` selects the MySQL migration driver and
  directory.
- PostgreSQL and SQLite retain their own connection paths and migrations.

### 4.2 Complete MySQL baseline

- `migrations/mysql/000074_baseline.up.sql` creates the current 50-table
  application schema.
- The baseline includes task queues, Wiki, system settings, tag relationships,
  processing spans, and other required feature tables.
- MySQL 8 compatibility is handled with `DATETIME(3)` timestamp defaults,
  prefix indexes for long indexed tokens, compatible unsigned foreign-key
  columns, and generated columns plus unique indexes where MySQL must emulate
  soft-delete partial uniqueness.

### 4.3 Runtime SQL adaptation

- JSON reads use `JSON_EXTRACT` and `JSON_UNQUOTE` on the MySQL path.
- PostgreSQL-specific `ILIKE`, `::jsonb`, and JSON-containment queries use
  MySQL equivalents including `LIKE`, `CAST(... AS CHAR)`, and
  `JSON_CONTAINS`.
- MySQL 8 task queues use `FOR UPDATE SKIP LOCKED`.
- `UPDATE ... RETURNING` is implemented as an update followed by a read in one
  transaction where MySQL has no equivalent.
- MySQL and PostgreSQL use row locking for vector-database deletion guards;
  SQLite retains its existing behavior.

### 4.4 Deployment and documentation

- `docker-compose.mysql.yml` starts MySQL, Qdrant, Redis, docreader, the
  application, and the frontend without changing the existing stack.
- `.env.mysql.example` provides a MySQL-specific environment template.
- `docs/DEPLOY_MYSQL.md` and `docs/DEPLOY_MYSQL_CN.md` document deployment,
  operational safeguards, backup procedures, and future migration rules.

## 5. Completed Verification

The following verification has been completed with isolated MySQL 8.4
containers where applicable:

1. Apply the complete MySQL baseline migration.
2. Confirm that all 50 business tables are created.
3. Validate JSON extraction and JSON-containment operations.
4. Validate that a soft-deleted vector store name can be reused.
5. Validate that duplicate active invitations are rejected by the unique
   constraint.
6. Validate `docker-compose.mysql.yml` with Docker Compose.
7. Format changed Go files with `gofmt`.
8. Build the complete Docker application image.
9. Start a clean MySQL 8 application database, then validate health checks,
   registration/login, knowledge-base creation, session creation, message
   loading, and keyword search.

## 6. Ongoing Requirements

The MySQL backend implementation is complete for fresh deployments. Subsequent
changes must keep the following requirements:

1. Every schema change adds an equivalent MySQL migration when the change is
   relevant to MySQL.
2. MySQL-specific behavior is covered with focused tests and does not change
   the PostgreSQL or SQLite paths.
3. MySQL Compose configuration is validated after deployment changes.
4. Operational safeguards are implemented one feature per branch, following
   `MYSQL_8_RESILIENCE_OPERATIONS_PLAN_CN.md` and its English counterpart.

## 7. Acceptance Criteria

- A new MySQL 8 database starts with `DB_DRIVER=mysql`, migrates successfully,
  and serves the application.
- `schema_migrations` records version `74` with `dirty=0`, and the baseline
  creates 50 business tables.
- Users, workspaces, knowledge bases, sessions, messages, task queues, Wiki,
  and resource metadata can be stored and queried through MySQL.
- PostgreSQL and SQLite continue to select their existing connection methods
  and migration directories, without executing the MySQL baseline.
- The MySQL Compose stack runs independently while vector retrieval remains
  the responsibility of Qdrant or another external retrieval engine.

## 8. Current Verification State (2026-07-28)

- `go mod tidy` completed and includes the checksum for
  `gorm.io/driver/mysql v1.6.0`.
- The focused database, repository, service, and container tests completed.
- `docker compose --env-file .env.mysql.example -f docker-compose.mysql.yml
  config --quiet` completed.
- `scripts/test_mysql_schema.sh` created all 50 business tables and validated
  JSON queries and soft-delete uniqueness.
- A clean MySQL 8 container automatically records migration version `74` and
  creates the complete application schema.
- MySQL failure paths do not alter or block existing PostgreSQL or SQLite
  behavior.

## 9. Submission Notes

PostgreSQL and SQLite connection configuration, migration directories, and the
default Compose stack are unchanged. The MySQL baseline is intended only for a
new MySQL database; existing PostgreSQL and SQLite data is neither read, changed,
nor migrated.
