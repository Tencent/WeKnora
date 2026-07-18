# MySQL as the business database

WeKnora can use MySQL 8.0.13 or later for its application data. MySQL is not
a retrieval backend in WeKnora, so a MySQL deployment must also configure a
supported external vector store such as Qdrant, Milvus, Weaviate,
Elasticsearch, OpenSearch, Doris, or Tencent VectorDB.

`DB_DRIVER=mysql` together with `RETRIEVE_DRIVER=postgres` is rejected at
startup. The PostgreSQL retriever stores vectors in the primary PostgreSQL
database and therefore cannot be combined with a MySQL primary database.

## Fresh local development setup

Start MySQL and Qdrant from the development Compose file:

```bash
docker compose -f docker-compose.dev.yml \
  --profile mysql --profile qdrant \
  up -d mysql qdrant redis
```

Configure the locally running backend:

```dotenv
DB_DRIVER=mysql
DB_HOST=127.0.0.1
DB_PORT=3306
DB_USER=weknora
DB_PASSWORD=weknora123
DB_NAME=WeKnora

RETRIEVE_DRIVER=qdrant
QDRANT_HOST=127.0.0.1
QDRANT_PORT=6334
```

The backend applies `migrations/mysql/000070_init.up.sql` automatically on a
fresh database. The MySQL baseline represents WeKnora schema version 70; all
future cross-database schema changes must add matching PostgreSQL and MySQL
migrations with the same version number.

To run migrations manually, install `golang-migrate` with both database
drivers and use the existing Make target:

```bash
go install -tags 'postgres mysql' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
make migrate-up
```

The migration script selects `migrations/mysql` when `DB_DRIVER=mysql`.

For Helm, use an external MySQL service and disable the bundled PostgreSQL
deployment. Configure an external vector store in the same values file:

```bash
helm upgrade --install weknora ./helm \
  --set postgresql.enabled=false \
  --set database.driver=mysql \
  --set database.host=mysql.example.internal \
  --set database.port=3306 \
  --set app.env.RETRIEVE_DRIVER=qdrant \
  --set secrets.dbUser=weknora \
  --set secrets.dbPassword='<password>'
```

## Existing installations

The MySQL migration stream is currently a fresh-install baseline. It does not
convert an existing PostgreSQL database or import its data. Moving an existing
deployment requires a separately planned data migration, validation, and
cutover; do not point an existing PostgreSQL data directory at MySQL.

Use `utf8mb4` with `utf8mb4_unicode_ci`. Application timestamps are read and
written in UTC. Back up the database before manual migration, rollback, or
version-forcing operations.
