# WeKnora MySQL 8 实施方案

**中文版本** | [English version](MYSQL_8_IMPLEMENTATION_PLAN_EN.md)

## 1. 目标

让 MySQL 8 成为与 PostgreSQL、SQLite 同级的**可选应用数据库后端**。

用户可以通过下面的配置选择 MySQL：

```env
DB_DRIVER=mysql
```

这里的“应用数据库”负责保存用户、工作区、知识库、会话、消息、任务、配置等业务数据。
向量检索仍需使用 WeKnora 已支持的检索引擎，例如 Qdrant、OpenSearch、Milvus、Weaviate 或 Doris。
MySQL 不替代 PostgreSQL 的 pgvector 功能。

## 2. 边界

### 包含

- 支持全新的 MySQL 8 数据库部署。
- 创建当前完整业务 schema，而不是仅创建早期的少数表。
- 支持现有应用功能所需的 JSON 查询、搜索、软删除唯一约束和任务队列并发控制。
- 提供独立 MySQL Docker Compose 部署文件和中文部署说明。
- 保持 PostgreSQL 与 SQLite 代码路径、迁移文件和默认行为不变。

### 不包含

- PostgreSQL、SQLite 到 MySQL 的历史数据迁移。
- MySQL 内置向量检索或 pgvector 兼容层。
- 修改已有 PostgreSQL 数据、迁移记录或 Docker 数据卷。

## 3. 基本设计

应用启动后会根据 `DB_DRIVER` 选择不同的数据库路径：

| 配置 | 业务数据库 | 迁移来源 |
| --- | --- | --- |
| `postgres` | PostgreSQL | `migrations/versioned` |
| `sqlite` | SQLite | `migrations/sqlite` |
| `mysql` | MySQL 8 | `migrations/mysql` |

MySQL 不顺序执行 PostgreSQL 的 74 个历史迁移。原因是其中包含 `jsonb`、`pg_trgm`、`RETURNING`、PostgreSQL 索引和扩展等特定语法。

由于本需求只支持新部署，MySQL 使用一个当前版本的基线迁移：

```text
migrations/mysql/000074_baseline.up.sql
```

它一次性建立当前所需的 50 张业务表，并将迁移版本推进到 `74`。以后每一个新 schema 版本都需要同步增加对应的 MySQL 迁移文件。

## 4. 已完成内容

### 4.1 数据库启动与迁移

- 在 `internal/container/container.go` 增加 `DB_DRIVER=mysql`。
- 使用 GORM MySQL 驱动连接 MySQL，并设置 `utf8mb4`、`utf8mb4_0900_ai_ci`、UTC 时间解析。
- 在 `internal/database/migration.go` 增加 MySQL 迁移驱动与迁移目录选择。
- PostgreSQL 与 SQLite 分支保留原有实现，不与 MySQL 共用 schema 或连接串。

### 4.2 完整 MySQL 基线

- 新增 `migrations/mysql/000074_baseline.up.sql`。
- 基线覆盖 50 张当前业务表，包含任务队列、Wiki、系统设置、标签关系和处理跨度等当前功能所需的表。
- 处理 MySQL 8 的兼容要求：
  - `DATETIME(3)` 使用 `CURRENT_TIMESTAMP(3)` 默认值。
  - 被索引的长 token 使用前缀索引。
  - 外键两侧统一使用兼容的无符号整型。
  - SQLite/PostgreSQL 的部分唯一索引改为 MySQL 生成列加唯一索引，继续保证软删除后的名称复用和活动记录去重。

### 4.3 运行期 SQL 适配

- JSON 字段读取切换为 `JSON_EXTRACT` / `JSON_UNQUOTE`。
- PostgreSQL `ILIKE`、`::jsonb`、JSON 包含查询在 MySQL 路径采用等价的 `LIKE`、`CAST(... AS CHAR)`、`JSON_CONTAINS`。
- 任务队列在 MySQL 8 使用 `FOR UPDATE SKIP LOCKED`。
- `UPDATE ... RETURNING` 在 MySQL 路径使用事务内“更新后读取”实现。
- 向量库删除保护在 MySQL 与 PostgreSQL 都使用行锁，SQLite 保持原行为。

### 4.4 部署与文档

- 新增 `docker-compose.mysql.yml`：独立启动 MySQL、Qdrant、Redis、docreader、应用和前端。
- 新增 `.env.mysql.example`。
- 新增 `docs/DEPLOY_MYSQL.md`，说明部署方式、版本要求和后续迁移规则。

## 5. 已完成验证

以下验证已在隔离的 `mysql:8.4` 容器中执行：

1. 完整执行 MySQL 基线迁移。
2. 确认 50 张业务表全部创建成功。
3. 验证 JSON 提取与 JSON 包含函数可执行。
4. 验证软删除后可复用同名向量库。
5. 验证活动状态下的重复邀请会被唯一约束拒绝。
6. 验证 `docker-compose.mysql.yml` 可被 Docker Compose 解析。
7. 对改动的 Go 文件执行 `gofmt`。

## 6. 后续要求

初始 MySQL 8 后端已完成。后续变更必须遵守以下要求：

1. 每一个适用于 MySQL 的 schema 变更，均需增加对应的 MySQL migration。
2. MySQL 专属行为需有针对性测试，且不得改变 PostgreSQL 或 SQLite 的执行路径。
3. 修改部署配置后，应验证 MySQL Compose 配置。
4. 运行保障能力按 `MYSQL_8_RESILIENCE_OPERATIONS_PLAN_CN.md` 的顺序，每项功能
   使用独立分支实现、验证和提交。

## 7. 验收标准

- 在空 MySQL 8 数据库中设置 `DB_DRIVER=mysql` 后，服务能完成迁移并正常启动。
- `schema_migrations` 的版本为 `74`，且 `dirty=0`；基线创建 50 张业务表。
- 用户、工作区、知识库、会话、消息、任务队列、Wiki 和资源元数据可使用 MySQL 保存与查询。
- PostgreSQL 与 SQLite 继续选择原有的连接方式和迁移目录，不会执行 MySQL 基线。
- MySQL Compose 配置可独立运行，向量检索仍由 Qdrant 等外部检索引擎负责。

## 8. 当前验证状态（2026-07-28）

已完成并通过：

- `go mod tidy`，已将 `gorm.io/driver/mysql v1.6.0` 的校验记录写入 `go.sum`。
- `go test ./internal/database ./internal/application/repository`。
- `go test ./internal/application/service`。
- `go test ./internal/container`（临时测试镜像安装 `libsqlite3-dev` 后执行）。
- `docker compose --env-file .env.mysql.example -f docker-compose.mysql.yml config --quiet`。
- `sh scripts/test_mysql_schema.sh`：成功创建 50 张业务表，并验证 JSON 查询和软删除唯一约束。
- 完整 Docker 应用镜像构建成功。
- 在全新的 MySQL 8 容器中，应用自动写入版本 `74` 的干净迁移记录，并创建 50 张业务表。
- MySQL 应用健康检查、注册登录、知识库创建、会话创建、消息加载和关键词搜索均已验证成功。

- `DB_DRIVER=mysql` 在新的 MySQL 8 数据库中可以自动完成初始化。
- MySQL 失败不会改动或阻断 PostgreSQL、SQLite 的原有路径。
- 新部署具备完整业务表，而不是旧脚本中的少数基础表。
- 业务元数据功能、任务队列、Wiki、会话、消息、资源与向量库配置可正常使用。
- 文档清楚区分 MySQL 业务数据库和外部向量检索引擎。

## 9. 提交前说明

- PostgreSQL 和 SQLite 的连接配置、迁移目录与默认 compose 均未修改；本次改动只在 `DB_DRIVER=mysql` 时进入新分支。
- MySQL 基线仅适用于全新数据库。已有 PostgreSQL 或 SQLite 数据不会被读取、改写或迁移到 MySQL。
