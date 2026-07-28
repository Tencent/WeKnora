# WeKnora MySQL 8 运行保障与灾备方案

**中文版本** | [English version](MYSQL_8_RESILIENCE_OPERATIONS_PLAN_EN.md)

> 状态：截至 2026-07-28，12 项计划能力均已完成。SMTP 告警与定时备份仍需由部署者
> 显式启用；外部监控与主机启动策略仍属于部署责任。本方案以
> `feature/mysql-8-backend` 的 MySQL 8 支持为基础，不改变 PostgreSQL、SQLite 的既有部署路径。

## 1. 要解决的问题

MySQL 后端能够运行只是第一步。生产服务还需要在故障发生时被发现、避免日志耗尽磁盘、可以恢复数据，并且在进程或机器重启后稳定重新提供服务。

本方案覆盖五类能力：

1. 监控与告警：服务、数据库、Redis、检索引擎或磁盘出现异常时尽快通知操作者。
2. 日志防护：限制应用文件日志和 Docker 容器日志，避免磁盘被无限写满。
3. 备份与恢复：操作者可手动或定时备份；在少数安全场景下自动创建恢复点。
4. 重启与自愈：服务异常退出、Docker 或机器重启后可以重新拉起，并能区分“进程活着”和“服务可用”。
5. 回滚：安全地区分版本回滚、配置回滚、数据库恢复，避免一次误操作扩大事故。

## 2. 当前基础与缺口

| 已有能力 | 现状 | 缺口 |
| --- | --- | --- |
| HTTP 健康检查 | `/health`、`/livez` 与 `/readyz` 已分别覆盖进程存活和依赖可用性 | 整个服务栈不可用时仍需外部监控 |
| 容器重启 | MySQL Compose 的各服务使用 `restart: unless-stopped` | Docker Desktop 启动、主机断电恢复与反复失败的处置仍由部署者负责 |
| 日志与磁盘保护 | `LOG_PATH` 使用可配置的 Lumberjack 轮转；所有 MySQL Compose 服务限制 Docker 日志 | 部署者仍需按保留期选择足够的宿主机容量 |
| MySQL 迁移 | 全新 MySQL 8 可创建 50 张业务表，迁移版本为 74 | 不迁移历史 PostgreSQL 或 SQLite 数据 |
| 运维可见性 | 状态接口、Prometheus 指标、SMTP 告警与仅 SystemAdmin 可见的运维状态页均已提供 | 外部整栈监控仍由部署者负责 |
| MySQL 备份 | 已支持手动/定时逻辑备份、本地文件归档、可选 Qdrant 快照、保留策略与隔离恢复演练 | 加密、远端复制与生产库替换仍是操作者控制的流程 |

## 3. 设计原则

- **先可观测，再自动化。** 不能可靠判断故障时，不应贸然自动重启或自动恢复。
- **默认不扩大事故。** 磁盘已满、数据库不可达或怀疑数据损坏时，自动备份可能进一步消耗资源，应该优先告警。
- **备份必须可恢复。** “文件已生成”不算成功，必须有校验、清单和定期恢复演练。
- **回滚不能等同于删数据。** 版本、配置、数据库数据分别采用不同流程和权限。
- **最小权限与可审计。** 备份、恢复、回滚仅系统管理员可执行；每次操作写审计日志并保留操作者、原因、结果与关联备份 ID。
- **保持后端隔离。** MySQL 专属备份和运行检查只在 `DB_DRIVER=mysql` 启用；PostgreSQL/SQLite 的现有行为不改变。

## 4. 目标架构

```mermaid
flowchart LR
    App[WeKnora App] --> Live[/livez]
    App --> Ready[/readyz]
    App --> Metrics[/metrics]
    App --> Logger[stdout + optional rotated file log]
    App --> Backup[Backup coordinator]

    Ready --> MySQL[(MySQL)]
    Ready --> Redis[(Redis)]
    Ready --> Vector[Qdrant or other vector store]
    Metrics --> Monitor[Independent monitor]
    Monitor --> Alert[邮件；Webhook 为后续扩展]
    Logger --> DockerLog[Docker local log rotation]
    Backup --> BackupStore[Access-controlled backup destination]
    BackupStore --> Restore[Isolated restore verification]
    Restore --> MySQL
```

监控组件可以是 Uptime Kuma、Prometheus Alertmanager 或任何能定期请求 HTTP、读取指标并发送邮件/Webhook 的工具。它必须独立于 WeKnora 运行：当 WeKnora 整体崩溃时，应用自身无法可靠地通知任何人。WeKnora 只负责提供可靠信号和可选的内部事件，不把某一种监控产品写死进业务代码。

## 5. 监控与告警

### 5.1 三类探针

保留现有 `/health` 以保证兼容，并新增以下语义明确的接口：

| 接口 | 返回条件 | 用途 |
| --- | --- | --- |
| `GET /livez` | 进程和 HTTP 路由循环可响应 | Docker、负载均衡器判断进程是否卡死；不查询外部依赖 |
| `GET /readyz` | MySQL、Redis 必须可用；已配置的检索引擎按模式检查 | 流量入口判断是否可接收业务请求；失败返回 `503` |
| `GET /metrics` | 输出 Prometheus 文本指标 | 内网监控系统抓取；不包含密钥、用户内容或高基数 ID |
| `GET /api/v1/admin/operations/status` | 仅系统管理员 | 给管理界面和 CLI 提供脱敏后的运行状态、备份状态与最近告警 |

`/readyz` 不应执行昂贵查询或调用大模型。数据库采用短超时的 `SELECT 1`，Redis 使用 `PING`，向量库采用已有的轻量连接检查。依赖失败时响应中只给出组件状态和错误类别，不返回 DSN、密码或内部堆栈。

### 5.2 推荐指标

第一期只实现少量真正可行动的指标：

- `weknora_http_requests_total`、`weknora_http_request_duration_seconds`、`weknora_http_in_flight_requests`
- `weknora_dependency_up{dependency="mysql|redis|vector"}`
- `weknora_db_open_connections`、`weknora_db_in_use_connections`、`weknora_db_wait_count`
- `weknora_task_queue_depth`、`weknora_task_failures_total`
- `weknora_log_file_bytes`、`weknora_disk_free_bytes`
- `weknora_backup_last_success_timestamp`、`weknora_backup_last_result`、`weknora_backup_duration_seconds`
- `weknora_build_info`、`weknora_schema_migration_version`

指标标签只允许固定值，例如依赖名称、HTTP 方法和响应类别。不得把用户 ID、知识库 ID、会话 ID、URL 或错误全文放入标签，否则监控存储会被高基数数据拖垮。

### 5.3 告警事件与通知

首期告警以 SMTP 邮件为主，由部署者配置接收告警的运维邮箱。这里的“使用者邮箱”应指系统管理员或运维联系人，而不是所有注册用户的邮箱；否则一次基础设施故障可能造成群发、泄露运行状态或被滥用为垃圾邮件出口。

```env
OPS_ALERT_EMAIL_ENABLED=true
OPS_ALERT_EMAIL_TO=operator@example.com
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USERNAME=
SMTP_PASSWORD_FILE=
SMTP_FROM=weknora-alerts@example.com
```

SMTP 密码只从环境变量或挂载的密钥文件读取，绝不保存到数据库、备份清单或日志。邮件是通知渠道，不替代独立监控；建议 Uptime Kuma 或同类工具定期检查 `/readyz`，由它在 app 完全失联时发送邮件。后续可保留通用 Webhook 扩展点，方便接入飞书、钉钉、企业微信、Slack 或自建告警平台：

```env
OPS_ALERT_WEBHOOK_ENABLED=false
OPS_ALERT_WEBHOOK_URL=
OPS_ALERT_WEBHOOK_SECRET=
OPS_ALERT_COOLDOWN_SECONDS=300
OPS_ALERT_TIMEOUT_SECONDS=5
```

告警事件至少包括：服务从就绪变为未就绪、MySQL 迁移 dirty、连续备份失败、备份恢复校验失败、磁盘余量低、日志轮转失败、任务死信队列快速增长、反复重启和恢复成功。

通知必须具备“首次异常、冷却期内去重、恢复通知”三种状态；Webhook 失败只记录有限次数的错误日志，不能因为通知失败形成日志风暴。Webhook 地址由环境变量或系统管理员配置，禁止普通用户提交任意 URL。

## 6. 日志与磁盘保护

日志上限应有两层，任何一层缺失都可能导致磁盘写满。

### 6.1 应用文件日志

保留现有 `LOG_PATH` 行为，但将固定参数改为可配置且有安全默认值：

```env
LOG_PATH=
LOG_FILE_MAX_SIZE_MB=50
LOG_FILE_MAX_BACKUPS=3
LOG_FILE_MAX_AGE_DAYS=28
LOG_FILE_COMPRESS=true
```

这只约束应用主动写入 `LOG_PATH` 的文件。达到单文件上限时应轮转并压缩旧文件，而不是直接停止所有日志。审计日志、错误日志和普通调试日志应采用不同级别；磁盘告急时只允许限流或丢弃低价值调试日志，绝不能静默丢弃错误和审计事件。

### 6.2 Docker 标准输出日志

Docker 默认的 `json-file` 驱动可能无限增长，这是当前最重要的遗漏。MySQL Compose 应改用 Docker `local` 日志驱动，或明确配置 `json-file` 的轮转上限：

```yaml
logging:
  driver: local
  options:
    max-size: "20m"
    max-file: "3"
```

对 app、mysql、redis、qdrant、docreader 和 frontend 都要配置。部署文档需要明确：这会限制容器历史日志长度，长期审计应交给外部日志平台，而不是依赖 Docker 本机无限保留。

### 6.3 磁盘阈值与降级

建议默认监控以下阈值，允许环境变量覆盖：

| 阈值 | 行为 |
| --- | --- |
| 可用空间低于 20% | 告警 |
| 可用空间低于 10% | 高优先级告警，禁止启动新的自动备份 |
| 可用空间低于 5% | 就绪状态失败或降级；暂停大文件上传和非必要后台任务，保留读请求与关键日志 |

阈值应同时考虑绝对容量，例如低于 5 GB 时也触发。只有明确判断为“磁盘告急”时才能降级，不能用自动清空日志或删除备份的方式掩盖问题。

## 7. 备份与恢复

### 7.1 备份范围分级

一个完整的 WeKnora 恢复不只有 MySQL，因此按阶段实现：

| 级别 | 内容 | 首期是否实现 |
| --- | --- | --- |
| L1：MySQL 元数据 | MySQL 业务库、迁移记录、校验清单 | 已实现：手动/定时逻辑备份与隔离恢复演练 |
| L2：本地上传文件 | 本地 `/data/files` | 已实现：可选本地归档、inventory 与验证演练 |
| L3：检索数据 | Qdrant 原生快照 | 已实现：可选且默认关闭；可由原始文件重新构建 |

对于当前以本机 Docker Compose 运行、默认本地文件存储的环境，推荐备份 **MySQL + `/data/files`**。这样可以恢复用户上传的原始文件，并在需要时重新构建 Qdrant 索引。Qdrant 快照已作为可选功能实现：它能缩短恢复后的重新索引时间，但会增加存储成本；其他向量引擎不进入 Qdrant 专属快照路径。Redis 缓存不纳入备份，任务由应用的重试和死信机制处理。

### 7.2 触发方式

操作者可以选择是否开启备份，定时任务默认关闭。当前实现只有两种触发方式：

1. 手动备份：系统管理员从受保护的管理接口或运维状态页创建，需填写原因。
2. 定时备份：仅在 `BACKUP_ENABLED=true` 且显式设置有效的五字段 Cron 表达式后启用。

发布前恢复点和事件触发的自动备份不是当前功能；高风险操作前应由操作者手动创建并
验证一份备份。服务异常退出不等于数据即将丢失，因此不应把任意故障解释为自动备份的
信号。在 MySQL 不可达、磁盘已满、疑似数据损坏或连续备份失败时，自动备份可能扩大
事故，应发送高优先级邮件告警并交由操作者处理。

### 7.3 L1 MySQL 备份设计

- 使用 `mysqldump --single-transaction --routines --events`，适用于 InnoDB 的一致性逻辑备份，避免不必要的全库锁表。
- 每次生成不可变的备份 ID、压缩包和 JSON 清单。清单记录应用版本、迁移版本、创建时间、文件大小、SHA-256、触发原因和结果。
- 当前实现仅写入 `BACKUP_LOCAL_DIR`，该目录必须映射到容器临时层之外、访问受控的
  宿主机目录；对象存储与远端复制属于后续部署方案。
- 当前实现不加密备份文件；需要静态加密时，应使用受控文件系统和加密的异机副本。
- 同时只允许一个备份任务运行，使用 MySQL advisory lock 防止并发覆盖。

当前可用的配置为：

```env
BACKUP_ENABLED=false
BACKUP_SCHEDULE=
BACKUP_RETENTION_DAYS=7
BACKUP_LOCAL_DIR=/data/backups
BACKUP_MIN_FREE_GB=5
BACKUP_FILES_ENABLED=false
BACKUP_QDRANT_ENABLED=false
```

本机部署时，`BACKUP_LOCAL_DIR` 应映射到 Docker 数据盘之外的宿主机目录，例如 `D:/WeKnoraBackups`，而不是容器临时层或 Docker 的默认虚拟磁盘。仅放在同一块本地硬盘只能防范误删和软件故障，不能防范硬盘损坏、电脑丢失或勒索软件；有条件时应再复制到 OSS、COS、S3、NAS 或其他受控的异机存储。

### 7.4 恢复与演练

MySQL 逻辑备份与文件归档不是天然的同一时刻快照。首期需要在清单中记录二者开始、结束时间和包含范围；对要求严格一致性的恢复，恢复前应进入短暂维护/只读模式，暂停上传和写入任务后再创建恢复点。

恢复操作必须先恢复到**新的隔离 MySQL 实例**，完成下列检查后才允许切换：

1. 验证 SHA-256 和 SQL 导入结果。
2. 校验 `schema_migrations`、关键表数量、外键和抽样业务记录。
3. 用只读方式启动同版本应用并运行 `/readyz`。
4. 记录恢复耗时，得到实际 RTO；记录最近可恢复点，得到实际 RPO。

正式替换生产库必须是两人确认或明确的 break-glass 流程。恢复不是“撤销最后一次操作”，它会覆盖目标数据库的数据，必须在管理界面显示数据时间点、影响范围和不可逆提示。

## 8. 重启与自愈策略

### 8.1 Docker Compose 层

现有 `restart: unless-stopped` 应继续保留，它能在 Docker 守护进程或机器重启后拉起服务，也尊重操作者手动停止的决定。还需要：

- 为所有核心服务设置健康检查和合理的 `start_period`、`interval`、`timeout`、`retries`。
- app 只在 MySQL 健康后启动；启动后仍通过 `/readyz` 处理依赖后续重启。
- 生产部署使用固定镜像版本或不可变 digest，不能直接依赖 `latest`。
- 把 MySQL、Redis、Qdrant、文件和备份目录都放入命名卷或明确的宿主机目录。

Docker 的健康检查本身不会因“unhealthy”自动重启容器。因此不建议把临时网络故障直接解释为应用崩溃。应用需要有依赖重连与指数退避；只有进程无法恢复的内部致命状态才退出并交给 Docker 重启。

### 8.2 主机层

建议为 Linux 部署补充 systemd 运行单元，负责系统启动后执行 `docker compose up -d`，并设置启动频率限制，避免无限 crash loop。关键参数包括 `Restart=on-failure`、`RestartSec`、`StartLimitBurst` 与 `StartLimitIntervalSec`。

重启后应自动执行启动自检：迁移版本、数据库连接、Redis、检索引擎、可写存储目录和备份目录。检查失败时 app 可以存活以便暴露指标，但必须保持 `/readyz=503` 并发送告警，不能对外伪装成可用。

## 9. 回滚策略

“回滚”至少有四种含义，必须在界面和 API 中分开：

| 类型 | 推荐做法 | 是否自动 |
| --- | --- | --- |
| 发布版本回滚 | 切换到上一个已验证镜像 digest，再运行兼容性检查 | 否 |
| 配置回滚 | 为可管理配置保存修订版本，选择历史修订并审计 | 否 |
| 数据库恢复 | 从指定备份恢复到隔离实例，验证后人工切换 | 否 |
| Schema 降级 | 仅为后续可逆迁移提供 `.down.sql`；初始基线和破坏性变更使用备份恢复 | 否 |

数据库迁移采用“扩展 -> 双写/兼容 -> 切换 -> 收缩”的发布方式。旧版本在新 schema 上仍能运行时，才允许简单镜像回退；一旦迁移删除或改变了语义，必须依赖备份恢复，不能假装有安全的一键降级。

所有回滚入口必须满足：系统管理员权限、二次确认、关联备份 ID、维护模式、操作审计、完成后 `/readyz` 验证以及恢复结果通知。

已实现的 rollback CLI 为每次调用新建一份脱敏 JSON 审计记录，并拒绝含糊或不兼容的
操作。镜像回退只接受非 `latest` tag、已批准的 app/UI/docreader repository digest，且
目标 migration version 必须与当前干净 MySQL migration state 完全一致；它只重建应用
侧服务，关闭自动迁移后等待应用健康。配置回退会验证并暂存已审核文件，但不会自动
重启服务。数据库模式只记录意图并指向隔离恢复验证演练，刻意没有覆盖运行中数据库
的命令。

## 10. 推荐实施顺序

下面保留的是本次已完成工作的实施顺序，以及后续能力扩展的参考。第 1 至第 12 项
均已完成；通用 Webhook、发布前自动备份、加密和对象存储仍只是未来建议，不应当作
当前环境变量或产品能力使用。

### P0：先降低事故概率

1. 增加 `/livez`、`/readyz`，保留 `/health` 兼容。
2. 为 MySQL Compose 全部服务加 Docker 日志轮转上限。
3. 将文件日志轮转参数环境变量化，增加日志轮转失败和磁盘阈值告警。
4. 补充生产部署、重启和故障排查文档。

### P1：让操作者看见并收到通知

1. 增加低基数 Prometheus 指标。
2. 增加受保护的运行状态接口。
3. 增加通用 Webhook 告警、去重与恢复通知。
4. 为数据库、Redis、磁盘、任务队列、迁移状态建立告警规则示例。

### P2：可验证的本机备份

1. 实现仅系统管理员可触发的 L1 手动备份。
2. 在使用本地文件存储时，连同 `/data/files` 生成受校验的文件清单与归档。
3. 实现清单、哈希、压缩、加密、保留和并发锁。
4. 实现隔离恢复校验命令与恢复演练测试。
5. 在确认备份目标和 RPO 后加入可选定时备份。

### P3：受控恢复与更完整灾备

1. 增加发布前恢复点和 break-glass 数据库恢复编排。
2. 增加配置修订与回滚记录。
3. 按实际检索引擎实现 Qdrant 等快照；对本地文件存储补充一致性快照策略。
4. 视使用场景加入管理界面；首版优先 CLI/API，避免高危操作埋在普通页面中。

## 11. 测试与验收

| 场景 | 预期结果 |
| --- | --- |
| app 进程正常、MySQL 不可用 | `/livez=200`，`/readyz=503`，产生一次告警 |
| MySQL 恢复 | `/readyz=200`，产生恢复通知 |
| stdout 连续大量写入 | Docker 日志文件按配置轮转，宿主机磁盘不被无限占用 |
| `LOG_PATH` 大量写入 | 文件日志保留数、压缩和最大容量符合配置 |
| 磁盘空间不足 | 不启动定时备份并触发高优先级告警，由操作者处理磁盘空间 |
| 手动备份 | 生成可校验、含清单的备份，审计记录完整 |
| 恢复演练 | 能恢复到隔离 MySQL，迁移版本和关键数据校验通过 |
| app、MySQL、Docker 守护进程重启 | 服务自动恢复，数据卷不丢失，最终 `/readyz=200` |
| 版本回退 | 仅在 schema 兼容时允许；不兼容时明确阻止并指向备份恢复流程 |
| PostgreSQL/SQLite 启动 | 不加载 MySQL 备份或迁移分支，原健康接口保持可用 |

## 12. 针对当前环境的推荐默认值

### 12.1 部署与告警

- 以 Windows 上的 Docker Desktop + 单机 Docker Compose 为首期目标，不在本轮引入 Kubernetes。
- 使用 SMTP 邮件告警；允许系统管理员配置一个或多个运维收件人。
- 本地备份目录建议为 `D:/WeKnoraBackups`，通过 Compose 挂载为容器内的 `/data/backups`，与 Docker 的日志和镜像数据隔离。

### 12.2 RPO、RTO 与备份档位

RPO 是“最多允许丢失多久的数据”，RTO 是“从故障到恢复服务最多允许多久”。它们是恢复目标而不是代码承诺，会受数据库大小、磁盘速度、备份位置和故障类型影响；只有定期恢复演练测得的结果才是可信结果。下面的数值是单机个人/小团队环境合理的起点。

| 档位 | 触发方式与保留 | 目标 RPO | 目标 RTO | 适用场景 |
| --- | --- | --- | --- | --- |
| 手动 | 仅操作者手动创建；重要操作前手动创建恢复点 | 无固定保证 | 视最近备份而定 | 本地试用、开发 |
| 标准（推荐） | 每天 03:30 备份；保留最近 7 份；高风险操作前手动创建恢复点 | 24 小时 | 2 小时内 | 个人部署、小型知识库 |
| 加强 | 每 6 小时备份；本地保留 14 天；异机副本由外部工具管理 | 6 小时 | 2 小时内 | 数据更新频繁、多人共用 |

这些是使用者在 `.env.mysql` 中自行选择的建议值，当前没有安装向导或网页配置入口。
任何档位都必须由管理员确认备份目录、可用磁盘空间和收件人邮箱后再启用。

### 12.3 关于问题 5：Qdrant 与上传文件是否首期备份

建议顺序如下：

1. **必须优先保证 MySQL。** 用户、权限、知识库、会话、消息、任务和配置的元数据都在这里。
2. **本地文件存储时，首期同时备份 `/data/files`。** 它保存用户原始文档；只备份 MySQL 会导致知识库记录存在但原文件无法重新解析。
3. **Qdrant 快照已经是可选能力。** 丢失 Qdrant 索引通常可由 MySQL 元数据和原文件重新构建，但耗时并可能再次消耗模型调用成本。频繁使用大知识库时，再启用 `BACKUP_QDRANT_ENABLED=true`。
4. **Redis 不备份。** Redis 在本项目中不应作为唯一业务事实来源，恢复后让任务队列和缓存自行重建即可。

### 12.4 关于问题 6：前端与 CLI 的边界

建议将“查看”和“高风险写操作”分开：

| 入口 | 建议功能 | 原因 |
| --- | --- | --- |
| 管理前端（仅系统管理员） | 依赖、数据库、迁移、日志磁盘与定时备份摘要；创建手动备份 | 当前运维状态页已实现；创建备份需要原因、二次确认与审计，不显示备份列表也不编辑保留策略 |
| CLI / 运维命令 | 恢复到新实例、切换数据库、版本回退、删除备份、修改加密密钥 | 操作影响大，需要明确参数、维护窗口和完整日志，不适合普通页面的一键按钮 |
| 自动任务 | 已确认的定时备份、发布前恢复点、健康检查与通知 | 规则稳定且可审计；不自动执行数据恢复 |

这样既能让你在网页上方便查看和手动备份，又不会把“覆盖数据库”这类动作暴露成容易误点的功能。

## 13. 分支实施路线与质量门槛

功能不应一次性堆到同一个分支中。当前 `feature/mysql-8-resilience` 分支只保存本方案；后续每项能力都从已验证的集成基线创建独立分支，完成后再合并。建议顺序如下：

| 顺序 | 分支建议 | 只做的事情 | 合并前必须通过 |
| --- | --- | --- | --- |
| 0 | `feature/mysql-8-resilience` | 方案文档与执行清单 | 文档评审；不改运行代码 |
| 1 | `feature/mysql-8-ops-health` | 已完成：`/livez`、`/readyz`、依赖短超时检查；保持 `/health` 兼容 | 单元测试、MySQL 依赖断开/恢复测试、PostgreSQL/SQLite 回归 |
| 2 | `feature/mysql-8-docker-log-rotation` | 已完成：Compose 中所有服务的 Docker 日志上限 | Compose 配置检查、连续输出验证轮转、磁盘不无限增长 |
| 3 | `feature/mysql-8-app-log-limits` | 已完成：`LOG_PATH` 轮转参数配置化、磁盘阈值信号 | 日志轮转、错误配置、低磁盘阈值测试 |
| 4 | `feature/mysql-8-ops-metrics` | 已完成：低基数指标与运行状态接口 | 指标格式测试、无敏感字段、高基数防护检查 |
| 5 | `feature/mysql-8-email-alerts` | 已完成：SMTP 邮件、告警去重和恢复通知 | SMTP 测试服务器、冷却期、通知失败不形成日志风暴 |
| 6 | `feature/mysql-8-backup-manual` | 已完成：MySQL 手动备份、清单、哈希、权限与审计 | 空库与有数据备份、校验失败、权限测试 |
| 7 | `feature/mysql-8-restore-verify` | 已完成：隔离恢复验证 CLI/运维流程 | 从备份恢复到新 MySQL、版本/数据抽样检查 |
| 8 | `feature/mysql-8-backup-schedule` | 已完成：可选定时备份、保留策略、备份锁 | 重复触发、失败告警、过期清理、不会删除最新可用备份 |
| 9 | `feature/mysql-8-file-backup` | 已完成：本地 `/data/files` 归档和一致性边界 | 文件清单、缺失文件、维护模式下恢复演练 |
| 10 | `feature/mysql-8-qdrant-snapshot` | 已完成：可选 Qdrant 快照、隔离恢复演练与重新索引回退路径 | 快照、恢复、重新索引回退路径 |
| 11 | `feature/mysql-8-rollback-cli` | 发布/配置回退与 break-glass 恢复编排 | 已完成：兼容性阻止、不兼容时指向恢复流程、完整审计 |
| 12 | `feature/mysql-8-ops-admin-ui` | 只读状态页与受控手动备份界面 | 已完成：RBAC、二次确认、审计、无恢复一键按钮 |

每个功能分支都遵守同一质量门槛：

1. 先写清楚本分支的目标、非目标和失败行为，不顺带重构无关代码。
2. 覆盖新增逻辑的单元测试；涉及 Docker、MySQL 或恢复时补充可重复的容器集成测试。
3. 验证 PostgreSQL 与 SQLite 没有进入 MySQL 专属分支，避免影响现有用户。
4. 更新对应部署文档、环境变量样例和故障处理说明。
5. 先在独立环境演练失败和恢复，再合并到集成基线；下一个功能从最新已验证基线创建分支。

外部监控工具不属于应用代码分支，但应从第 1 项开始同步配置。推荐先使用 Uptime Kuma 之类的独立服务轮询 `/readyz` 并发送 SMTP 邮件，后续再接入 Prometheus 指标。

## 14. 本方案额外补充的建议

- 为部署生成版本清单：镜像 digest、Git 提交、迁移版本、配置版本和备份 ID 应能关联到同一次发布。
- 为高风险管理操作设置维护模式，临时拒绝写请求，降低恢复期间的数据分叉风险。
- 定期执行恢复演练，而不是只检查备份文件存在；“未经恢复验证的备份”等同于未验证。
- 对外暴露的健康接口和指标接口应限制在内网、反向代理白名单或独立监控网络，避免泄露运行状态。
- 对备份失败、日志轮转失败和频繁重启本身设置告警，防止监控系统只报告最终宕机。
