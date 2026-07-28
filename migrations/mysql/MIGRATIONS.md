# MySQL 迁移约定

## 设计说明

MySQL 迁移目录使用与 PostgreSQL (`migrations/versioned/`) 相同的增量版本化迁移方案。

### 基础迁移

`000000_init.up.sql` 包含**完整的初始 schema**（47 张表），是 MySQL 起步的唯一基础迁移。

### 增量迁移

后续的 schema 变更（添加表、修改列等）必须以**新的增量迁移文件**形式添加，而不是修改 `000000_init.up.sql`：

```
migrations/mysql/
├── 000000_init.up.sql       ← 完整初始 schema（不变）
├── 000000_init.down.sql     ← 回滚初始 schema
├── 000001_xxx.up.sql        ← 第一次增量变更
├── 000001_xxx.down.sql
├── 000002_xxx.up.sql        ← 第二次增量变更
├── 000002_xxx.down.sql
└── MIGRATIONS.md
```

### 与 PostgreSQL 版本号的同步

当 `migrations/versioned/` 目录中添加了新的 PG 迁移（如 `000065_xxx.up.sql`），
应在 `migrations/mysql/` 中添加**相同版本号**的 MySQL 迁移（如 `000065_xxx.up.sql`），
确保两个数据库后端在相同版本号上有对应的 schema 变更。

### 创建新迁移

使用项目提供的脚本：

```bash
# 创建 MySQL 迁移（推荐）
DB_DRIVER=mysql ./scripts/migrate.sh create migration_name

# 创建的文件会自动出现在 migrations/mysql/ 目录下
```

或手动创建：

```bash
migrate create -ext sql -dir migrations/mysql -seq migration_name
```

### 迁移文件编写规范

- 所有 CREATE TABLE 必须包含 `ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`
- JSON 列使用 `type:json`（与 PostgreSQL 的 `jsonb` 对应）
- MySQL 保留字（如 `key`）需在代码层使用 `clause.Column{Name: "key"}` 引用
- 优先使用参数化查询（`?` 占位符），防范 SQL 注入
