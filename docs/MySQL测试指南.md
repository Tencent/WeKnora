# MySQL 模式测试与验证指南

本文档指导你如何对 WeKnora 的 MySQL 后端模式进行测试与验证。

## 1. 快速开始：一键测试

项目提供了 `test_mysql.sh` 自动化测试脚本，可通过以下命令执行完整测试流程：

```bash
# 确保端口 3306、6379、18080 未被占用
./test_mysql.sh
```

该脚本会自动执行以下 7 个步骤：

| 步骤 | 内容 | 验证点 |
|------|------|--------|
| 1/7 | 检查前提条件（Docker、Go、curl） | 环境就绪 |
| 2/7 | 生成 `.env.mysql.test` 配置文件 | 配置完整 |
| 3/7 | 启动 MySQL 8.0 + Redis 容器 | 容器健康、charset 正确 |
| 4/7 | 编译 Go 后端二进制 | 编译无错误 |
| 5/7 | 执行数据库迁移（golang-migrate） | 47 张表全部创建 |
| 6/7 | 启动应用并测试 API | 5 项 API 测试 |
| 7/7 | 生成测试报告 | 日志关键信息汇总 |

### 必须关注的重点日志

#### 步骤 3 — MySQL 启动日志

```bash
docker logs WeKnora-mysql
```

确认：
- `[Server] /usr/sbin/mysqld: ready for connections.` — MySQL 就绪
- `character_set_server` 为 `utf8mb4`
- `collation_server` 为 `utf8mb4_unicode_ci`

#### 步骤 5 — 迁移日志

迁移完成后手动验证：

```bash
# 查看表数量
docker exec WeKnora-mysql mysql -h localhost -u root -p"$DB_PASSWORD" -s \
  -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='WeKnora'"

# 查看迁移版本（如安装了 golang-migrate）
migrate -path migrations/mysql -database "mysql://root:${DB_PASSWORD}@tcp(127.0.0.1:3306)/WeKnora?charset=utf8mb4" version
```

#### 步骤 6 — 应用启动日志

```bash
# 实时查看
tail -f /tmp/weknora-mysql-test.log

# 搜索关键行
grep -E "DB Config|driver.*mysql|migration|dirty|error|warn|panic" /tmp/weknora-mysql-test.log
```

成功启动时应看到：
- `DB Config: driver=mysql` — 确认使用了 MySQL 驱动
- `Database is up to date` 或 `Database migrated from version 0 to 1` — 迁移完成
- `Server started` 或包含端口监听信息 — HTTP 服务就绪

---

## 2. 逐步手动测试

如果希望手动执行测试流程，而非使用自动化脚本，可按以下步骤操作。

### 2.1 启动测试容器

```bash
# 启动 MySQL + Redis
DB_PASSWORD=weknora_test_pass \
DB_NAME=WeKnora \
REDIS_PASSWORD=redis_test_pass \
docker compose -f docker-compose.mysql.yml --profile mysql-test up -d

# 等待 MySQL 就绪（约 30 秒）
docker exec WeKnora-mysql mysqladmin ping -h localhost -u root -p"weknora_test_pass" --silent
```

### 2.2 创建测试配置文件

```bash
# 创建 .env.mysql.test (见 test_mysql.sh 中的模板)
# 或直接设置环境变量：
export DB_DRIVER=mysql
export DB_HOST=127.0.0.1
export DB_PORT=3306
export DB_USER=root
export DB_PASSWORD=weknora_test_pass
export DB_NAME=WeKnora
export REDIS_ADDR=127.0.0.1:6379
export REDIS_PASSWORD=redis_test_pass
export APP_PORT=18080
export STORAGE_TYPE=local
export LOCAL_STORAGE_BASE_DIR=./.local-test-data/files
export GIN_MODE=debug
export RETRIEVE_DRIVER=
export STREAM_MANAGER_TYPE=memory
export DISABLE_REGISTRATION=false
export JWT_SECRET=test-jwt-secret
export TENANT_AES_KEY=test-tenant-aes-key-32bytes!!!
export SYSTEM_AES_KEY=test-system-aes-key-32bytes!!
```

### 2.3 执行数据库迁移

```bash
# 方案 A：使用 golang-migrate CLI（推荐）
migrate -path migrations/mysql \
  -database "mysql://root:weknora_test_pass@tcp(127.0.0.1:3306)/WeKnora?charset=utf8mb4&multiStatements=true" up

# 方案 B：直接执行 SQL（当 golang-migrate 不可用时）
docker exec -i WeKnora-mysql mysql -h localhost -u root -p"weknora_test_pass" WeKnora \
  < migrations/mysql/000000_init.up.sql
```

验证迁移结果：

```bash
# 输出应为 47 张表
docker exec WeKnora-mysql mysql -h localhost -u root -p"weknora_test_pass" -s \
  -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='WeKnora'"
```

### 2.4 编译启动后端

```bash
# 编译
go build -o WeKnora-mysql-test ./cmd/server

# 启动（后台运行）
mkdir -p ./.local-test-data/files
./WeKnora-mysql-test > /tmp/weknora-test.log 2>&1 &
echo $! # 记录 PID
```

### 2.5 API 功能验证

```bash
# 1) 健康检查（无需认证）
curl -s http://localhost:18080/health
# 预期: {"status":"ok"}

# 2) 用户注册
curl -s -X POST http://localhost:18080/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"username":"testuser","email":"test@example.com","password":"Test12345!"}'
# 预期: 返回包含 token 的 JSON

# 3) 用户登录
curl -s -X POST http://localhost:18080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"test@example.com","password":"Test12345!"}'
# 保存返回的 TOKEN

# 4) 获取当前用户信息（使用上一步的 TOKEN）
TOKEN="<从响应中提取的 token>"
curl -s http://localhost:18080/api/v1/auth/me \
  -H "Authorization: Bearer $TOKEN"
# 预期: 返回用户信息 JSON

# 5) 获取系统信息
curl -s http://localhost:18080/api/v1/system/info
# 预期: 返回系统配置信息（可能需认证头）
```

### 2.6 清理

```bash
# 停止应用
kill <PID>

# 停止容器
docker compose -f docker-compose.mysql.yml --profile mysql-test down

# 删除测试数据
rm -rf ./.local-test-data
rm -f WeKnora-mysql-test
rm -f .env.mysql.test
```

---

## 3. 已知的可验证 SQL 兼容性场景

这些是 MySQL 迁移中最关键的 SQL 兼容点，已通过代码中的 dialect branching 处理：

### 3.1 JSON 操作

| 功能 | PostgreSQL | MySQL | 受影响的文件 |
|------|-----------|-------|-------------|
| JSON 字段提取 | `metadata->'key'` | `JSON_EXTRACT(metadata, '$.key')` | chunk.go, knowledge.go |
| JSON 文本提取 | `metadata->>'key'` | `JSON_UNQUOTE(JSON_EXTRACT(...))` 或 `metadata->>'$.key'` | knowledge.go, model_usage.go |
| JSON 包含查询（数组） | `@> ?::jsonb` | `JSON_CONTAINS(col, ?)` | chunk.go, wiki_page.go |
| JSON 数组长度 | `jsonb_array_length(col)` | `JSON_LENGTH(col)` | wiki_page.go, chunk.go |
| JSON 表达式默认值 | `'[]'::jsonb` | `CAST('[]' AS JSON)` | 迁移脚本（wiki_pages 等） |

**如何验证**：创建知识库、FAQ 导入、Wiki 页面编辑——观察日志中是否有 JSON 相关错误。

### 3.2 全文搜索与字符串匹配

| 功能 | PostgreSQL | MySQL |
|------|-----------|-------|
| 大小写不敏感匹配 | `ILIKE` | `LOWER(col) LIKE LOWER(?)` |
| 全文检索 | `to_tsvector(...) @@ plainto_tsquery(...)` | `LOWER(col) LIKE LOWER(CONCAT('%', ?, '%'))` |
| 正则表达式 | `~*`（大小写不敏感） | `REGEXP`（默认大小写不敏感） |
| 随机排序 | `RANDOM()` | `RAND()` |

**如何验证**：在知识库搜索功能、Wiki 页面搜索中输入关键词，确认正常返回结果。

### 3.3 时间函数

| 功能 | PostgreSQL | MySQL |
|------|-----------|-------|
| 当前时间 | `NOW()` | `NOW()` |
| 时间间隔减法 | `NOW() - INTERVAL 'N DAYS'` | `DATE_SUB(NOW(), INTERVAL N DAY)` |
| 自定义时间戳 | `datetime('now')`（SQLite） | `NOW()`（MySQL） |

**如何验证**：查看数据源同步日志清理、审计日志保留清理是否正常。

### 3.4 RETURNING 子句

MySQL 不支持 `RETURNING`，任务队列使用事务 + SELECT 替代：

```sql
-- PostgreSQL（单次往返）
UPDATE task_pending_ops SET fail_count = fail_count + 1
WHERE id IN (...) RETURNING fail_count;

-- MySQL（事务 + 两趟查询）
START TRANSACTION;
UPDATE task_pending_ops SET fail_count = fail_count + 1 WHERE id IN (...);
SELECT fail_count FROM task_pending_ops WHERE id IN (...);
COMMIT;
```

**如何验证**：测试文档导入/重试功能，确认无死锁或超时。

### 3.5 保留字冲突

MySQL 中 `key` 是保留字。代码层通过 GORM 的 `clause.Column{Name: "key"}` 自动处理（GORM 会基于 dialect 选择合适的引用方式）：

```go
// GORM 内部在 PostgreSQL 上生成: ORDER BY "key"
// GORM 内部在 MySQL 上生成:    ORDER BY `key`
db.Order(clause.Column{Name: "key"})
```

**如何验证**：查看系统设置页面是否能正常加载（GORM 的 `clause.Column` 会处理引用差异）。

### 3.6 行级锁

MySQL 与 PostgreSQL 均支持 `SELECT ... FOR UPDATE` 行级锁，用于向量存储删除守卫：

```go
// canRowLock() 判断逻辑
switch db.Dialector.Name() {
case "postgres", "mysql":
    return true  // SELECT ... FOR UPDATE 支持
default: // sqlite
    return false // SQLite 使用序列化写入
}
```

**如何验证**：创建向量存储后删除，验证行级锁正常生效。

### 3.7 批量更新类型转换

MySQL 不需要 PostgreSQL 的 `::boolean` / `::integer` 类型转换语法：

| 操作 | PostgreSQL | MySQL | SQLite |
|------|-----------|-------|--------|
| 布尔 CASE 表达式 | `(CASE ... END)::boolean` | `CASE ... END` | `CASE ... END` |
| 整数 CASE 表达式 | `(CASE ... END)::integer` | `CASE ... END` | `CASE ... END` |

**如何验证**：批量修改 FAQ 分类标签或启用/禁用标志，确认标志位更新正确。

---

## 4. SQL 验证功能不兼容说明

知识库搜索中的"自然语言转 SQL"验证功能使用 PostgreSQL 专有的解析器
（`github.com/pganalyze/pg_query_go`），**在 MySQL 模式下不适用**。

**影响范围**：
- `internal/utils/inject.go` 中的 `ParseSQL`、`ValidateSQL`、`ValidateAndSecureSQL` 函数
- 知识库 "Natural Language to SQL" 搜索功能

**根本原因**：该功能使用 PostgreSQL 官方解析器进行 SQL 语法验证和安全检查
（表名校验、SQL 注入检测、租户隔离注入等）。MySQL 的 SQL 方言（如 JSON 函数、
正则表达式语法、反引号引用等）在此解析器下会解析失败或产生误报。

**解决方案**：
1. MySQL 模式下该功能不可用——维持数据库后端为 PostgreSQL 即可使用
2. 未来可考虑为 MySQL 提供独立的 SQL 验证实现（如基于 `pingcap/tidb/parser`）

## 5. 单元测试与集成测试覆盖

项目已有以下测试文件覆盖 MySQL 相关逻辑：

| 测试文件 | 内容 |
|----------|------|
| `internal/container/database_mysql_test.go` | MySQL DSN 生成测试、retriever 守卫逻辑测试 |
| `internal/database/migration_mysql_test.go` | 迁移路径选择测试、DSN 前缀判断测试 |
| `internal/application/repository/mysql_integration_test.go` | **集成测试**：需真实 MySQL 实例，测试方言分支 |

运行测试：

```bash
# 仅运行 MySQL 相关单元测试（无需外部依赖）
go test -v ./internal/container/ -run TestMySQL
go test -v ./internal/database/ -run TestMySQL

# 运行 MySQL 集成测试（需启动 MySQL 容器）
docker compose -f docker-compose.mysql.yml --profile mysql-test up -d mysql redis
go test -v -run TestMySQL ./internal/application/repository/ -tags=mysql_integration

# 或运行全量单元测试（不依赖外部数据库的测试）
go test -short ./...
```

---

## 6. 常见问题排查

### 6.1 MySQL 连接被拒绝

```bash
# 检查容器是否运行
docker ps | grep WeKnora-mysql

# 检查 MySQL 日志
docker logs WeKnora-mysql | tail -20

# 检查端口是否冲突（默认 3306）
lsof -i :3306
```

### 6.2 迁移失败（dirty state）

```bash
# 查看 dirty 状态
migrate -path migrations/mysql \
  -database "mysql://root:password@tcp(127.0.0.1:3306)/WeKnora?charset=utf8mb4" version

# 强制回退版本（如回退到 -1 重新执行）
migrate -path migrations/mysql \
  -database "mysql://root:password@tcp(127.0.0.1:3306)/WeKnora?charset=utf8mb4" force -1

# 重新执行迁移
migrate -path migrations/mysql \
  -database "mysql://root:password@tcp(127.0.0.1:3306)/WeKnora?charset=utf8mb4&multiStatements=true" up
```

### 6.3 应用启动时数据库错误

常见错误及解决方法：

| 错误信息 | 原因 | 解决 |
|----------|------|------|
| `dial tcp 127.0.0.1:3306: connect: connection refused` | MySQL 未启动 | 检查容器状态 |
| `Error 1045: Access denied for user` | 密码错误 | 检查 DB_PASSWORD |
| `Error 1049: Unknown database` | 数据库不存在 | 检查 DB_NAME 或手动创建 |
| `driver name "postgres" not found` | RETRIEVE_DRIVER 包含 postgres | 设为空或外部引擎 |
| `Migration ended in dirty state` | 迁移中途失败 | 清除 dirty 状态后重试 |

### 6.4 确认当前使用 MySQL

应用启动后，检查日志中的以下行：

```
DB Config: driver=mysql
```

如果没有这行或显示 `driver=postgres`，说明环境变量 `DB_DRIVER=mysql` 未被正确加载。

---

## 6. 测试覆盖矩阵

| 测试项 | 自动化脚本 | 手动 | 验证方法 |
|--------|-----------|------|----------|
| MySQL 连接 | ✅ | ✅ | 日志 `driver=mysql` |
| Schema 迁移 | ✅ | ✅ | `SELECT COUNT(*) FROM information_schema.tables` = 47 |
| 健康检查 | ✅ | ✅ | `curl /health` → `{"status":"ok"}` |
| 用户注册 | ✅ | ✅ | `POST /auth/register` → token |
| 用户登录 | ✅ | ✅ | `POST /auth/login` → token |
| 已认证请求 | ✅ | ✅ | `GET /auth/me` → user info |
| JSON 查询 | ❌ | ✅ | 创建知识库后更新配置 |
| ILIKE 搜索 | ❌ | ✅ | 知识库搜索中文关键词 |
| RETURNING 事务 | ❌ | ✅ | 文档导入（异步任务） |
| 系统设置加载 | ❌ | ✅ | 访问系统设置页面 |
| 清理/停止 | ✅ | ✅ | container down + binary kill |
