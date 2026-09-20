# migrations/mysql —— 未接线的 MySQL 建表脚本

⚠️ **这里的 SQL 不会被任何代码或脚本执行。MySQL 目前不是 WeKnora 的主库选项。**

## 现状

`internal/container/container.go` 的 `initDatabase()` 中，`DB_DRIVER` 的 switch 只有两个分支：

| `DB_DRIVER` | 迁移来源 |
| --- | --- |
| `postgres` | `migrations/versioned/`（golang-migrate 增量迁移） |
| `sqlite` | `migrations/sqlite/`（golang-migrate 增量迁移，Lite 模式） |

其余取值一律返回 `unsupported database driver`，因此配置 `DB_DRIVER=mysql` 会导致服务启动失败。

本目录下的 `00-init-db.sql` 是一份一次性建表脚本，覆盖 10 张核心表（`tenants`、`models`、`knowledge_bases`、`knowledges`、`sessions`、`messages`、`message_suggestion_sets`、`message_suggestion_events`、`chunks`、`chunk_revisions`）。它会在有人改动这些基础表时被顺带更新，但**没有任何 Go 代码、Makefile 目标或 compose 配置引用它**。

## 要真正支持 MySQL 还差什么

仅有这份建表脚本是不够的——它只对应初始 schema，缺少与 `migrations/versioned/`（Postgres 侧已有 100+ 个增量迁移）对等的增量迁移集。完整接线至少需要：

1. 在 `initDatabase()` 中增加 `case "mysql"`，接上 `gorm.io/driver/mysql` 与 golang-migrate 的 MySQL DSN；
2. 建立 `migrations/mysql/` 的增量迁移序列，并与 `versioned/` 的 schema 演进保持同步；
3. 处理 Postgres 专有用法（`JSONB`、数组类型、`ON CONFLICT`、ParadeDB 的 BM25 索引等）在 MySQL 侧的等价实现或降级方案；
4. 明确向量检索的落地方式——MySQL 本身不提供向量索引，需要依赖外部向量库（`RETRIEVE_DRIVER`）。

相关讨论见 [#1418](https://github.com/Tencent/WeKnora/issues/1418)。在上述工作完成之前，请勿在文档或配置注释中声称支持 `DB_DRIVER=mysql`。
