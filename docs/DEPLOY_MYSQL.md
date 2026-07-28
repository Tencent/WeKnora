# MySQL 8 Deployment

WeKnora can use MySQL 8 as its application metadata database with
`DB_DRIVER=mysql`. This is a fresh-deployment path only. Existing PostgreSQL
or SQLite databases are not migrated or modified.

MySQL is not a vector retrieval engine. Set `RETRIEVE_DRIVER` to a supported
retriever such as `qdrant`, `opensearch`, `milvus`, `weaviate`, or `doris`.
The supplied compose file uses Qdrant.

## Requirements

- MySQL 8.0.13 or newer, using `utf8mb4` and `utf8mb4_0900_ai_ci`.
- Docker Compose v2 for the bundled deployment.
- A new, empty MySQL database. Do not point `DB_DRIVER=mysql` at an existing
  WeKnora PostgreSQL dump or a partially initialized MySQL schema.

## Start

Use `.env.mysql.example` as the environment-file template and set strong
database passwords. The supplied compose file builds the application from the
current checkout, so it can validate a fork before an official image includes
the MySQL change. Then start the standalone stack:

```bash
docker compose --env-file .env.mysql -f docker-compose.mysql.yml up -d
```

On first startup, the application runs `migrations/mysql/000074_baseline.up.sql`.
It creates the complete current application schema and records migration version
`74` in `schema_migrations`. Existing PostgreSQL migrations continue to be used
unchanged when `DB_DRIVER=postgres`; SQLite continues to use its own baseline.

## Schema Check

Run the repeatable schema validation before publishing a MySQL-related change:

```bash
sh scripts/test_mysql_schema.sh
```

The script starts a temporary MySQL 8.4 container, applies the baseline, checks
the complete table count, JSON functions, and soft-delete uniqueness behavior,
then removes the container.

## Future Migrations

The MySQL baseline represents version 74. Every future schema change must add a
matching `migrations/mysql/<version>_*.up.sql` migration in addition to the
PostgreSQL and, where applicable, SQLite migration. This keeps upgrades from a
fresh MySQL deployment deterministic without replaying PostgreSQL-only history.
