# 常见问题与升级排障

先在「设置 → 系统信息」确认应用版本、数据库迁移状态和依赖连接状态，再查看对应服务日志。以下 Compose 命令在仓库根目录执行；本地开发的依赖服务使用 `docker compose -f docker-compose.dev.yml`，app 日志查看宿主机终端或其日志文件，开发 Compose 没有 `app` 服务。

```bash
docker compose ps
docker compose logs --tail=200 app
docker compose logs --tail=200 docreader
docker compose logs --tail=200 postgres redis
```

修改 `.env` 后用 `docker compose up -d <服务名>` 重建对应容器；`docker compose restart` 不会重新加载容器环境变量。

## 按现象定位

| 现象 | 检查顺序与说明 |
| --- | --- |
| 上传失败、解析一直处理中 | 文档详情的失败原因与解析追踪 → 解析引擎连通性 → 后台任务队列；见[文档解析](../03-features/03-document-parsing.md)和[异步任务](../02-architecture/05-async-tasks.md) |
| 图片能在本机显示，其他设备打不开 | 检查返回地址是否包含 `localhost`、容器服务名或不可访问的对象存储地址；第三方客户端收到 `resource://` 时按[文件访问](../03-features/21-file-access.md)解析，不能直接作为图片 URL |
| 保存配置后又恢复旧值 | 检查代理/浏览器缓存是否改写响应、是否连到了其他环境或切换了空间；内置模型另查 YAML 启动同步；见[模型管理](../03-features/06-models.md) |
| 升级后提示权限不足 | 核对当前空间角色、资源归属与 API Key 能力范围；见[认证与授权](../03-features/01-tenant-auth.md)。平台管理员与空间 Owner 不是同一个概念 |
| Agent 提示模型未就绪 | 确认该智能体引用的模型仍存在且配置完整，执行模型连通性测试；见[模型管理](../03-features/06-models.md) |
| 私网数据源、模型或向量库连接被拒绝 | 检查 SSRF 校验和端口策略；按[配置参考](04-configuration.md)仅放行需要的目标 |
| 报错提示「仅允许白名单出站」 | 部署开启了 `SSRF_DNS_WHITELIST_ONLY`，不在白名单的主机在 DNS 解析前即被拒绝。把目标主机加入 `SSRF_WHITELIST` 或 `SSRF_WHITELIST_EXTRA` 后重建 app / docreader 容器，见[配置详解](04-configuration.md) |
| 登录接口返回 HTML 404 | 检查 `SEARXNG_PORT` 是否与 `APP_PORT`（默认 8080）相同。Linux 上 SearXNG 绑定 `127.0.0.1:8080` 后，`localhost:8080` 的请求会先到 SearXNG。保持 SearXNG 使用 8888 或其他空闲端口 |
| 拉取 MinIO 镜像提示 `pull access denied` | MinIO 已不再向 Docker Hub 发布镜像。当前 Compose 与 Helm 使用 `quay.io/minio/minio`；自定义编排文件需同步修改镜像地址 |
| 升级后嵌入页面或签名链接失效 | 签名密钥取 `SYSTEM_SIGNING_KEY`，未设置时回退到 `SYSTEM_AES_KEY`；示例密钥和长度不足 16 的密钥不能签名。v0.8.2 更换了嵌入会话的签名算法，升级前签发的嵌入会话全部失效，访客需重新进入；多副本部署须使用同一个密钥 |
| 升级后钉钉机器人不再回复 | v0.8.2 起钉钉只支持 Stream 模式，升级迁移会把 `webhook` 渠道改为 `websocket`。需在钉钉开发者后台为该应用开启 Stream 模式，见[IM 集成](../03-features/12-im-integration.md) |
| 后台队列持续积压 | 结合最老任务等待时间、活跃 worker 和下游配额定位；提高 worker 数不会增加模型供应商配额，见[容量估算](../02-architecture/05-async-tasks.md#capacity-planning) |
| 技能已加入目录却不可执行 | 目录收录和沙箱安装是两步；检查安装记录、智能体的沙箱选择与技能范围，见[技能目录与沙箱](../03-features/22-skills-sandbox.md) |
| 升级后找不到 Local 沙箱 | `local` 后端已移除，重新配置 Docker、CubeSandbox 或 E2B；见[沙箱部署与排障](../06-development/04-sandbox-deployment.md) |
| 本机浏览器已配对但没有执行网页任务 | 智能推理输入框需要开启本机浏览器；暂停的任务需显式继续，见[本机浏览器](../05-clients/09-local-browser.md) |
| 嵌入页面加载失败或返回 403 | 白名单填写实际宿主 Origin；检查 CSP、反向代理和安全模式 exchange 的 Origin，见[嵌入渠道](../03-features/13-embed-channel.md) |
| 飞书应用测试成功但加载不到文件夹 | 连接测试只验证应用身份，文件夹还需授权；见[飞书云盘接入](../03-features/24-feishu-drive.md) |

## 数据库迁移失败 {#database-migrations}

应用默认在启动时执行迁移。失败后仍可能继续启动，因此“页面能打开”不代表表结构已经升级成功。系统信息中的迁移版本、dirty 标记和错误，以及 app 启动日志，是排查入口。

**不要假定失败迁移已经完整回滚。** 实际状态取决于 SQL 的事务边界和失败位置。恢复前保留日志、备份数据库，核对失败版本对应的迁移文件与实际 schema；不要靠删除数据卷或直接修改版本号跳过错误。

PostgreSQL 使用 `migrations/versioned/`，SQLite 使用 `migrations/sqlite/`。下面的 Make 命令调用 PostgreSQL 迁移脚本；Lite 的 SQLite 数据库应使用对应驱动和迁移目录处理，不能套用 PostgreSQL DSN。

```bash
make migrate-version
```

也可在目标数据库中只读检查：

```sql
SELECT version, dirty FROM schema_migrations;
-- 以下两条仅用于 PostgreSQL
SELECT version();
SELECT extname, extversion FROM pg_extension;
```

### 扩展缺失或权限不足

`gin_trgm_ops` 不存在通常对应 `pg_trgm`，`vector` 类型不存在对应 pgvector，BM25 功能依赖 ParadeDB 的 `pg_search`。先确认应用实际连接的数据库，再由数据库管理员检查扩展包、数据库内扩展和对象所有权。

```sql
SELECT name, default_version, installed_version
FROM pg_available_extensions
WHERE name IN ('pg_trgm', 'vector', 'pg_search');
```

扩展文件未安装与 SQL 权限不足是不同问题；`CREATE EXTENSION IF NOT EXISTS` 不会自动安装操作系统软件包，也不会升级已经存在的扩展。只创建当前部署确实需要的扩展，避免把外部检索引擎部署误改为 PostgreSQL 检索。

ParadeDB 存量库的镜像与扩展版本升级见[ParadeDB 升级](06-paradedb-upgrade.md)。

### Dirty 状态

`AUTO_RECOVER_DIRTY` 默认开启，启动时会尝试重设迁移版本并重跑。这是重试机制，不能修复扩展缺失、磁盘不足或手工修改导致的 schema 差异。

人工恢复时，先停止应用写入，并检查失败迁移是否留下部分变更。只有确认数据库已经符合上一成功版本、且失败迁移可安全重跑后，才使用 `force`。它**只修改迁移版本标记，不执行回滚 SQL**。例如，确认上一成功版本为 98 后：

```bash
make migrate-force version=98
make migrate-up
```

这里的 98 是示例，必须换成实际确认的版本，不能机械地把报错数字减一。初始迁移失败还需单独检查初始化状态。恢复后重启 app，确认系统信息不再显示错误，并验证受影响的功能。

### 并发建索引中断

迁移 `000106` 用 `CREATE INDEX CONCURRENTLY` 为 `messages` 建索引，构建期间不阻塞写入。若构建被中断（进程重启、超时、磁盘不足），会留下一个 INVALID 的 `idx_messages_session_created_id`，`IF NOT EXISTS` 不会重建它。先确认并删除该索引，再按上文恢复迁移状态后重跑：

```sql
SELECT indexrelid::regclass, indisvalid FROM pg_index
WHERE indexrelid = 'idx_messages_session_created_id'::regclass;
DROP INDEX CONCURRENTLY IF EXISTS idx_messages_session_created_id;
```

### 磁盘不足与 Schema 差异

索引构建需要额外临时空间。遇到 `No space left on device`，检查数据库数据卷及临时目录容量，清理空间后再按实际迁移状态恢复。

列类型、索引或约束与迁移预期不一致时，对照失败文件和上一成功版本检查；不要直接在生产库反复执行报错 SQL。需要复现写操作时，先在备份恢复出的隔离数据库中验证。

## 提交问题时提供什么

提供应用版本/提交号、失败时间、完整错误、数据库类型与版本、迁移版本/dirty 状态，以及相关的非默认配置。先脱敏日志中的 Token、密码、连接串和文档内容。数据库迁移实现与开发方式见[数据库与迁移](../06-development/02-database-schema.md)。
