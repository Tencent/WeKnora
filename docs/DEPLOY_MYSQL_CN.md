# WeKnora MySQL 8 部署指南

**中文版本** | [English version](DEPLOY_MYSQL.md)

WeKnora 可通过 `DB_DRIVER=mysql` 将 MySQL 8 用作应用业务数据库。本路径仅
支持全新部署，不会迁移或修改已有的 PostgreSQL、SQLite 数据库。

MySQL 不是向量检索引擎。请将 `RETRIEVE_DRIVER` 设置为项目已支持的检索引擎，
例如 `qdrant`、`opensearch`、`milvus`、`weaviate` 或 `doris`。随附的 Compose
配置使用 Qdrant。

## 环境要求

- MySQL 版本为 8.0.13 或更高，字符集使用 `utf8mb4`，排序规则使用
  `utf8mb4_0900_ai_ci`。
- 使用 Docker Compose v2 运行随附的部署配置。
- 使用一个全新的空 MySQL 数据库。不要将 `DB_DRIVER=mysql` 指向已有的
  WeKnora PostgreSQL 备份，或未完整初始化的 MySQL schema。

## 启动服务

以 `.env.mysql.example` 为模板创建 `.env.mysql`，并配置高强度数据库密码。
随附 Compose 文件会从当前代码目录构建应用，因此可在官方镜像包含 MySQL 支持
之前验证 fork。启动独立服务栈：

```bash
docker compose --env-file .env.mysql -f docker-compose.mysql.yml up -d
```

首次启动时，应用会执行 `migrations/mysql/000074_baseline.up.sql`，创建完整的
当前应用 schema，并在 `schema_migrations` 中记录版本 `74`。当
`DB_DRIVER=postgres` 时，PostgreSQL 继续使用原有迁移；SQLite 继续使用自己的
baseline。

## 容器日志保留

MySQL Compose 服务栈会为全部服务配置 Docker 的 `local` logging driver，包括
`frontend`、`app`、`mysql`、`redis`、`qdrant` 和 `docreader`。Docker 会在单个
stdout/stderr 日志文件达到 20 MB 时轮转，并保留三个文件，因此每个容器的本地
Docker 日志历史上限约为 60 MB。

该设置只会在创建容器时生效。如需应用到已有服务栈，请重建服务：

```bash
docker compose --env-file .env.mysql -f docker-compose.mysql.yml up -d --force-recreate
```

此限制不能替代集中式日志系统对长期审计日志的保留。

## 应用文件日志保留

设置 `LOG_PATH` 后，应用还会写入本地文件日志。它与 Docker stdout/stderr
日志独立轮转，默认安全值如下：

```env
LOG_FILE_MAX_SIZE_MB=50
LOG_FILE_MAX_BACKUPS=3
LOG_FILE_MAX_AGE_DAYS=28
LOG_FILE_COMPRESS=true
```

零、负数或格式错误的配置会回退到这些默认值，避免错误配置关闭轮转。应用启动
及重新加载日志配置时，会检查 `LOG_PATH` 所在文件系统：剩余空间低于 20% 时向
stderr 写入 warning，低于 10% 或 5 GB 时写入 critical 信号。阈值可通过
`LOG_DISK_WARNING_FREE_PERCENT`、`LOG_DISK_CRITICAL_FREE_PERCENT` 与
`LOG_DISK_MIN_FREE_GB` 调整。

这些信号不会删除日志，也不会自动触发备份。

## 监控接口

`GET /metrics` 会输出低基数 Prometheus 指标，包括 HTTP 流量、数据库和 Redis
连通性、数据库连接池、应用文件日志大小、磁盘剩余空间、构建版本和启动时记录的
迁移状态。指标标签和接口响应不会包含请求路径、用户数据、凭据或原始错误信息。

Prometheus 应直接抓取应用端口。请将 `/metrics` 作为内网接口处理：使用防火墙
或反向代理 allowlist 限制访问，不要通过公开前端路由暴露它。

`GET /api/v1/admin/operations/status` 为系统管理员提供脱敏后的运行快照，包含
依赖组件状态、连接池数值、文件日志存储状态、迁移状态和定时备份状态，但不包含
文件路径、DSN、密码或依赖服务返回的原始错误。

## 邮件告警

SMTP 告警为 opt-in 功能，接收对象应是部署操作者，而不是每个 WeKnora 用户。
应用会检查数据库和 Redis 连通性、dirty migration 状态以及配置的文件日志
文件系统。对于一个新问题，它发送首封告警；问题持续时抑制重复告警；问题恢复时
发送一封 recovery 邮件。邮件投递失败只会在 cooldown 后重试，并记录限频且脱敏的
日志。检查间隔最小为 10 秒，cooldown 最小为 60 秒，避免错误配置形成邮件或日志
风暴。

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

`SMTP_TLS_MODE` 支持 `starttls`（默认值，通常使用端口 587）、`implicit`（通常
使用端口 465）和 `none`。选择 `none` 时，应用拒绝 SMTP authentication。只能设置
`SMTP_PASSWORD` 或 `SMTP_PASSWORD_FILE` 其中之一，推荐使用挂载的 secret file。
配置错误会使邮件告警保持禁用，且不会在日志或 API 响应中暴露密码。

进程内 notifier 无法报告整个应用或主机已经宕机的情况。请在此 Compose 服务栈
之外运行独立监控工具，例如 Uptime Kuma 或 Prometheus Alertmanager，定期请求
`/readyz`，并在应用完全不可用时发送自己的通知。

## 手动备份

第一阶段备份功能有意限制为由操作者触发的 MySQL logical backup。它仅对系统管理员
开放，必须填写操作原因，默认禁用。PostgreSQL 与 SQLite 永远不会进入这一
MySQL 专属代码路径。

对于 Windows 上的 Docker Desktop，请将备份目录放在宿主机的数据盘，不要放在
Docker 虚拟磁盘或容器 layer 中：

```env
BACKUP_ENABLED=true
BACKUP_LOCAL_DIR=/data/backups
BACKUP_HOST_DIR=D:/WeKnoraBackups
BACKUP_TIMEOUT_SECONDS=900
BACKUP_MYSQLDUMP_PATH=mysqldump
```

Compose 会将 `BACKUP_HOST_DIR` 挂载到应用容器内的 `/data/backups`。请选择仅供
部署操作者读取的目录。当前阶段不会加密 archive，因此在加密目标功能完成前，
文件系统访问控制和 off-host 副本非常重要。

等待受保护请求执行完成，即可创建备份：

```bash
curl -X POST http://localhost:8080/api/v1/admin/operations/backups \
  -H "Authorization: Bearer <system-admin-token>" \
  -H "Content-Type: application/json" \
  -d '{"reason":"before a schema or deployment change"}'
```

应用通过 MySQL advisory lock 防止并行备份；第二个请求会返回 `409`，不会和首个
`mysqldump` 重叠。备份命令使用 `mysqldump --single-transaction --routines --events`，
原子写入私有 gzip archive 及相邻的 JSON manifest。manifest 会记录 archive 大小、
SHA-256、应用版本、迁移状态、trigger、reason 和最终结果。响应、manifest 和审计
事件均不包含密码、absolute path、DSN 或原始 `mysqldump` 输出。失败时会清除
partial archive，只在 manifest 与审计事件中保留安全的失败类别。

不要在操作原因中写入 secret，因为它会被写入 manifest 和系统审计日志。此 API
不提供恢复操作；下述恢复验证流程只会恢复到隔离的 MySQL 实例。

## 本地文件归档

当 `STORAGE_TYPE=local` 时，设置 `BACKUP_FILES_ENABLED=true` 可让每一次成功的
手动或定时 MySQL 备份同时归档 `LOCAL_STORAGE_BASE_DIR`（通常是 `/data/files`）。
每个文件 archive 均有相邻 inventory，记录相对文件名、大小和 SHA-256。主 backup
manifest 只记录相对的 archive/inventory 文件名、汇总 checksum 和开始/结束时间，
不记录本地文件存储的 absolute path。

```env
STORAGE_TYPE=local
LOCAL_STORAGE_BASE_DIR=/data/files
BACKUP_FILES_ENABLED=true
```

该配置不能用于 object-storage provider，也不能让文件目录与备份目录重叠。备份目标应
位于访问受控的宿主机数据盘，并且在 Docker container layer 之外。retention 会将
SQL archive、文件 archive、inventory 和主 manifest 一起删除，同时始终保护最新的
完整备份。

数据库 dump 与文件 archive 被刻意标记为**非原子 point-in-time snapshot**。各自的
开始和结束时间会写入清单，明确这一边界。如需严格 recovery point，请先在
maintenance window 中暂停上传和写入，再创建备份。不要将文件 archive 直接恢复到
正在运行的 `/data/files` 目录。

只能解压到一个新的空目录来验证文件 archive。该 PowerShell drill 会校验外层 archive
以及每一个解压文件与 inventory 的一致性，不会连接 Docker，也不会覆盖应用数据：

```powershell
.\scripts\verify_local_file_backup.ps1 `
  -BackupId weknora-mysql-YYYYMMDDTHHMMSSZ-<24-lowercase-hex-characters> `
  -BackupDirectory D:\WeKnoraBackups `
  -DestinationDirectory D:\WeKnoraRestoreDrill\files
```

## Qdrant 原生快照

Qdrant snapshot 是加快向量索引恢复的可选能力，不是唯一恢复路径。恢复 MySQL 与
本地文件后，WeKnora 可以从源数据重新构建 Qdrant index。只有在
`RETRIEVE_DRIVER=qdrant` 时才可启用原生 snapshot；其他 retriever 不会进入该路径。

```env
BACKUP_QDRANT_ENABLED=true
# 留空时，在 Compose network 中使用 http://QDRANT_HOST:6333。
BACKUP_QDRANT_URL=
```

对于每个 collection，备份会调用 Qdrant native snapshot API，将返回的 snapshot
复制到 `BACKUP_LOCAL_DIR`，计算 SHA-256 后删除 Qdrant 服务端临时 snapshot。主
manifest 记录 collection、相对的 opaque filename、大小、checksum 与时间范围，但
不记录 endpoint 或 API key。任意 collection 失败都不会留下成功的组合备份；retention
会将快照与 SQL、文件 archive、inventory 和主 manifest 一起清理。

snapshot 与 MySQL dump 不是原子 point-in-time image。需要严格 recovery point 时
请使用 maintenance window。下列 drill 会先校验 snapshot，再启动一个新的临时 Qdrant
容器，仅向该容器上传并恢复 snapshot；它不会连接正在运行的 Compose 服务：

```powershell
.\scripts\verify_qdrant_snapshots.ps1 `
  -BackupId weknora-mysql-YYYYMMDDTHHMMSSZ-<24-lowercase-hex-characters> `
  -BackupDirectory D:\WeKnoraBackups
```

## 隔离恢复验证

在依赖备份进行恢复前，请先将其恢复到一个新的临时 MySQL 8 容器并验证。这是
operational drill，而不是 production restore command：它不会启动、停止、连接或
覆盖正常的 `mysql` Compose 服务。临时服务不公开端口、不使用持久化 MySQL volume，
只读挂载备份目录，并使用独立内部 Docker network。验证结束后会自动删除。

请在仓库根目录的 PowerShell 中执行，将 BackupId 替换成手动备份 API 返回的值：

```powershell
.\scripts\verify_mysql_restore.ps1 `
  -BackupId weknora-mysql-YYYYMMDDTHHMMSSZ-<24-lowercase-hex-characters> `
  -BackupDirectory D:\WeKnoraBackups `
  -EnvFile .env.mysql
```

脚本会先检查相邻 JSON manifest、archive 字节数、SHA-256 和 gzip stream，再导入。
随后会确认只恢复出一个含有 `schema_migrations` 的应用数据库，检查 clean migration
状态及（如有记录）manifest 中的迁移版本，报告关键表的精确计数，并仅显示少量
record ID 与 timestamp。它不会打印数据库密码、DSN、manifest 中的路径或业务内容
字段。

脚本会在 process environment 中生成随机的 restore-only root password，并通过
container standard input 写入临时 MySQL client option file，不会将密码写进宿主机
命令行。仅在排查失败演练时使用 `-KeepContainer`；完成后按脚本显示的 project name
清理容器。成功验证可得到当前备份规模和主机性能下的测量恢复耗时，但不代表可以
直接替换 production database。真实 production cutover 属于独立、经过审查的
break-glass 操作。

修改该流程后，请运行可重复的 integration drill：

```powershell
.\scripts\test_mysql_restore_verify.ps1
```

该脚本会构建一个临时 MySQL source database，写入含代表性记录且符合 manifest 格式
的 gzip dump，通过隔离 profile 验证，再清除全部临时容器和文件。

## 定时备份

定时备份为 opt-in 功能：只有同时设置 `BACKUP_ENABLED=true` 和有效的
`BACKUP_SCHEDULE` 后才会启动。仅设置 Cron expression 不会意外启用备份；空值会
保持禁用。定时任务没有操作者填写的 reason，在 manifest 与审计记录中使用
`trigger=scheduled`。

个人或小团队部署可从下列配置开始。`BACKUP_SCHEDULE` 使用标准五字段 Cron 格式：
`minute hour day-of-month month day-of-week`。它按照应用容器时区运行；如果镜像或
部署未配置其他时区，通常是 UTC。生产使用前请检查容器时间：

```bash
docker compose exec app date
```

```env
# 启用 MySQL 手动与定时备份
BACKUP_ENABLED=true

# 每天 03:30
BACKUP_SCHEDULE=30 3 * * *

# 保留 7 天
BACKUP_RETENTION_DAYS=7

# 可用空间少于 5 GiB 时跳过定时备份
BACKUP_MIN_FREE_GB=5
```

每一次**成功的定时备份**完成后，retention sweep 只会删除过期且有效的
archive-manifest 成对文件。即使最新一份有效备份早于保留期限，也绝不删除它。将
`BACKUP_RETENTION_DAYS=0` 设置为零可完全关闭自动删除。手动备份和失败的备份不会
触发删除。备份盘已满时，请先确认最近一份可恢复备份，再由操作者手动释放空间或
调整 retention policy。

创建自动 archive 前，scheduler 会检查 `BACKUP_LOCAL_DIR` 的可用空间。可用空间低于
`BACKUP_MIN_FREE_GB`、MySQL 不可用、MySQL advisory lock 被占用或 dump 失败时，都
不会产生 partial success record。lock contention 被视为安全跳过而非失败，因此多个
应用实例不能同时执行 dump。PostgreSQL 和 SQLite 不会启动该 scheduler。

`GET /api/v1/admin/operations/status` 包含脱敏的定时备份状态，`/metrics` 提供
schedule enabled、last success、last failure 与 retention failure gauges。SMTP alert
启用时，定时备份失败或 retention cleanup 失败会发送一封去重邮件；后续成功执行会
发送通常的 recovery notification。告警、状态和审计记录均不包含 password、DSN、
absolute path 或原始 `mysqldump` 输出。

## Schema 检查

发布 MySQL 相关改动前，请执行可重复的 schema validation：

```bash
sh scripts/test_mysql_schema.sh
```

该脚本会启动临时 MySQL 8.4 容器，应用 baseline，检查完整表数量、JSON functions 和
soft-delete uniqueness 行为，然后删除该容器。

## 后续迁移

MySQL baseline 对应版本 `74`。每个后续 schema change 除 PostgreSQL 迁移外，也必须
添加对应的 `migrations/mysql/<version>_*.up.sql`；适用时还需要 SQLite migration。
这能确保全新 MySQL 部署可确定性升级，而不必重放 PostgreSQL 专属的历史迁移。
