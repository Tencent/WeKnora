# 使用 MySQL 作为业务主数据库

WeKnora 支持将 **MySQL 8.0.13+** 作为租户、用户、知识库、会话、任务队列、Wiki、资源目录等业务数据的主数据库。

> **边界说明：MySQL 只作为业务主库，不承担向量或关键词检索。** 生产和开发环境都必须为 `RETRIEVE_DRIVER` 配置 Qdrant、Milvus、Weaviate、Doris、OpenSearch、Elasticsearch 或腾讯云 VectorDB 等专业检索后端。

推荐组合：

```dotenv
DB_DRIVER=mysql
RETRIEVE_DRIVER=qdrant
```

不支持 `RETRIEVE_DRIVER=mysql`；应用会在启动阶段直接报错。MySQL 只负责业务主库，向量必须使用外部专业数据库。

## 1. 架构职责

| 配置 | 职责 | 推荐值 |
| --- | --- | --- |
| `DB_DRIVER` | 业务数据、事务、权限、任务状态、配置 | `mysql` |
| `RETRIEVE_DRIVER` | Embedding 向量与关键词检索 | `qdrant`（推荐）或其他专业后端 |

这种拆分让 MySQL 专注于强事务和关系数据，让向量数据库负责 ANN 索引、相似度召回和大规模检索，避免在 MySQL 中使用 JSON 向量扫描。

## 2. Docker Compose：MySQL + Qdrant

复制配置：

```bash
cp .env.example .env
```

修改 `.env`：

```dotenv
DB_DRIVER=mysql
DB_HOST=mysql
DB_PORT=3306
DB_USER=weknora
DB_PASSWORD=replace-with-a-strong-password
DB_NAME=WeKnora

RETRIEVE_DRIVER=qdrant
QDRANT_HOST=qdrant
QDRANT_PORT=6334
QDRANT_COLLECTION=weknora_embeddings

MYSQL_ROOT_PASSWORD=replace-with-a-different-root-password
```

启动：

```bash
docker compose -f docker-compose.yml -f docker-compose.mysql.yml --profile qdrant up -d
```

MySQL override 会禁用默认 PostgreSQL、启动 MySQL 8.4，并等待 MySQL 健康后启动应用；Qdrant 由 `qdrant` profile 单独启动。`scripts/start_all.sh` 在检测到 `DB_DRIVER=mysql` 与 `RETRIEVE_DRIVER=qdrant` 时会自动选择 MySQL override，并在 `QDRANT_HOST` 未配置或为 `qdrant` 时自动启用该 profile：

```bash
./scripts/start_all.sh --docker
```

> MySQL override 使用 Compose 的 `!override` 合并标签，要求 Docker Compose 2.24.4 或更高版本。旧版 `docker-compose` v1 会被启动脚本明确拒绝。

## 3. 外部 MySQL 与外部向量数据库

业务主库：

```dotenv
DB_DRIVER=mysql
DB_HOST=mysql.example.internal
DB_PORT=3306
DB_USER=weknora
DB_PASSWORD=p@ss-word-with-special-characters
DB_NAME=WeKnora
AUTO_MIGRATE=true
```

外部 Qdrant：

```dotenv
RETRIEVE_DRIVER=qdrant
QDRANT_HOST=qdrant.example.internal
QDRANT_PORT=6334
QDRANT_COLLECTION=weknora_embeddings
QDRANT_API_KEY=replace-me
QDRANT_USE_TLS=true
```

连接串由 `go-sql-driver/mysql.Config` 和 URL-safe migration builder 构造，密码可以包含 `@`、`#`、空格、冒号和斜杠等字符。应用与迁移脚本不会输出密码或完整 DSN。

生产环境可按需配置：

```dotenv
DB_TLS_MODE=true
DB_CONNECT_TIMEOUT=10s
DB_READ_TIMEOUT=30s
DB_WRITE_TIMEOUT=30s
DB_MAX_OPEN_CONNS=50
DB_MAX_IDLE_CONNS=10
DB_CONN_MAX_LIFETIME=10m
DB_CONN_MAX_IDLE_TIME=5m
```

## 4. 数据库初始化与迁移

建议预先创建数据库和最小权限用户：

```sql
CREATE DATABASE WeKnora
  CHARACTER SET utf8mb4
  COLLATE utf8mb4_unicode_ci;

CREATE USER 'weknora'@'%' IDENTIFIED BY 'replace-with-a-strong-password';
GRANT ALL PRIVILEGES ON WeKnora.* TO 'weknora'@'%';
FLUSH PRIVILEGES;
```

默认自动执行 `migrations/mysql`。如果由平台团队单独管理迁移：

```dotenv
AUTO_MIGRATE=false
```

```bash
DB_DRIVER=mysql ./scripts/migrate.sh up
DB_DRIVER=mysql ./scripts/migrate.sh version
```

迁移默认 fail-fast。`MIGRATION_FAILURE_MODE=warn` 只应作为临时兼容开关，不能作为长期生产配置。

MySQL migration 版本必须与 PostgreSQL 主线保持一致；自动测试会验证版本、up/down 文件配对、真实 up→down→up、表/字段/空值语义一致性。

## 5. SQL 方言适配

业务 Repository 已显式处理：

- 大小写不敏感搜索与 Wiki 正则搜索；
- JSON 提取、包含和数组长度；
- MySQL 保留字（如 `key`）；
- PostgreSQL `UPDATE ... RETURNING` 的 MySQL 事务替代；
- `FOR UPDATE SKIP LOCKED` 并发任务领取；
- partial unique index 的 generated-column 等价实现；
- `DATETIME(3)`、UTC、`utf8mb4_unicode_ci`；
- 软删除、空值语义和动态 JSON key 参数化。

## 6. Helm：外部 MySQL + 外部 Qdrant

Chart 不内置 MySQL 或 Qdrant，需指向外部服务。示例 values：

```yaml
app:
  database:
    driver: mysql
    host: mysql.example.internal
    port: "3306"
  env:
    RETRIEVE_DRIVER: qdrant
  extraEnv:
    - name: QDRANT_HOST
      value: qdrant.example.internal
    - name: QDRANT_PORT
      value: "6334"
    - name: QDRANT_USE_TLS
      value: "true"

postgresql:
  enabled: false

secrets:
  dbUser: weknora
  dbPassword: replace-me
  dbName: WeKnora
```

Chart 会拒绝以下矛盾配置：

- `database.driver=mysql` 且 `postgresql.enabled=true`；
- `database.driver=mysql` 且 `RETRIEVE_DRIVER=postgres`；

## 7. 验证与排障

检查 migration：

```sql
SELECT version, dirty FROM schema_migrations;
SHOW VARIABLES LIKE 'character_set_server';
SHOW VARIABLES LIKE 'collation_server';
SHOW VARIABLES LIKE 'time_zone';
```

检查应用启动日志时，应同时看到 MySQL 主库连接成功和外部向量引擎注册成功；不应出现 MySQL 向量表或 MySQL Retriever 初始化日志。若迁移失败，不要直接删除 `schema_migrations`，应先检查失败 DDL 是否部分执行，再使用 `scripts/migrate.sh force <version>` 修复版本状态。
