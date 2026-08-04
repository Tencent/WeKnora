# MySQL 主数据库支持

WeKnora 默认仍使用 PostgreSQL/ParadeDB。需要把业务元数据放到 MySQL 时，可以将 `DB_DRIVER` 设为 `mysql`。

MySQL 只作为主业务数据库保存租户、知识库、会话、消息、资源等元数据；它不是向量检索后端。启用 MySQL 时，`RETRIEVE_DRIVER` 必须选择外部向量库，例如 `elasticsearch_v8`、`opensearch`、`qdrant`、`milvus`、`tencent_vectordb`、`weaviate` 或 `doris`，不能使用 `postgres` 或 `sqlite`。

## Docker Compose

示例 `.env`：

```env
DB_DRIVER=mysql
DB_HOST=mysql
DB_PORT=3306
DB_USER=weknora
DB_PASSWORD=change-me
DB_NAME=WeKnora
MYSQL_ROOT_PASSWORD=change-root-password

RETRIEVE_DRIVER=qdrant
QDRANT_HOST=qdrant
QDRANT_PORT=6334
```

启动示例：

```bash
docker compose --profile mysql --profile qdrant up -d
```

PostgreSQL 默认路径没有改变。不启用 `mysql` profile 时，compose 仍按原来的 PostgreSQL 服务启动。

## Helm

使用外部 MySQL 时，关闭内置 PostgreSQL，并设置数据库连接信息：

```yaml
database:
  driver: mysql
  host: mysql.example.internal
  port: 3306

postgresql:
  enabled: false

app:
  env:
    RETRIEVE_DRIVER: qdrant
```

`DB_USER`、`DB_PASSWORD`、`DB_NAME` 仍来自 chart secret。使用已有 secret 时，确保 secret 包含这些 key。

## 迁移行为

MySQL 使用 `migrations/mysql/000078_mysql_baseline.*.sql` 作为当前 PostgreSQL 迁移头 `000078` 的 consolidated baseline。迁移失败或 dirty 状态不会自动恢复，启动会失败退出；这是为了避免 MySQL 初始化半成功后继续写入不完整 schema。

当前实现不提供 PostgreSQL 到 MySQL 的在线迁移工具，也不提供 MySQL 备份恢复或管理控制台。已有 PostgreSQL 数据需要另行做离线迁移和校验。
