# 使用 MySQL 数据库

WeKnora 支持使用 MySQL 8.0+ 作为主数据库（替代默认的 PostgreSQL）。

## 前提条件

- MySQL 8.0 或更高版本（需要 8.0.13+ 以支持 TEXT 列的表达式默认值）
- 一个外部向量检索引擎（如 Qdrant、Milvus、Weaviate、Elasticsearch）
- `golang-migrate` CLI 工具（如使用脚本迁移）

> **注意**：MySQL 模式不支持 `pgvector` 检索。当 `DB_DRIVER=mysql` 时，必须将 `RETRIEVE_DRIVER` 设置为外部引擎（不可包含 `postgres`）。

## 配置

在 `.env` 文件中设置以下环境变量：

```env
# 选择 MySQL 作为主数据库
DB_DRIVER=mysql

# MySQL 连接配置
DB_HOST=localhost
DB_PORT=3306
DB_USER=root
DB_PASSWORD=your_password
DB_NAME=WeKnora

# 向量检索必须使用外部引擎（不可为 postgres）
RETRIEVE_DRIVER=qdrant
# 或 RETRIEVE_DRIVER=milvus
# 或 RETRIEVE_DRIVER=weaviate
# 或 RETRIEVE_DRIVER=elasticsearch_v8
```

## 快速启动（Docker Compose）

项目提供了 `docker-compose.mysql.yml` 配置文件，可快速启动 MySQL 服务：

```bash
# 启动 MySQL + Redis + DocReader + App + Frontend
docker compose --env-file .env \
  -f docker-compose.yml \
  -f docker-compose.mysql.yml \
  --profile mysql \
  up
```

## 手动迁移

使用提供的迁移脚本：

```bash
# 运行所有 MySQL 迁移
DB_DRIVER=mysql DB_HOST=localhost DB_PORT=3306 DB_USER=root DB_PASSWORD=your_password DB_NAME=WeKnora ./scripts/migrate.sh up
```

迁移文件位于 `migrations/mysql/` 目录，采用单体初始化脚本方式（`000000_init.up.sql` 包含所有表定义）。

## 开发模式

在本地开发环境中使用 MySQL：

```bash
# 1. 修改 .env 文件中的配置
DB_DRIVER=mysql
DB_HOST=127.0.0.1
DB_PORT=3306
DB_USER=root
DB_PASSWORD=weknora
DB_NAME=WeKnora

# 2. 启动 MySQL（通过 Docker）
docker compose --env-file .env -f docker-compose.mysql.yml --profile mysql up -d mysql

# 3. 运行自动迁移（应用启动时会自动执行）
make dev-app
```

## 已知限制

1. **无内置向量检索**：MySQL 模式没有 `pgvector` 支持，必须使用外部向量检索引擎。
2. **无 ParadeDB 全文搜索**：MySQL 模式无法使用 ParadeDB 的 BM25 检索。
3. **无 Langfuse 集成**：本地部署的 Langfuse 需要 PostgreSQL，MySQL 模式下不可用。
4. **要求 MySQL 8.0.13+**：TEXT 列的表达式默认值需要此版本支持。
5. **SQL 验证功能不可用**：知识库搜索中的"自然语言转 SQL"验证依赖
   PostgreSQL 解析器（pg_query_go），在 MySQL 模式下无法正确解析
   MySQL 语法。如需使用此功能，请保持数据库后端为 PostgreSQL。

## 数据迁移（从 PostgreSQL 迁移到 MySQL）

当前版本不提供自动化的 PostgreSQL→MySQL 数据迁移工具。如需迁移，建议：

1. 使用 `pg_dump` 导出 PostgreSQL 数据为 SQL
2. 手动调整 SQL 语法以兼容 MySQL
3. 使用 `mysql` CLI 导入调整后的数据

## 验证

启动后，可通过以下方式验证 MySQL 模式是否正常工作：

1. 检查应用日志中是否出现 `DB Config: driver=mysql` 字样
2. 访问 `/health` 端点确认应用健康状态
3. 创建知识库并上传文档，确认读写功能正常
