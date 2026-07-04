# MySQL 支持说明

WeKnora 支持在新部署中使用 MySQL 作为主业务数据库，也支持从已有 PostgreSQL
部署离线迁移到 MySQL。默认仍使用 PostgreSQL；只有显式设置 `DB_DRIVER=mysql`
或 `database.driver=mysql` 时才会启用 MySQL。

MySQL 检索器也可以作为向量库使用。它会按向量维度分表保存向量嵌入，在
MySQL 8 上使用 JSON 向量存储兼容方案；当服务器支持时，MySQL 9+ 可以使用原生
`VECTOR(N)` 列类型。

## 新部署

全新 MySQL 部署需要设置主数据库变量：

```env
DB_DRIVER=mysql
DB_HOST=mysql
DB_PORT=3306
DB_USER=root
DB_PASSWORD=change-me
DB_NAME=weknora
```

如果同时要把 MySQL 用作检索器，请设置：

```env
RETRIEVE_DRIVER=mysql
MYSQL_HOST=mysql
MYSQL_PORT=3306
MYSQL_USERNAME=root
MYSQL_PASSWORD=change-me
MYSQL_DATABASE=weknora
MYSQL_TABLE_PREFIX=weknora_embeddings
```

当 `DB_DRIVER=mysql` 且 `RETRIEVE_DRIVER` 为空或仍为默认的 `postgres` 时，应用会自动
把默认检索器归一化为 `mysql`，避免 MySQL 主库误配 PostgreSQL 检索器。若显式设置
`RETRIEVE_DRIVER=postgres,qdrant`，应用会移除不兼容的 `postgres` 并保留 `qdrant`。

当 `DB_DRIVER=mysql` 且没有设置 `MYSQL_*` 连接覆盖项时，MySQL 检索器会复用主业务库的
`DB_*` 连接。`MYSQL_TABLE_PREFIX` 只控制向量嵌入表前缀，不会强制创建单独连接。

## Docker Compose

标准 Compose 文件通过 `mysql` 配置档提供可选 MySQL 服务：

```bash
docker compose --profile mysql up -d mysql
```

完整 Compose 部署建议叠加 MySQL 覆盖文件。该覆盖文件会把 app 的主库默认值切到
MySQL，并让 app 等待 MySQL 健康：

```bash
docker compose -f docker-compose.yml -f docker-compose.mysql.yml --profile mysql up -d
```

未设置时，app 默认使用 MySQL root 用户和 `DB_PASSWORD`。如需非 root 用户，请先在
MySQL 中创建用户并授权，再通过 `MYSQL_*` 覆盖 app 的检索器连接参数。

`docker-compose.dev.yml` 为本地基础设施提供同样的配置档：

```bash
docker compose -f docker-compose.dev.yml --profile mysql up -d mysql redis docreader
```

Compose 的应用服务默认仍使用 PostgreSQL，因此现有部署不会被改变。使用 MySQL 运行
应用时，请通过上面的双文件命令启动，或自行确保 app 收到了 `DB_DRIVER=mysql` 与
MySQL 连接参数。

Compose 默认值也会把 `mysql` 加入 `SSRF_WHITELIST_EXTRA`，这样向量库 API 可以校验
Compose 内部主机名 `mysql:3306`。

## Helm

Helm Chart 默认使用 PostgreSQL：

```yaml
database:
  driver: postgres
postgresql:
  enabled: true
mysql:
  enabled: false
```

使用外部 MySQL 实例：

```yaml
database:
  driver: mysql
  external:
    host: mysql.example.internal
    port: "3306"
postgresql:
  enabled: false
mysql:
  enabled: false
secrets:
  dbUser: app_user
  dbPassword: change-me
  dbName: weknora
```

使用 Helm Chart 内置 MySQL 工作负载：

```yaml
database:
  driver: mysql
postgresql:
  enabled: false
mysql:
  enabled: true
secrets:
  dbUser: root
  dbPassword: change-me
  dbName: weknora
```

应用模板会根据 `database.driver` 和 `database.external.*` 输出 `DB_DRIVER`、
`DB_HOST` 和 `DB_PORT`。`DB_USER`、`DB_PASSWORD` 与 `DB_NAME` 仍来自 Helm Chart 密钥。

当 `database.driver=mysql` 且 `app.env.RETRIEVE_DRIVER` 未设置或仍为默认 `postgres` 时，
Helm Chart 会把 app 的 `RETRIEVE_DRIVER` 渲染为 `mysql`，并提供匹配的 `MYSQL_*` 默认值，
让检索器使用所选 MySQL 数据库。显式配置为其他检索器（如 `qdrant`）时不会被覆盖。

## 向量库 API

用户可注册的 MySQL 向量库类型使用以下字段：

```json
{
  "connection_config": {
    "addr": "mysql:3306",
    "database": "weknora",
    "username": "root",
    "password": "change-me"
  },
  "index_config": {
    "collection_prefix": "weknora_embeddings"
  }
}
```

`addr` 和 `database` 为必填项。`password` 会加密保存，并在 API 响应中脱敏。重复
检测使用连接端点和最终生效的表前缀。

## PostgreSQL 到 MySQL 离线迁移

迁移工具是离线工具，不会在应用启动时自动运行。请先备份 PostgreSQL，停止应用写入，
创建空的 MySQL 目标库，然后执行：

```bash
go run ./cmd/tools/pg_to_mysql \
  --pg-dsn "postgres://postgres:postgres@localhost:5432/weknora?sslmode=disable" \
  --mysql-dsn "root:change-me@tcp(localhost:3306)/weknora?charset=utf8mb4&parseTime=true&loc=UTC" \
  --batch-size 1000 \
  --migrate-schema
```

该工具刻意采用保守行为：

- `--migrate-schema` 会在复制数据前应用 `migrations/mysql`。
- 目标 MySQL 数据库必须为空，除非显式传入 `--allow-non-empty-target`。
- 只复制 PostgreSQL 源表和 MySQL 目标表同时存在的列。
- 不复制 PostgreSQL `embeddings` 数据。业务数据迁移后，应通过重新索引知识库重建
  MySQL 检索器向量嵌入表。
- `--dry-run` 只打印计划复制的表，不写入行。除非先真实执行过 `--migrate-schema`，
  否则试运行检查时目标表结构必须已经存在。
- 失败时错误信息会包含表名和批次偏移范围。

迁移完成后，用 `DB_DRIVER=mysql` 启动 WeKnora，并按正常知识库索引流程重新生成
检索器向量嵌入。
