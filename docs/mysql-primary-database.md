# 使用 MySQL 作为业务主库

WeKnora 支持将 **MySQL 8.0.13+** 用作业务主数据库。MySQL 只保存用户、空间、
知识库、文档、分块、会话、任务、外部向量库连接配置等事务数据；向量和关键词索引
由 Qdrant 负责。

推荐组合：

```dotenv
DB_DRIVER=mysql
RETRIEVE_DRIVER=qdrant
```

## Docker Compose

`.env` 的最小配置示例：

```dotenv
DB_DRIVER=mysql
RETRIEVE_DRIVER=qdrant

DB_HOST=mysql
DB_PORT=3306
DB_USER=weknora
DB_PASSWORD=replace-with-a-strong-password
DB_NAME=WeKnora
MYSQL_ROOT_PASSWORD=replace-with-a-different-root-password

QDRANT_HOST=qdrant
QDRANT_PORT=6334
QDRANT_COLLECTION=weknora_embeddings

REDIS_PASSWORD=replace-with-a-strong-password
```

启动托管的 MySQL 和 Qdrant：

```bash
docker compose --profile mysql --profile qdrant up -d --scale postgres=0
```

`scripts/start_all.sh` 会根据 `DB_DRIVER` 自动选择 `mysql` 或 `postgres`
profile。应用容器只等待当前选择的业务主库，不会为了 MySQL 部署额外启动
PostgreSQL。

## 使用外部 MySQL

外部 MySQL 应预先创建数据库和具有 DDL/DML 权限的业务账号：

```dotenv
DB_DRIVER=mysql
RETRIEVE_DRIVER=qdrant
DB_HOST=mysql.example.internal
DB_PORT=3306
DB_USER=weknora
DB_PASSWORD=replace-with-a-strong-password
DB_NAME=WeKnora
```

可选连接参数：

```dotenv
DB_CHARSET=utf8mb4
DB_COLLATION=utf8mb4_unicode_ci
DB_CONNECT_TIMEOUT=10s
DB_READ_TIMEOUT=30s
DB_WRITE_TIMEOUT=30s
DB_MAX_OPEN_CONNS=50
DB_MAX_IDLE_CONNS=10
DB_CONN_MAX_LIFETIME=10m
DB_CONN_MAX_IDLE_TIME=5m
DB_REJECT_READ_ONLY=false
```

需要 TLS 时：

```dotenv
DB_USE_TLS=true
DB_TLS_SERVER_NAME=mysql.example.internal
DB_TLS_CA=/run/secrets/mysql-ca.pem
# 双向 TLS 才需要以下两个文件：
DB_TLS_CERT=/run/secrets/mysql-client.pem
DB_TLS_KEY=/run/secrets/mysql-client-key.pem
```

仅在开发或明确接受中间人风险时才使用
`DB_TLS_INSECURE_SKIP_VERIFY=true`。

手动迁移：

```bash
DB_DRIVER=mysql ./scripts/migrate.sh up
```

脚本会自动使用 `migrations/mysql`，并且不会在日志中打印数据库密码。

## Helm

托管 MySQL + 托管 Qdrant 示例：

```bash
helm install weknora ./helm \
  --set database.driver=mysql \
  --set app.env.RETRIEVE_DRIVER=qdrant \
  --set qdrant.enabled=true \
  --set secrets.dbUser=weknora \
  --set secrets.dbPassword='<business-password>' \
  --set secrets.mysqlRootPassword='<root-password>' \
  --set secrets.redisPassword='<redis-password>' \
  --set secrets.jwtSecret='<jwt-secret>'
```

默认情况下，Helm 会为 MySQL 使用服务名 `mysql` 和端口 `3306`。使用外部
MySQL 时设置：

```yaml
database:
  driver: mysql
  managed: false
  host: mysql.example.internal
  port: 3306
```

如果使用 `secrets.existingSecret`，托管 MySQL 部署还要求 Secret 包含
`MYSQL_ROOT_PASSWORD`。

## 支持边界

不支持以下配置：

```dotenv
RETRIEVE_DRIVER=mysql
DB_DRIVER=mysql
RETRIEVE_DRIVER=postgres
```

MySQL 主库模式只接受单一的 `RETRIEVE_DRIVER=qdrant`，因此也不支持 SQLite、
Milvus 或多个 Retriever 的组合。PostgreSQL 业务主库加独立 MySQL Retriever
同样不在此功能范围内。

当前 MySQL 迁移是面向**全新部署**的业务 schema 基线，不提供自动
PostgreSQL → MySQL 数据迁移。

业务主库是部署级配置，不能在 Web 用户端切换。用户可以在 VectorStore
设置中管理外部向量数据库连接，但不能选择 MySQL 作为 Retriever。

`vector_stores` 表会保留在 MySQL 中，因为它保存的是外部向量数据库的连接和
索引配置元数据，并不保存向量本身。

## 验证

启动后可从用户端完成一轮最小验证：

1. 注册或登录并创建空间。
2. 配置模型和 Qdrant VectorStore。
3. 创建知识库并上传文档，等待解析和索引完成。
4. 发起问答，确认可以返回内容及引用。
5. 重启应用后确认用户、知识库和会话仍存在。

同时检查应用日志中显示 `driver=mysql`，且 Qdrant 注册成功；MySQL 中应存在
业务表和 `vector_stores`，但不应存在 MySQL embeddings/向量索引表。
