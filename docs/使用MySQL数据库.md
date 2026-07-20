# 使用 MySQL 作为主数据库

WeKnora 默认使用 PostgreSQL 作为关系型主库。从本版本起，也支持使用 **MySQL 8.0** 作为主库的替代方案（对应 issue #1418）。

本文说明两者的区别、切换步骤，以及已知限制。

## 架构说明：关系层与向量层是解耦的

WeKnora 有两条独立的数据链路，由两个环境变量分别控制：

| 变量 | 作用 | 可选值 |
| --- | --- | --- |
| `DB_DRIVER` | 关系型主库（租户、知识库、会话、任务等元数据） | `postgres` / `mysql` / `sqlite` |
| `RETRIEVE_DRIVER` | 向量检索引擎（embedding 存储与相似度检索） | `postgres`（pgvector）/ `milvus` / `qdrant` / `weaviate` / `doris` / `elasticsearch_*` / `tencent_vectordb` |

默认部署里两者都用 PostgreSQL：主库是 Postgres，向量库复用同一个 Postgres 的 pgvector 扩展。

**MySQL 没有等价的向量能力**，因此当 `DB_DRIVER=mysql` 时，`RETRIEVE_DRIVER` 必须指向一个外部向量库（推荐 `milvus` 或 `qdrant`）。关系层与向量层互不影响。

## 切换步骤

### 1. 修改 `.env`

```dotenv
# 关系型主库改为 MySQL
DB_DRIVER=mysql
DB_HOST=mysql          # Docker Compose 部署用服务名；本地开发用 localhost
DB_PORT=3306           # MySQL 默认端口
DB_USER=<your_user>
DB_PASSWORD=<your_password>
DB_NAME=WeKnora

# 向量库改为外部引擎（pgvector 在 MySQL 模式下不可用）
RETRIEVE_DRIVER=milvus
MILVUS_ADDRESS=milvus:19530
```

> 密码可以包含 `@`、`#`、`!` 等特殊字符——连接串通过 `go-sql-driver` 的 `Config.FormatDSN()` 构造，会正确转义。

### 2. 启动 MySQL 与向量库

**Docker Compose（生产/完整部署）**

`docker-compose.yml` 已内置 `mysql` 服务。按需启动 MySQL + Milvus：

```bash
docker compose up -d mysql
docker compose --profile milvus up -d milvus
```

**本地开发（`scripts/dev.sh`）**

dev 脚本会读取 `.env` 的 `DB_DRIVER`，自动二选一启动数据库：

```bash
./scripts/dev.sh start      # DB_DRIVER=mysql 时自动启动 mysql-dev，跳过 postgres/langfuse
```

也可显式追加 `--mysql`。MySQL dev 容器默认映射到宿主机 `3306`（可用 `MYSQL_DEV_PORT` 覆盖）。

### 3. 启动应用

首次启动时，golang-migrate 会自动运行 `migrations/mysql/` 下的初始化脚本建表（无需手动导入）。

```bash
make dev-app            # 或正常的容器启动流程
```

日志出现 `Database is up to date (version: 0)` 且服务监听 `:8080`，即表示 MySQL 主库就绪。

## 迁移脚本

MySQL 的建表脚本位于 `migrations/mysql/`，采用与 SQLite 相同的「单文件整合 init」模式（`000000_init.up.sql` / `.down.sql`），而非 PostgreSQL 那套逐版本增量迁移。

相较 PostgreSQL 版本，MySQL schema 做了以下方言适配：

- 自增主键 `BIGINT AUTO_INCREMENT`
- 时间戳 `DATETIME(6) DEFAULT CURRENT_TIMESTAMP(6)`
- `JSONB` → `JSON`
- `TEXT`/`LONGTEXT` 列的默认值使用 MySQL 8.0.13+ 的表达式默认语法 `DEFAULT ('...')`
- 复合索引对长文本列加前缀长度限制（规避 3072 字节 key 上限）
- 部分索引（partial index）降级为普通索引，唯一性约束在应用层保证
- 统一 `ENGINE=InnoDB`、`utf8mb4` / `utf8mb4_unicode_ci`

## 已知限制

- **向量检索必须用外部引擎**：`RETRIEVE_DRIVER=postgres`（pgvector）在 MySQL 模式下不可用，请改用 Milvus / Qdrant 等。
- **自建 Langfuse 不可用**：Langfuse 上游仅支持 PostgreSQL（Prisma provider 写死为 postgresql），MySQL 模式下 `scripts/dev.sh` 会自动跳过 Langfuse 栈。若仍需可观测性，可为 Langfuse 单独部署一个 PostgreSQL 实例，或使用 Langfuse 云端。
- **要求 MySQL ≥ 8.0.13**：初始化脚本依赖 TEXT 列的表达式默认值语法。

## 相关文件

- `.env.example` — DB 配置说明
- `docker-compose.yml` / `docker-compose.dev.yml` — `mysql` 服务定义
- `migrations/mysql/` — MySQL 初始化脚本
- `internal/container/container.go` — MySQL 驱动接入（`case "mysql"`）
