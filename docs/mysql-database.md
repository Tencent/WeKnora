# MySQL 主业务数据库

本分支从 main `e28c68cf552cc1aba2fc923a73b0ab23a33bcb55` 重建。该基线的 PostgreSQL 迁移头为 `000133`；MySQL 保存业务元数据，空库基线以当前 main 的最终结构为目标，逐项对应关系记录在 `migrations/mysql/coverage.json`。发布前须核对清单中的 `main_commit`、`postgres_head` 与实际迁移文件一致。检索数据使用独立向量引擎。

## 版本和验证边界

`coverage.json` 的源文件 SHA-256 按 Git 的 LF 换行计算；校验时仅将 Windows 检出的 CRLF 还原为 LF，其他内容变动仍会使校验失败。

连接校验接受 MySQL 8.0.17 及以上的 8.0 补丁版本，以及 8.4；基线使用的 utf8mb4_0900_bin 决定了这个下界，见 [MySQL 8.0.17 发布说明](https://dev.mysql.com/doc/relnotes/mysql/8.0/en/news-8-0-17.html)。拒绝 MariaDB、未经覆盖的版本系列。必须启用严格 SQL 模式，连接会统一到 UTC。

本轮针对该 main / `000133` 基线，在独立的 **MySQL 8.0.18 / 8.4.8** 实例上完成专项验证，涵盖空库迁移、重复 up、down/up、约束、业务查询、并发与应用数据库初始化；最后的查询和序号分批修复另做了两版本回归。普通包回归、主程序编译及 Compose/Helm 配置渲染也已通过。真实数据库测试结果没有借用此前 `000117` 的记录。

CI 配置固定为 **8.0.46 / 8.4.8**，Compose 固定为 **8.4.8**。2026-10-25 已从本分支源码构建 app、frontend、Docreader，启动全部六个服务，并在 MySQL 8.4.8 上通过六个包的专项测试。普通库级应用账号完成空库初始化，迁移状态为 clean；真实文本上传、2048 维 embedding、Qdrant 召回和 DeepSeek 问答均通过。删除并重建测试容器及网络、保留数据卷后，原账号、会话、文档检索与问答仍可用。GitHub Actions 和性能测试未执行。8.4.8 镜像标签可在 [MySQL 官方 Docker Hub](https://hub.docker.com/_/mysql/tags?name=8.4.8) 核对。

这是**空数据库新部署**方案。旧 MySQL `00-init-db.sql` / `000078` 库的原地升级、PostgreSQL 存量数据搬迁均不在本基线范围内；已有数据时先备份并识别实际 schema，不能直接重放 init 或执行 down。

## 独立 Compose 部署

需要 Docker Compose **2.24+**，用于可选 env_file。入口包含 app、frontend、MySQL、Redis、Docreader 和 Qdrant；不依赖主库 PostgreSQL。frontend、Docreader 和 Redis 通过 `extends` 复用当前 main 的主 Compose 服务定义，app 使用独立的 MySQL 环境配置。

在仓库根目录创建 `.env.mysql`，填入自己的密码和稳定的密钥。该文件包含敏感信息，不要提交。密钥中的 `$` 等字符应按 Compose 的 env 文件规则使用单引号字面值。

```dotenv
DB_DRIVER=mysql
DB_USER=weknora
DB_NAME=WeKnora
DB_PASSWORD='replace-with-application-password'
MYSQL_ROOT_PASSWORD='replace-with-separate-root-password'
REDIS_PASSWORD='replace-with-redis-password'
JWT_SECRET='replace-with-random-jwt-secret'
SYSTEM_AES_KEY='replace-with-exactly-32-byte-key!'
RETRIEVE_DRIVER=qdrant
FRONTEND_PORT=8081
APP_PORT=8080
```

上面的 AES 值是说明性占位符；必须替换为恰好 32 字节的随机密钥，并在更新、重启和恢复时保留同一密钥，否则已加密的数据库字段无法解密。

```sh
docker compose --env-file .env.mysql -f docker-compose.mysql.yml config --quiet
docker compose --env-file .env.mysql -f docker-compose.mysql.yml build
docker compose --env-file .env.mysql -f docker-compose.mysql.yml up -d
docker compose --env-file .env.mysql -f docker-compose.mysql.yml ps
```

必须从包含这次改动的源码构建。`weknora-app:mysql-local` 是本地构建标签，不能用未包含 MySQL 支持的上游 release/latest 镜像代替。默认不向宿主机发布 MySQL 端口；数据库、文件和 Qdrant 数据写入独立 Compose 卷。注册账户后仍须按现有产品流程配置模型和 embedding，才能上传文档和进行检索问答。

本轮已在此 Compose 的 Qdrant 组合上实际验证文本上传、问答和容器重建后的恢复。验证使用 `deepseek-flash` 聊天模型，以及 OpenAI 兼容接口上的 `doubao-embedding-vision`（2048 维）。Langfuse 可配置外部服务；主 Compose 中复用 PostgreSQL 的自建 Langfuse 服务没有移入这个入口。

## 连接设置与迁移

基线迁移会创建图片投影触发器。独立 Compose 显式启用 `log_bin_trust_function_creators=1`，让仅有目标库权限的应用账号在开启 binlog 时也能完成迁移，无需授予应用账号全局 `SUPER` 权限。该设置是服务器级的，会放宽存储函数和触发器创建限制，适用于本 Compose 专用数据库实例。

外部 MySQL（包括 Helm）须由 DBA 选择迁移策略：允许上述服务器配置并授予迁移账号目标库 DDL/触发器权限，或使用有相应权限的迁移账号预先执行 `scripts/migrate.sh up`，再用普通运行账号启动应用并设置 `AUTO_MIGRATE=false`。应用不会自行修改全局权限配置。若此前已遇到 Error 1419 并留下 dirty 状态，仅调整配置不能自动修复：空测试库可重建，已有数据的库必须先检查并修复部分执行的 schema。

| 环境变量 | MySQL 规则 |
| --- | --- |
| `DB_HOST` / `DB_USER` / `DB_NAME` | 必填；Compose 内固定 host 为 mysql |
| `DB_PORT` | 默认 3306；非法值直接报错 |
| `DB_PASSWORD` | 原始密码，不预先 URL 编码 |
| `DB_MYSQL_TLS` | 默认 false；true 使用证书校验，不提供 skip-verify |
| `DB_CONNECT_TIMEOUT` | 正数 Go duration，默认 5s |
| `DB_READ_TIMEOUT` / `DB_WRITE_TIMEOUT` | 正数 Go duration，默认 30s |
| `AUTO_MIGRATE` | 默认 true；false 时由部署方预先完成匹配的迁移 |

应用连接不允许 multiStatements；迁移连接开启。`cmd/database-dsn` 与应用共享 `MySQLDSNs`，包含原始密码的特殊字符处理、UTC、超时和 TLS 设置。脚本不输出 DSN 或密码。

```sh
go install -tags 'postgres mysql' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1
go build -o database-dsn ./cmd/database-dsn
# 将 database-dsn 和 migrate 放入 PATH；MySQL 服务器须已存在数据库和授权。
ENV_FILE="$PWD/.env.mysql" DB_HOST=127.0.0.1 DB_PORT=3306 bash scripts/migrate.sh up
ENV_FILE="$PWD/.env.mysql" DB_HOST=127.0.0.1 DB_PORT=3306 bash scripts/migrate.sh version
```

`.env.mysql` 可以包含外部 MySQL 的 DB_HOST/DB_PORT；脚本会加载它们。共享 Compose 中的应用仍使用 mysql:3306。`DB_URL` 是显式人工迁移覆盖值：mysql 驱动要求 golang-migrate 的 `mysql://` 原生 DSN 格式，不能套用 postgres URL 或添加 sslmode。普通部署优先用单项变量生成 DSN。

迁移失败或 dirty 时应用停止启动；`AUTO_RECOVER_DIRTY=true` 对 MySQL 无效。检查迁移日志及实际 schema，在备份后修复失败 SQL，再由操作人员确认版本；不要未经检查自动 force。`down` 会删除业务表，只用于可丢弃测试库。

## 检索及行为差异

- MySQL 主库不能被内置 `postgres` / `sqlite` 检索引擎复用。可通过管理界面保存外部 vector store，或设置 `RETRIEVE_DRIVER=qdrant` 等外部引擎；留空允许启动后配置，但依赖检索的操作仍须有可用引擎。
- Wiki 的 MySQL 相似候选查询使用 LIKE，没有 PostgreSQL pg_trgm 的模糊相似度排名；关键词查询保留大小写处理和字面转义。
- Memory 复用进程内向量排序路径，候选扫描上限为 5000；这不是 MySQL 原生向量索引。
- 同一 `(task_type, scope_type, scope_id)` 的任务队列领取和清理在短事务内串行；任务实际处理继续并行，不同队列范围独立。该策略保持 dedup_key 分组领取，但高频单范围领取的性能需要实际负载测量。

## 外部 MySQL 的 Helm 配置

Chart 保留 PostgreSQL 默认值。MySQL 由部署方单独管理，现有 Secret 的 `DB_USER`、`DB_PASSWORD`、`DB_NAME` 键继续使用；同时提供包含本改动的应用镜像。

```yaml
database:
  driver: mysql
  host: mysql.example.internal
  port: "3306"
  mysqlTLS: true
postgresql:
  enabled: false
app:
  image:
    repository: your-registry/weknora-app
    tag: mysql-tested-build
  env:
    RETRIEVE_DRIVER: qdrant
  extraEnv:
    - name: QDRANT_HOST
      value: qdrant.example.internal
    - name: QDRANT_PORT
      value: "6334"
secrets:
  existingSecret: weknora-secrets
```

证书须受到镜像信任库信任。Chart 会拒绝缺失 MySQL host 或同时开启内置 PostgreSQL 的配置；Qdrant 服务和必要凭据由部署方提供。

## 可重复验证

真实数据库集成测试会创建并删除独立测试库，需要 CREATE/DROP DATABASE 权限；提供的 DSN 库名必须包含 `test`。未提供 DSN 时测试失败，不会静默跳过。

普通应用账号迁移用例还需要测试管理员拥有 CREATE USER、DROP USER、目标库授权及读取 `mysql.user` 的权限，并要求专用测试服务器配置 `log_bin_trust_function_creators=1`。管理员只创建一次性库和库级权限账号；该账号执行首次及重复迁移，测试检查触发器存在、迁移状态干净且账号没有 `SUPER`。CI 的两个 MySQL 版本均运行此用例。

```sh
go test ./cmd/database-dsn ./internal/database -count=1
WEKNORA_MYSQL_TEST_DSN='root:password@tcp(127.0.0.1:3306)/weknora_mysql_test?parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&multiStatements=true' \
  go test -tags mysqlintegration ./internal/database ./internal/application/repository ./internal/application/service ./internal/application/service/memory ./internal/container ./internal/agent/collaboration -run MySQL -count=1
```

Windows 本地使用项目内 E 盘缓存与临时目录；本次 Python 辅助工具采用 Codex 自带 Python，没有调用 Anaconda。容器构建使用按 Git LF 内容规范化的源码快照，避免 Windows CRLF 检出影响许可证清单解析；Debian 依赖下载使用现有镜像构建参数。GitHub CI 和性能测试仍须在实际执行后记录结果。
