# MySQL 主数据库部署说明

WeKnora 现在支持使用 MySQL 作为主数据库（`DB_DRIVER=mysql`）。PostgreSQL 仍然可用；MySQL 模式主要替换业务主库，不再使用 PostgreSQL/ParadeDB 的内置向量检索能力。

## 关键限制

- `DB_DRIVER=mysql` 时不能再使用 `RETRIEVE_DRIVER=postgres`。
- 请改用独立检索/向量引擎，例如：`qdrant`、`milvus`、`weaviate`、`elasticsearch_v8`、`opensearch`、`doris` 或 `tencent_vectordb`。

## docker-compose 示例

`.env` 示例：

```env
DB_DRIVER=mysql
DB_HOST=mysql
DB_PORT=3306
DB_USER=weknora
DB_PASSWORD=weknora123!@#
DB_NAME=WeKnora

# MySQL 模式下请选择非 postgres 的检索引擎
RETRIEVE_DRIVER=qdrant
QDRANT_HOST=qdrant
QDRANT_PORT=6334
```

启动 MySQL 与示例 Qdrant 检索引擎：

```bash
docker compose --profile mysql --profile qdrant up -d
```

> 现有 Langfuse 集成依然依赖 PostgreSQL，这是 Langfuse 自身要求；它与 WeKnora 主库可分开配置。

## 手动迁移

```bash
DB_DRIVER=mysql ./scripts/migrate.sh up
```

脚本会自动使用 `migrations/mysql` 目录。应用启动时 `AUTO_MIGRATE=true` 也会自动执行相同的 MySQL 迁移。
