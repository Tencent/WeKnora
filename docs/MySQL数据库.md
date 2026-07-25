# 使用 MySQL 作为业务数据库

WeKnora 支持使用 MySQL 8.0.16+ 存储空间、知识库、文档、会话等业务数据。
MySQL 不承担向量检索，必须同时配置至少一个具备向量能力的外部检索引擎。

## 约束

- MySQL 版本必须为 8.0.16 或更高，不支持 MariaDB。
- 数据库字符集使用 `utf8mb4`，排序规则使用 `utf8mb4_0900_ai_ci`。
- 数据库账号需要建表、修改表、创建索引和删除表权限，以执行自动迁移。
- `RETRIEVE_DRIVER` 不能是 `mysql`、`postgres` 或 `sqlite`。
- 仅配置 `elasticsearch_v7` 不满足向量检索要求；可与 Qdrant 等向量引擎组合使用。
- PostgreSQL 与 MySQL 之间不提供自动数据搬迁。已有 PostgreSQL 部署切换前需自行完成数据导出、转换和校验。

## Docker Compose

`docker-compose.mysql.yml` 会将基础 Compose 文件中的数据库服务替换为
MySQL 8.0.37，并默认使用 Qdrant：

```bash
MYSQL_USER=weknora \
MYSQL_PASSWORD='change-me' \
MYSQL_DATABASE=WeKnora \
MYSQL_ROOT_PASSWORD='change-root-password' \
MYSQL_RETRIEVE_DRIVER=qdrant \
docker compose \
  -f docker-compose.yml \
  -f docker-compose.mysql.yml \
  --profile qdrant up -d
```

该方式不会启动 PostgreSQL。MySQL 数据保存在 `mysql-data` volume 中。

## 外部 MySQL

```dotenv
DB_DRIVER=mysql
DB_HOST=mysql.example.internal
DB_PORT=3306
DB_USER=weknora
DB_PASSWORD=change-me
DB_NAME=weknora

RETRIEVE_DRIVER=qdrant
QDRANT_HOST=qdrant.example.internal
QDRANT_PORT=6334
```

可选连接池参数：

```dotenv
DB_CONNECT_TIMEOUT=10s
DB_READ_TIMEOUT=30s
DB_WRITE_TIMEOUT=30s
DB_MAX_OPEN_CONNS=50
DB_MAX_IDLE_CONNS=10
DB_CONN_MAX_LIFETIME=10m
DB_CONN_MAX_IDLE_TIME=5m
```

## 迁移与故障处理

服务首次启动会从 `migrations/mysql` 创建版本 74 的完整业务 schema。
MySQL DDL 会隐式提交，因此迁移失败后服务会拒绝启动，也不会自动执行
`force` 或重试。此时应：

1. 停止应用写入。
2. 检查 `schema_migrations` 的 `version` 和 `dirty`。
3. 根据启动日志定位已执行到的 DDL。
4. 从备份恢复或人工修复 schema。
5. 确认 `dirty=false` 后再启动应用。

生产升级前应先备份业务库，并在同版本的临时库执行一次迁移演练。
