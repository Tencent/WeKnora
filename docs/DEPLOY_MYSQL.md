# MySQL 8 Deployment

[Chinese version](DEPLOY_MYSQL_CN.md) | **English version**

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

## Operations Console

System administrators can open **Settings -> System Administration ->
Operations** (or `/platform/system/operations`) to view the protected runtime
status without exposing it through a public route. The page refreshes every 30
seconds while it is open and also has a manual refresh control. It presents
dependency reachability, the database driver and pool state, migration state,
application file-log disk state, and the scheduled-backup/retention state.

For a MySQL deployment with a reachable database and a clean migration state,
the page also provides **Create backup**. The operator must enter a reason and
confirm the action. It calls the existing system-admin-only manual backup API,
so the same MySQL advisory lock, manifest/checksum generation, error handling,
and audit event apply. The result shows only safe backup identifiers and
relative artifact names. Do not put passwords, tokens, or business-sensitive
content in the reason because the reason is an audit record.

The page does not provide restore, database replacement, configuration rollback,
image rollback, or backup deletion controls. Those higher-risk operations remain
in the reviewed PowerShell CLI and break-glass procedures.

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

## Manual Backups

The first backup phase is deliberately limited to an operator-triggered MySQL
logical backup. It is only available to system administrators, requires a
reason, and is disabled by default. PostgreSQL and SQLite never enter this
MySQL-specific path.

For Docker Desktop on Windows, keep the backup directory on a host data drive
rather than inside Docker's virtual disk or a container layer:

```env
BACKUP_ENABLED=true
BACKUP_LOCAL_DIR=/data/backups
BACKUP_HOST_DIR=D:/WeKnoraBackups
BACKUP_TIMEOUT_SECONDS=900
BACKUP_MYSQLDUMP_PATH=mysqldump
```

The Compose file mounts `BACKUP_HOST_DIR` into the app at `/data/backups`.
Choose a directory that only the deployment operator can read. This phase does
not encrypt archives yet, so filesystem access control and an off-host copy are
important until the later encrypted-destination feature is added.

Create a backup by waiting for the protected request to complete:

```bash
curl -X POST http://localhost:8080/api/v1/admin/operations/backups \
  -H "Authorization: Bearer <system-admin-token>" \
  -H "Content-Type: application/json" \
  -d '{"reason":"before a schema or deployment change"}'
```

The app obtains a MySQL advisory lock, so a second request returns `409` rather
than overlapping the first dump. The dump uses `mysqldump --single-transaction
--routines --events`, writes a private gzip archive and an adjacent JSON
manifest atomically, and records the archive size, SHA-256, application version,
migration state, trigger, reason, and final result. Passwords, absolute paths,
DSNs, and raw `mysqldump` output are excluded from the response, manifest, and
audit event. A failed dump removes its partial archive and records only a safe
failure category in its manifest and audit event.

Do not put secrets in the operator reason: it is recorded in the manifest and
the system audit log. Restore is intentionally not available from this API; the
restore-verification workflow below restores only into an isolated MySQL
instance.

## Local File Archives

When `STORAGE_TYPE=local`, set `BACKUP_FILES_ENABLED=true` to pair every
successful manual or scheduled MySQL backup with a `tar.gz` archive of
`LOCAL_STORAGE_BASE_DIR` (normally `/data/files`). Each file archive has an
adjacent inventory with relative file names, sizes, and SHA-256 checksums. The
main backup manifest records only relative archive and inventory names, summary
checksums, and the start/end timestamps. It never records the local storage
absolute path.

```env
STORAGE_TYPE=local
LOCAL_STORAGE_BASE_DIR=/data/files
BACKUP_FILES_ENABLED=true
```

This setting is rejected for object-storage providers and when the files and
backup directories overlap. The backup destination must be on an
access-controlled host data drive, outside Docker's container layer. Retention
removes the SQL archive, file archive, inventory, and main manifest together;
the newest complete backup remains protected.

The database dump and file archive are deliberately **not** described as an
atomic point-in-time snapshot. Their separate start and completion timestamps
make this boundary visible. For a strict recovery point, pause uploads and
writes in a maintenance window before creating the backup. Do not restore the
file archive over a live `/data/files` directory.

Verify a file archive by extracting it only into a new, empty directory. This
PowerShell drill checks the outer archive plus each extracted file against its
inventory and does not contact Docker or overwrite application data:

```powershell
.\scripts\verify_local_file_backup.ps1 `
  -BackupId weknora-mysql-YYYYMMDDTHHMMSSZ-<24-lowercase-hex-characters> `
  -BackupDirectory D:\WeKnoraBackups `
  -DestinationDirectory D:\WeKnoraRestoreDrill\files
```

## Qdrant Native Snapshots

Qdrant snapshots are optional acceleration for vector-index recovery, not the
only recovery path. When the database and local files are restored, WeKnora can
rebuild Qdrant indexes from source data. Enable native snapshots only when
`RETRIEVE_DRIVER=qdrant`; other retrievers do not enter this path.

```env
BACKUP_QDRANT_ENABLED=true
# Empty uses http://QDRANT_HOST:6333 inside the Compose network.
BACKUP_QDRANT_URL=
```

For each collection, the backup requests Qdrant's native snapshot API, copies
the returned snapshot into `BACKUP_LOCAL_DIR`, calculates its SHA-256, then
deletes the temporary server-side snapshot. The main manifest records the
collection, a relative opaque filename, size, checksum, and time range, but no
endpoint or API key. A failure in any collection leaves no successful combined
backup. Retention removes these snapshots together with the SQL, file, and
manifest artifacts.

Snapshots and the MySQL dump are not an atomic point-in-time image. Use a
maintenance window when a strict recovery point is required. To validate and
restore snapshots, the following drill starts a new disposable Qdrant container
and uploads the snapshots only to that container. It does not connect to the
live Compose service:

```powershell
.\scripts\verify_qdrant_snapshots.ps1 `
  -BackupId weknora-mysql-YYYYMMDDTHHMMSSZ-<24-lowercase-hex-characters> `
  -BackupDirectory D:\WeKnoraBackups
```

## Isolated Restore Verification

Before relying on a backup, restore it into a new temporary MySQL 8 container
and verify it. This is an operational drill, not a production restore command:
it never starts, stops, connects to, or overwrites the normal `mysql` Compose
service. The temporary service has no published ports, no persistent MySQL
volume, a read-only backup mount, and its own internal Docker network. It is
removed automatically when verification finishes.

Run this from PowerShell at the repository root, replacing the backup ID with
the value returned by the manual-backup API:

```powershell
.\scripts\verify_mysql_restore.ps1 `
  -BackupId weknora-mysql-YYYYMMDDTHHMMSSZ-<24-lowercase-hex-characters> `
  -BackupDirectory D:\WeKnoraBackups `
  -EnvFile .env.mysql
```

The script checks the adjacent JSON manifest, archive byte size, SHA-256, and
gzip stream before importing. It then checks that exactly one restored
application database contains `schema_migrations`, confirms a clean migration
state and (when recorded) the manifest migration version, reports exact counts
for key tables, and shows only record IDs and timestamps for a small sample.
It does not print database passwords, DSNs, file paths from the manifest, or
business-content fields.

The script creates a random restore-only root password in its process
environment and writes a temporary MySQL client option file through container
standard input, rather than putting that password in a host command line. Use
`-KeepContainer` only when investigating a failed drill; clean it up with the
project name printed by the script. A successful verification provides a
measured restore duration for the current backup size and host, but it does not
authorize a production database replacement. A real production cutover remains
a separate, reviewed break-glass operation.

Run the repeatable integration drill after changing this workflow:

```powershell
.\scripts\test_mysql_restore_verify.ps1
```

It builds a temporary MySQL source database, produces a manifest-compatible
gzip dump containing representative records, verifies that dump through the
isolated profile, and removes every temporary container and file afterward.

## Scheduled Backups

Scheduled backups are opt-in: they start only when both `BACKUP_ENABLED=true`
and a valid `BACKUP_SCHEDULE` are set. A Cron expression alone cannot enable
backups accidentally, and an empty value keeps scheduling disabled. This is
intentionally different from the manual-backup endpoint: a scheduled run has
no operator-supplied reason and uses `trigger=scheduled` in its manifest and
audit record.

For a personal or small-team deployment, the following is a reasonable starting
point. `BACKUP_SCHEDULE` uses the standard five-field Cron format: `minute
hour day-of-month month day-of-week`. It follows the application container's
local timezone, which is normally UTC unless the image/deployment configures a
different timezone. Verify the container time with `docker compose exec app
date` before choosing a production schedule.

```env
# Enable manual and scheduled MySQL backups
BACKUP_ENABLED=true

# Every day at 03:30
BACKUP_SCHEDULE=30 3 * * *

# Keep backups for seven days
BACKUP_RETENTION_DAYS=7

# Skip scheduled runs when fewer than 5 GiB are free
BACKUP_MIN_FREE_GB=5
```

After every **successful scheduled backup**, the retention sweep removes only
expired, valid archive-and-manifest pairs. It never removes the newest valid
backup, even if that backup is older than the configured horizon. Set
`BACKUP_RETENTION_DAYS=0` to disable automatic deletion entirely. Manual and
failed backups do not trigger deletion. When the backup disk is full, confirm
the newest recoverable backup first, then free space manually or adjust the
retention policy.

Before creating an automatic archive, the scheduler checks the free space of
`BACKUP_LOCAL_DIR`. A value below `BACKUP_MIN_FREE_GB`, an unavailable MySQL
database, a held MySQL advisory lock, or a failed dump produces no partial
success record. Lock contention is treated as a safe skip, not a failure, so
multiple application instances cannot overlap scheduled dumps. PostgreSQL and
SQLite do not start this scheduler.

`GET /api/v1/admin/operations/status` now includes sanitized scheduled-backup
state, and `/metrics` exposes schedule enabled, last success, last failure,
and retention-failure gauges. When SMTP alerts are enabled, a scheduled backup
failure or retention cleanup failure sends one deduplicated email; a later
successful run sends the normal recovery notification. Alerts, status, and
audit records never include passwords, DSNs, absolute paths, or raw
`mysqldump` output.

## Controlled Rollback CLI

`scripts/rollback_mysql_deployment.ps1` is a MySQL-only, operator-run rollback
tool for Windows and Docker Desktop. It does not load PostgreSQL or SQLite
configuration and never changes either deployment path. Store its audit output
in an access-controlled directory on a host data drive, separate from the
repository and Docker Desktop's virtual disk:

```powershell
New-Item -ItemType Directory -Force D:\WeKnoraOpsAudit
```

Each invocation writes one new JSON audit record with the action, outcome,
repository revision, configuration SHA-256 values, migration version, and
approved image digests. It records only file names, never passwords, DSNs,
secret values, raw environment-file contents, or absolute backup paths.
Protect this directory as operational evidence; the script does not edit
existing audit records.

### Application Image Rollback

An image rollback is permitted only when all of these conditions hold:

- The target uses a fixed, non-`latest` version tag.
- The operator supplies approved SHA-256 repository digests for the app, UI,
  and docreader images. The script pulls each image and rejects a digest
  mismatch before recreating anything.
- The operator supplies the target's known MySQL migration version, and it is
  exactly equal to the clean `schema_migrations` version of the running MySQL
  database. A lower or higher version is refused because it is not a proven
  compatible rollback.
- The active `.env.mysql` produces a valid bundled MySQL Compose definition.

The command recreates only `app`, `frontend`, and `docreader` with `--no-build`
and `--no-deps`; it does not recreate `mysql`, `redis`, Qdrant, or persistent
volumes. It temporarily starts the rolled-back application with
`AUTO_MIGRATE=false`, then requires the application container health check to
pass. This prevents a rollback from unexpectedly applying a forward migration.

Obtain the three digests from the reviewed release record, not from a mutable
tag copied from an unreviewed command. For a release known to use migration
version `74`, the invocation shape is:

```powershell
.\scripts\rollback_mysql_deployment.ps1 `
  -Action Deployment `
  -AuditDirectory D:\WeKnoraOpsAudit `
  -EnvFile .env.mysql `
  -ImageTag <reviewed-version> `
  -TargetMigrationVersion 74 `
  -ExpectedAppImageDigest sha256:<64-lowercase-hex-characters> `
  -ExpectedFrontendImageDigest sha256:<64-lowercase-hex-characters> `
  -ExpectedDocreaderImageDigest sha256:<64-lowercase-hex-characters> `
  -ConfirmRollback
```

The tool cannot prove a migration version embedded in an arbitrary historical
image. Treat the release record and its tested migration version as a required
change-control input. If the image does not use the current schema exactly,
use a compatible forward fix or the break-glass recovery procedure instead.

### Configuration Rollback

Configuration rollback is deliberately a staging operation, not an automatic
container restart. This prevents a historical file from silently changing
database credentials, ports, or topology on a running stack. Keep reviewed
environment revisions in an access-controlled directory that is not Git when
they contain secrets. The candidate must be inside that approved directory;
the script validates MySQL compatibility and `docker compose config` before it
copies the candidate over `.env.mysql`. It also requires a new, non-existing
path to preserve the current environment file before the change.

```powershell
.\scripts\rollback_mysql_deployment.ps1 `
  -Action Config `
  -AuditDirectory D:\WeKnoraOpsAudit `
  -EnvFile .env.mysql `
  -ApprovedConfigDirectory D:\WeKnoraConfigHistory `
  -ConfigFile D:\WeKnoraConfigHistory\.env.mysql.20260728 `
  -CurrentConfigBackupPath D:\WeKnoraConfigHistory\.env.mysql.before-rollback `
  -ConfirmRollback
```

After staging, compare the rendered Compose configuration and recreate only
the services whose reviewed settings should change during a maintenance
window. Do not use this operation as a shortcut for changing MySQL credentials
or replacing the database volume.

### Break-Glass Database Recovery

The CLI has no command that imports into, stops, connects to, or overwrites the
live MySQL service. Its `Database` mode records the recovery intent and prints
the next isolated-verification step:

```powershell
.\scripts\rollback_mysql_deployment.ps1 `
  -Action Database `
  -AuditDirectory D:\WeKnoraOpsAudit `
  -BackupId weknora-mysql-YYYYMMDDTHHMMSSZ-<24-lowercase-hex-characters> `
  -BackupDirectory D:\WeKnoraBackups
```

Run `verify_mysql_restore.ps1` for that backup first. Only after its manifest,
checksum, migration state, table counts, and measured restore time have been
reviewed should an operator enter a maintenance window, take a new recovery
point, restore to a separate MySQL instance, validate it, and perform a
reviewed connection cutover. There is intentionally no one-command production
database replacement.

Run the lightweight CLI safety check after changing the rollback script:

```powershell
.\scripts\test_mysql_rollback_cli.ps1 -IncludeConfigStaging
```

The optional configuration-staging check uses only temporary environment files
and `docker compose config`; it does not start containers or alter `.env.mysql`.

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
