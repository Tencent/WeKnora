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

## Container Log Retention

The MySQL Compose stack configures Docker's `local` logging driver for every
service (`frontend`, `app`, `mysql`, `redis`, `qdrant`, and `docreader`). Docker
rotates stdout and stderr logs at 20 MB per file and keeps three files, limiting
each container's local Docker log history to approximately 60 MB.

The setting applies when a container is created. To apply it to an existing
stack, recreate the services:

```bash
docker compose --env-file .env.mysql -f docker-compose.mysql.yml up -d --force-recreate
```

This limit does not replace centralized log retention for long-term auditing.

## Application File Log Retention

When `LOG_PATH` is set, the application also writes a local file log. Its
rotation is independent of Docker stdout/stderr logging and has the following
safe defaults:

```env
LOG_FILE_MAX_SIZE_MB=50
LOG_FILE_MAX_BACKUPS=3
LOG_FILE_MAX_AGE_DAYS=28
LOG_FILE_COMPRESS=true
```

Invalid zero, negative, or malformed values fall back to these defaults so an
accidental configuration error cannot disable rotation. On startup and logger
configuration reload, the application checks the filesystem that contains
`LOG_PATH`. It writes a warning to stderr below 20% free space and a critical
signal below 10% or 5 GB free. The thresholds can be adjusted with
`LOG_DISK_WARNING_FREE_PERCENT`, `LOG_DISK_CRITICAL_FREE_PERCENT`, and
`LOG_DISK_MIN_FREE_GB`.

These signals do not delete logs or trigger automatic backups.

## Monitoring Endpoints

`GET /metrics` exposes low-cardinality Prometheus metrics for HTTP traffic,
database and Redis reachability, database pool use, application file-log size,
free disk space, build version, and the migration state captured at startup.
It intentionally excludes request paths, user data, credentials, and error
messages from labels and responses.

Prometheus should scrape the application port directly. Treat `/metrics` as an
internal endpoint: restrict it with a firewall or reverse-proxy allowlist and
do not publish it through a public frontend route.

`GET /api/v1/admin/operations/status` returns a sanitized runtime snapshot for
system administrators. It reports dependency states, connection-pool values,
file-log storage values, and migration state, but never filesystem paths, DSNs,
passwords, or raw dependency errors.

## Email Alerts

SMTP alerts are opt-in and are intended for deployment operators, not every
WeKnora user. The app checks database and Redis reachability, dirty migration
state, and the configured file-log filesystem. It sends a first alert for a new
condition, suppresses duplicates while that condition persists, and sends one
recovery email when it clears. Failed email deliveries are retried only after
the cooldown period and log only a rate-limited, sanitized message.
The check interval has a minimum of 10 seconds and the cooldown has a minimum
of 60 seconds so an unsafe configuration cannot create an email or log storm.

```env
OPS_ALERT_EMAIL_ENABLED=true
OPS_ALERT_EMAIL_TO=operator@example.com,oncall@example.com
OPS_ALERT_CHECK_INTERVAL_SECONDS=60
OPS_ALERT_COOLDOWN_SECONDS=300
OPS_ALERT_TIMEOUT_SECONDS=5
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_TLS_MODE=starttls
SMTP_USERNAME=weknora-alerts@example.com
SMTP_PASSWORD_FILE=/run/secrets/weknora_smtp_password
SMTP_FROM=weknora-alerts@example.com
```

`SMTP_TLS_MODE` supports `starttls` (the default, normally port 587),
`implicit` (normally port 465), and `none`. Authentication is refused when
`none` is selected. Configure exactly one of `SMTP_PASSWORD` and
`SMTP_PASSWORD_FILE`; a mounted secret file is recommended. Configuration
errors leave email alerts disabled and never expose the password in logs or API
responses.

The in-process notifier cannot report a total application or host outage. Keep
an independent monitor such as Uptime Kuma or Prometheus Alertmanager outside
this Compose stack to poll `/readyz` and send its own notification when the app
cannot run at all.

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
