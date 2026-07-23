# MySQL 作为主数据库

WeKnora 支持 MySQL 8.0.16+ 作为元数据数据库（PostgreSQL 之外的选项）。

## 最低要求

- **MySQL 8.0.16+**（CHECK 约束从 8.0.16 开始强制执行；更早的 8.0.x 版本会静默忽略 CHECK）
- 也支持 8.4.x 和 9.x
- **不支持 MariaDB**（JSON / SKIP LOCKED / CHECK / utf8mb4_0900_ai_ci 语义与 MySQL 8 不兼容）
- 字符集：`utf8mb4`（支持 4 字节 UTF-8：emoji、CJK 扩展）
- 排序规则：`utf8mb4_0900_ai_ci`（大小写不敏感、重音不敏感）

## Metadata 与 Retriever 的关系

MySQL 只接管**元数据层**（租户、知识库、文档、消息、Wiki 等）。**向量检索必须委托给外部引擎**：

```
DB_DRIVER=mysql          → 元数据存储在 MySQL
RETRIEVE_DRIVER=qdrant   → 向量索引存储在 Qdrant（或其他外部引擎）
```

启动时会校验组合：`DB_DRIVER=mysql` + `RETRIEVE_DRIVER=postgres` 或 `sqlite` 会被拒绝（因为 postgres/sqlite retriever 依赖 MySQL 模式下不存在的 embeddings 表）。

合法的外部引擎：`qdrant`、`milvus`、`elasticsearch_v8`、`weaviate`、`doris`、`tencent_vectordb`。

## 快速开始（Docker Compose）

```bash
# 1. 设置环境变量
# 在 .env 中：
DB_DRIVER=mysql
DB_HOST=postgres          # 服务名不变，docker-compose override 会将其替换为 MySQL
DB_PORT=3306
DB_USER=weknora
DB_PASSWORD=<your-password>
DB_NAME=weknora
MYSQL_ROOT_PASSWORD=<root-password>
RETRIEVE_DRIVER=qdrant

# 2. 启动（需要 qdrant profile 来运行向量引擎）
docker compose --profile qdrant -f docker-compose.yml -f docker-compose.mysql.yml up -d
```

## 迁移管理

WeKnora 使用 [golang-migrate](https://github.com/golang-migrate/migrate) 管理数据库 schema。

### MySQL 迁移文件

MySQL 使用独立的 squash baseline（`migrations/mysql/000074_init.up.sql`），其版本号与当前 PostgreSQL schema head 对齐。全新 MySQL 部署只需要这一个文件；后续两种数据库从 `000075` 开始使用相同的迁移版本序列。

### 手动迁移

```bash
# 使用迁移脚本
DB_DRIVER=mysql ./scripts/migrate.sh up
DB_DRIVER=mysql ./scripts/migrate.sh down
DB_DRIVER=mysql ./scripts/migrate.sh version
DB_DRIVER=mysql ./scripts/migrate.sh force <version>
```

### Dirty 状态处理

MySQL 的 DDL 语句（CREATE TABLE、ALTER TABLE 等）会隐式提交事务，因此迁移失败后无法回滚到一致状态。

**MySQL 模式下，dirty 迁移状态不会自动恢复**（PostgreSQL/SQLite 会自动 Force + 重试）。如果迁移失败：

1. 检查已创建的表（可能只创建了部分 schema）
2. 决定是修复还是重建数据库
3. 手动执行 `./scripts/migrate.sh force <version>` 或删除数据库后重新迁移

### Rollback

```bash
DB_DRIVER=mysql ./scripts/migrate.sh down
# 验证：down 后应剩 0 张业务表
```

## 连接配置

### DSN 参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `DB_HOST` | （必填） | MySQL 主机地址（支持 IPv6） |
| `DB_PORT` | 3306 | MySQL 端口 |
| `DB_USER` | （必填） | 数据库用户名 |
| `DB_PASSWORD` | （必填） | 数据库密码 |
| `DB_NAME` | （必填） | 数据库名称 |

### 连接池

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `DB_MAX_OPEN_CONNS` | 50 | 最大打开连接数 |
| `DB_MAX_IDLE_CONNS` | 10 | 最大空闲连接数 |
| `DB_CONN_MAX_LIFETIME` | 10m | 连接最大存活时间 |
| `DB_CONN_MAX_IDLE_TIME` | 5m | 空闲连接最大存活时间 |

### 超时

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `DB_CONNECT_TIMEOUT` | 10s | 连接超时 |
| `DB_READ_TIMEOUT` | 30s | 读超时 |
| `DB_WRITE_TIMEOUT` | 30s | 写超时 |

### 时区

所有时间戳以 UTC 存储：
- 连接 `loc=UTC`
- 服务器建议设置 `--default-time-zone=+00:00`
- 迁移 baseline 开头 `SET time_zone = '+00:00'`
- 所有时间列使用 `DATETIME(6)`（微秒精度）

## Helm 部署

MySQL 和 PostgreSQL 内部部署互斥。启用 MySQL 时**必须**同时关闭 PostgreSQL：

```bash
# --set 方式（注意必须同时设置 postgresql.enabled=false）
helm install weknora ./helm \
  --set mysql.enabled=true \
  --set postgresql.enabled=false \
  --set app.env.RETRIEVE_DRIVER=qdrant \
  --set secrets.dbPassword=... \
  --set secrets.redisPassword=... \
  --set secrets.jwtSecret=...
```

```yaml
# values.yaml 方式
mysql:
  enabled: true
  auth:
    database: WeKnora
    username: weknora
    password: "your-password"

postgresql:
  enabled: false  # 必须显式关闭

app:
  env:
    RETRIEVE_DRIVER: qdrant  # MySQL 模式必须使用外部检索引擎

# 也可以使用外部数据库（不渲染内部 DB）
postgresql:
  enabled: false
mysql:
  enabled: false
database:
  external: true
  driver: mysql
  host: "your-mysql.internal"
  port: 3306
```

## Wiki 搜索差异

MySQL 和 PostgreSQL 在 Wiki 搜索功能上存在已知差异：

| 功能 | PostgreSQL | MySQL |
|------|-----------|-------|
| 全文搜索 | `to_tsvector` + GIN 索引（词级匹配） | `LIKE '%query%'`（子串匹配，召回率更高但更慢） |
| 相似页面 | `pg_trgm` 相似度 + GIN 索引 | `LIKE` 近似匹配（粗粒度） |
| source_refs 查询 | `@>` + GIN 索引 | `JSON_CONTAINS`（KB 分区内扫描） |

这些差异意味着 MySQL 上的 Wiki 搜索结果可能比 PostgreSQL 更多（因为 LIKE 是子串匹配），但大型知识库中查询速度更慢。
