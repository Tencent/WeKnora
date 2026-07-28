# WeKnora MySQL 8 Resilience and Disaster-Recovery Plan

[Chinese version](MYSQL_8_RESILIENCE_OPERATIONS_PLAN_CN.md) | **English version**

> Status: implementation is in progress. Capabilities 1 through 8 are
> complete. This plan builds on `feature/mysql-8-backend` and does not change
> the existing PostgreSQL or SQLite deployment paths.

## 1. Problem Statement

Making the MySQL backend run is only the first step. A production service must
make failures visible, avoid filling the disk with logs, preserve recoverable
data, and return to service predictably after a process or host restart.

This plan covers monitoring and alerting, log and disk protection, backup and
recovery, restart and self-healing, and controlled rollback.

## 2. Current Foundation and Gaps

| Capability | Current state | Remaining gap |
| --- | --- | --- |
| Health checks | `/health`, `/livez`, and `/readyz` cover process and dependency state | External monitoring still belongs outside the app |
| Container restart | MySQL Compose services use `restart: unless-stopped` | Host-level boot and restart escalation remain deployment-owned |
| Log rotation | Application file logs and Docker service logs have bounded rotation | Operators must select storage capacity appropriate to retention |
| MySQL migration | A fresh MySQL 8 database creates the complete schema at version 74 | No migration of historical PostgreSQL or SQLite data |
| Operations visibility | Status endpoint, Prometheus metrics, and SMTP alerts are available | Operator-facing administration UI remains planned |
| MySQL backups | Manual backup, isolated restore verification, scheduling, and retention are available | File and vector-store backup remain planned |

## 3. Design Principles

- **Observe before automating.** Do not automate restart or recovery until a
  failure can be diagnosed reliably.
- **Do not amplify an incident by default.** A full disk, unavailable database,
  or suspected corruption should alert first; an automatic action must not make
  resource exhaustion worse.
- **A backup is useful only when it can be restored.** Archives need checksums,
  manifests, and regular isolated restore drills.
- **Rollback is not deletion.** Application version, configuration, and
  database recovery require separate procedures and authority.
- **Use least privilege and auditability.** Backup, restore, and rollback need
  administrator-only access and an audit trail containing actor, reason,
  outcome, and related backup ID.
- **Keep database backends isolated.** MySQL-specific paths activate only with
  `DB_DRIVER=mysql`; PostgreSQL and SQLite behavior remains unchanged.

## 4. Target Architecture

```mermaid
flowchart LR
    App[WeKnora application] --> Live[/livez]
    App --> Ready[/readyz]
    App --> Metrics[/metrics]
    App --> Logs[stdout and rotated file log]
    App --> Backup[Backup coordinator]
    Ready --> MySQL[(MySQL)]
    Ready --> Redis[(Redis)]
    Ready --> Vector[Vector store]
    Metrics --> Monitor[Independent monitor]
    Monitor --> Alert[Email or webhook]
    Logs --> DockerLog[Docker log rotation]
    Backup --> BackupStore[Backup destination]
    BackupStore --> Restore[Isolated restore verification]
```

Uptime Kuma, Prometheus Alertmanager, or another monitor must run independently
from WeKnora. When the application or host is completely unavailable, the
application cannot reliably notify anyone itself. WeKnora exposes safe signals;
it does not hard-code one monitoring product into business logic.

## 5. Monitoring and Alerting

### 5.1 Probes

| Endpoint | Success condition | Intended use |
| --- | --- | --- |
| `GET /livez` | Process and HTTP router respond | Docker or load balancer process-liveness decision; no dependency query |
| `GET /readyz` | MySQL, Redis, and configured vector-store checks pass | Admission of business traffic; returns `503` on failure |
| `GET /metrics` | Prometheus text metrics | Internal monitoring scrape only |
| `GET /api/v1/admin/operations/status` | Sanitized administrator status snapshot | Admin UI and CLI consumers |

Readiness checks use short operations such as `SELECT 1` and Redis `PING`; they
must not execute expensive queries or invoke models. Responses expose a
component state and safe error class only, never a password, DSN, or stack trace.

### 5.2 Metrics

The initial metric set is intentionally small and low-cardinality:

- `weknora_http_requests_total`, `weknora_http_request_duration_seconds`, and
  `weknora_http_in_flight_requests`
- `weknora_dependency_up`, database connection-pool metrics, and migration state
- `weknora_log_file_bytes` and `weknora_disk_free_bytes`
- scheduled-backup enabled, last-success, last-failure, and retention-failure
  gauges
- `weknora_build_info`

Metric labels must be fixed, bounded values. Do not use user IDs, knowledge-base
IDs, session IDs, URLs, or raw error text as labels.

### 5.3 Notifications

SMTP alerts are configured by the deployment operator and target the on-call or
administrator address, not every registered user. Every condition follows the
same lifecycle: first alert, cooldown-based deduplication, and one recovery
notification. Notification failures are rate-limited so an unavailable SMTP
service cannot create a log storm.

Alerts currently include dependency failures, dirty migrations, file-log disk
pressure, scheduled backup failures, and backup-retention failures. A generic
webhook remains a future extension point for Feishu, DingTalk, WeCom, Slack, or
another alerting system. Webhook destinations must come from trusted deployment
configuration, never from ordinary users.

## 6. Log and Disk Protection

### 6.1 Application file logs

When `LOG_PATH` is configured, Lumberjack rotates local logs. Size, backup
count, maximum age, and compression are configurable with safe fallbacks. The
application checks the target filesystem at startup and configuration reload;
warning and critical thresholds expose a safe signal without automatically
deleting evidence.

### 6.2 Docker stdout and stderr logs

All services in `docker-compose.mysql.yml` use Docker's `local` logging driver
with bounded size and file count. The policy takes effect when containers are
created, so existing stacks must be recreated after changes.

### 6.3 Disk-pressure response

The preferred response is progressive: warn, alert, stop discretionary scheduled
backup work when its configured minimum free space is unavailable, and require
operator action before deleting archives or logs. Backup retention deletes only
valid archive-manifest pairs and retains the newest valid backup.

## 7. Backup and Recovery

### 7.1 Backup scope tiers

| Tier | Data | Current direction |
| --- | --- | --- |
| L1 | MySQL business data | Manual and scheduled logical backup with checksum and manifest |
| L2 | Local uploaded files under `/data/files` | Planned file inventory and recovery workflow |
| L3 | Vector-store data such as Qdrant | Planned optional snapshot, restore, and reindex fallback |

Redis is not treated as the sole source of business truth. It can rebuild caches
and queues after recovery.

### 7.2 Trigger modes

Manual backup is administrator-only and requires a reason. Scheduled backup is
opt-in through `BACKUP_ENABLED=true` plus a five-field `BACKUP_SCHEDULE`; it has
no operator-supplied reason. Repeated jobs are guarded both within the scheduler
and by the MySQL advisory lock across application instances. Lock contention is
a safe skip rather than a failure.

### 7.3 L1 MySQL backup behavior

The MySQL backup uses `mysqldump --single-transaction --routines --events`, a
private gzip archive, atomic writes, and an adjacent JSON manifest. The manifest
records a backup ID, archive checksum and size, application version, migration
state, trigger, and safe outcome. It excludes credentials, DSNs, absolute paths,
and raw command output. After a successful scheduled backup, retention removes
expired valid pairs while never deleting the newest valid backup.

### 7.4 Restore and drills

The restore-verification script checks the manifest, archive size, SHA-256, and
gzip stream, then imports into a temporary isolated MySQL container. It never
overwrites the production MySQL service. Run a restore drill regularly, record
the measured duration, and use that measurement to tune RTO expectations.

For a small Windows and Docker Desktop deployment, begin with daily scheduled
backups, a seven-day retention window, and a 5 GiB minimum-free-space threshold.
These values are defaults, not universal policy: each operator can modify them
in `.env.mysql` to match available storage, change rate, RPO, and RTO.

## 8. Restart and Self-Healing

### 8.1 Docker Compose layer

Use `restart: unless-stopped` for services that should return after Docker or
host startup. Liveness answers whether the process responds; readiness answers
whether the application can accept traffic. Do not make a failed dependency
check repeatedly restart an otherwise healthy application process, because that
can hide the cause and increase instability.

### 8.2 Host layer

Docker Desktop startup, Windows logon/startup behavior, disk availability, and
host power recovery are deployment responsibilities. The deployment guide should
be paired with an external monitor that alerts when the entire Compose stack is
unavailable. Repeated restart failures require an operator investigation, not
unbounded automatic restart attempts.

## 9. Rollback Strategy

Use separate procedures for each rollback type:

1. **Application image rollback:** redeploy a previously known-good image or
   Git commit only after checking database migration compatibility.
2. **Configuration rollback:** restore a reviewed `.env` version and recreate
   affected containers. Secrets stay in protected secret files, not Git.
3. **Database recovery:** use a verified backup to restore a new isolated
   database first, validate it, enter maintenance mode, then perform a reviewed
   cutover. Never expose this as a one-click web action.

When a migration is irreversible, an application rollback may require a
compatible forward fix or a database restore instead. The rollback CLI planned
below must refuse ambiguous or incompatible operations and create a complete
audit event.

## 10. Recommended Implementation Order

The implementation uses one feature per branch so that each risk boundary is
reviewable and reversible:

| No. | Branch | Scope | Status |
| --- | --- | --- | --- |
| 1 | `feature/mysql-8-ops-health` | Liveness, readiness, dependency timeouts | Complete |
| 2 | `feature/mysql-8-docker-log-rotation` | Compose log limits | Complete |
| 3 | `feature/mysql-8-app-log-limits` | Configurable application log limits | Complete |
| 4 | `feature/mysql-8-ops-metrics` | Low-cardinality metrics and status | Complete |
| 5 | `feature/mysql-8-email-alerts` | SMTP alerts, deduplication, recovery | Complete |
| 6 | `feature/mysql-8-backup-manual` | Manual MySQL backup, manifest, audit | Complete |
| 7 | `feature/mysql-8-restore-verify` | Isolated restore verification | Complete |
| 8 | `feature/mysql-8-backup-schedule` | Scheduling, retention, backup locking | Complete |
| 9 | `feature/mysql-8-file-backup` | Local file archive and consistency boundaries | Planned |
| 10 | `feature/mysql-8-qdrant-snapshot` | Optional Qdrant snapshot and restore | Planned |
| 11 | `feature/mysql-8-rollback-cli` | Deployment/config rollback and break-glass recovery | Planned |
| 12 | `feature/mysql-8-ops-admin-ui` | Read-only status and controlled manual backup UI | Planned |

## 11. Quality Gates

Every feature branch must:

1. Define its goal, non-goals, safe failure behavior, and permission boundary.
2. Add focused unit tests; add repeatable Docker/MySQL integration tests when
   containers, backups, or recovery are involved.
3. Prove that PostgreSQL and SQLite do not enter the MySQL-specific path.
4. Update the paired Chinese and English deployment/operations documentation
   and environment examples.
5. Exercise failure and recovery in an isolated environment before merging into
   the verified baseline.

## 12. Interface Boundaries

The administrator UI may show health state, recent alerts, disk state, backup
inventory, and a controlled manual-backup action with confirmation and audit.
Destructive or high-impact operations such as restoring into a new instance,
switching databases, deleting archives, changing encryption keys, and rollback
belong to a reviewed CLI or operational procedure. Automated work is limited to
safe, audited scheduled backups and health/notification checks; it must never
perform an automatic production database restore.

## 13. Additional Recommendations

- Record an application image digest, Git commit, migration version,
  configuration version, and backup ID for every release.
- Use maintenance mode for high-risk recovery actions to avoid data divergence.
- Treat an untested backup as unverified; schedule restore drills rather than
  only checking that archive files exist.
- Restrict health and metrics endpoints to internal networks or a monitoring
  allowlist.
- Alert on backup failure, log-rotation failure, and repeated restarts, rather
  than only on the final service outage.
