# MySQL 业务元数据迁移

`DB_DRIVER=mysql` 使用本目录的 golang-migrate 版本化迁移。MySQL 存储业务元数据；知识检索使用外部检索引擎，PostgreSQL 的 pgvector、ParadeDB 索引不会在这里创建。

当前 `000000_init` 是尚未发布的空库基线，按 upstream/main `e28c68cf552cc1aba2fc923a73b0ab23a33bcb55`、PostgreSQL `000133` 的最终结构整理，覆盖 134 个 PostgreSQL up 迁移文件。`coverage.json` 记录每个源文件的 SHA-256 及对应结构、索引或空库无需执行的数据修复。

- `000000_init.up.sql` 创建表、索引、种子和约束；`down.sql` 删除这些对象。
- 软删除、非空 token、协作活动状态的部分唯一约束使用可空生成列实现；普通部分索引使用包含过滤列的普通索引实现，索引内可能包含 PostgreSQL 排除的行。
- 业务时间使用 `DATETIME(6)`；连接必须使用 UTC。已有数据的历史回填不会在空库重放。
- 本基线不承担旧 MySQL 数据库的升级；后续已发布的 schema 变更应新增递增版本，不能修改已发布迁移。

实际数据库验证使用 `mysqlintegration` 构建标签和 `WEKNORA_MYSQL_TEST_DSN`，通过正式迁移创建独立测试库，不使用 `AutoMigrate`。覆盖测试会核对 PostgreSQL 文件清单及哈希；新增源迁移时必须审查并更新基线对应效果。

配置和检索限制见 [MySQL 数据库说明](../../docs/mysql-database.md)。
