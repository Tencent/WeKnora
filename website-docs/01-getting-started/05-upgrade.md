# 版本升级指南

本文适用于已有数据的 WeKnora 部署。Docker Compose 是主要路径，另外补充源码构建、Lite 与 Helm 的升级要点。首次安装请先阅读[安装部署](./02-installation.md)，环境变量说明见[配置说明](./04-configuration.md)。

::: danger 升级前必须备份
升级可能包含数据库迁移。请先备份数据库、文件存储和 `.env`，并安排停止写入的维护窗口。不要执行 `docker compose down -v`、`make clean-db` 或手工删除数据卷；这些操作会删除持久化数据。
:::

## 1. 升级前检查

1. 在 [GitHub Releases](https://github.com/Tencent/WeKnora/releases) 阅读目标版本及中间版本的发布说明，确认是否有额外迁移步骤或不兼容变更。
2. 记录当前版本、镜像和本地修改。若 `git status --short` 有输出，先保存并确认这些改动，不要用 `git reset --hard` 丢弃部署配置。
3. 等待正在上传、解析、生成 Wiki 的任务完成，然后停止新的写入。
4. 确认磁盘空间足以同时容纳备份、新旧镜像和数据库迁移临时空间。

下面命令在 WeKnora 仓库根目录执行，示例环境为 Linux/macOS shell。`vX.Y.Z` 表示准备升级到的实际 Release 标签，例如 `v0.6.3`，执行前必须替换。

```bash
# 记录当前 Git 版本、Compose 目标镜像和实际运行镜像，便于核对与回滚
git rev-parse HEAD
git describe --tags --always
docker compose config --images
docker compose images

# 确认工作树是否有本地修改；有输出时先保存并人工处理
git status --short
```

### 必须保持不变的配置

升级时不要用新的 `.env.example` 覆盖现有 `.env`。应将示例文件中新增或废弃的配置逐项合并到 `.env`，并保持以下值不变：

- `SYSTEM_AES_KEY`：用于解密已保存的 API Key 等敏感数据，丢失后无法恢复；
- `JWT_SECRET`：更改后现有登录令牌会失效；
- `DB_*`、`REDIS_PASSWORD` 和对象存储凭据：必须继续指向原来的数据；
- `STORAGE_TYPE` 与存储路径/桶：不要在版本升级时顺带迁移存储后端。

## 2. Docker Compose 标准升级

### 2.1 创建一致性备份

先停止入口和应用，保留 PostgreSQL 容器运行以执行 `pg_dump`。不要执行 `docker compose down`，因为当前 Redis 没有命名卷，删除容器会丢失其中尚未消费的队列数据。

```bash
# 在仓库同级创建权限为 700 的备份目录，避免把含密钥的 .env 提交进仓库
BACKUP_DIR="../weknora-backup-$(date +%Y%m%d-%H%M%S)"
mkdir -m 700 "$BACKUP_DIR"

# 保存配置、示例配置、当前提交和镜像清单
cp .env "$BACKUP_DIR/env.before"
cp .env.example "$BACKUP_DIR/env.example.before"
cp config/config.yaml "$BACKUP_DIR/config.yaml.before"
git rev-parse HEAD > "$BACKUP_DIR/git-ref.before"
docker compose images > "$BACKUP_DIR/images.before.txt"

# 停止产生写入的核心服务；PostgreSQL 和 Redis 保持运行
docker compose stop frontend app docreader

# 使用容器内的 POSTGRES_USER/POSTGRES_DB 创建 PostgreSQL custom-format 备份
docker compose exec -T postgres sh -c \
  'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom' \
  > "$BACKUP_DIR/postgres.dump"

# 备份 app 的本地文件卷；首次运行会按需拉取小型 busybox 镜像
docker run --rm --volumes-from WeKnora-app \
  -v "$(cd "$BACKUP_DIR" && pwd):/backup" busybox:1.36 \
  sh -c 'tar -C /data/files -czf /backup/data-files.tgz .'

# 两个文件都必须存在且非空
test -s "$BACKUP_DIR/postgres.dump"
test -s "$BACKUP_DIR/data-files.tgz"
```

若使用 Compose 内置 MinIO（`STORAGE_TYPE=minio`），还要备份它的数据卷：

```bash
# 仅在 WeKnora-minio 容器存在时执行，备份 /data 中的对象
docker run --rm --volumes-from WeKnora-minio \
  -v "$(cd "$BACKUP_DIR" && pwd):/backup" busybox:1.36 \
  sh -c 'tar -C /data -czf /backup/minio-data.tgz .'

# 验证 MinIO 备份不是空文件
test -s "$BACKUP_DIR/minio-data.tgz"
```

COS、S3、TOS、OBS、OSS 或外部 MinIO 应使用服务商的版本控制、快照或复制工具完成备份。若还启用了 Neo4j、Qdrant、Milvus、Weaviate 或 Doris，也应按对应产品的官方流程备份其持久化数据。

### 2.2 切换目标版本并合并配置

```bash
# vX.Y.Z 是占位符，替换成 GitHub Releases 中实际存在的标签
TARGET_VERSION=vX.Y.Z

# 获取标签并检出目标版本；detach 模式适合部署目录，不会创建无意义的本地分支
git fetch --tags origin
git switch --detach "$TARGET_VERSION"

# 查看新旧示例配置差异；diff 返回 1 代表“有差异”，不是命令故障
diff -u "$BACKUP_DIR/env.example.before" .env.example || true
```

人工把需要的新增项合并到 `.env`，然后将镜像标签设置为同一个目标 Release。不要填写没有 `v` 前缀的版本号，也不建议生产环境长期使用浮动的 `main` 标签。

```dotenv
# 示例；请改成实际目标 Release 标签
WEKNORA_VERSION=vX.Y.Z
```

```bash
# 展开并校验最终 Compose 配置；无输出且退出码为 0 才继续
docker compose config --quiet

# 核对 app、frontend、docreader 等镜像都指向同一个目标版本
docker compose config --images
```

若使用了 `--profile minio`、`--profile full` 等 profile，升级和启动时继续传入相同的 `--profile` 参数，避免漏拉可选服务镜像。

### 2.3 拉取并启动

```bash
# 拉取与 WEKNORA_VERSION 匹配的镜像
docker compose pull

# 重新创建发生变化的容器；命名卷不会被删除
docker compose up -d

# 观察容器是否进入 running/healthy
docker compose ps
```

`AUTO_MIGRATE=true`（默认）时，app 启动会自动执行数据库迁移。需要特别注意：当前实现遇到迁移失败时只写 Warn 日志，不会阻止进程启动，因此 `/health` 成功不能单独证明迁移成功。

```bash
# 检查本次启动日志，重点确认没有 “Database migration failed”
docker compose logs --since=10m app

# 验证后端健康端点；默认宿主机端口为 8080
curl -fsS http://localhost:8080/health
```

迁移机制和 `AUTO_RECOVER_DIRTY` 的行为详见[数据库与迁移](../06-development/02-database-schema.md)。生产环境不要为了消除错误直接执行 `migrate force` 或 `migrate down`；应先保存日志并确认具体迁移版本。

### 2.4 验收清单

完成以下检查后再结束维护窗口：

- `docker compose ps` 中核心服务为 `running`/`healthy`，app 日志没有迁移、连接或解密错误；
- 能登录，已有用户、空间、知识库和模型配置仍然存在；
- 能打开一篇历史文档并完成一次知识库问答，引用和图片可访问；
- 上传一个小测试文件，解析状态能从等待/处理中变为完成；
- 若使用 Wiki、IM、MCP、网络搜索或可选向量库，对启用的功能各做一次最小冒烟测试；
- 观察任务队列和错误日志一段时间，确认没有持续重试或积压。

## 3. 回滚

若只是新应用镜像启动失败，且数据库迁移保持向后兼容，可先将代码和 `WEKNORA_VERSION` 切回备份中记录的旧版本，再执行 `docker compose pull` 与 `docker compose up -d`。

若新版本已执行不兼容迁移，旧应用可能无法读取新结构。此时应：

1. 立即停止 `frontend`、`app` 和 `docreader`，不要继续写入；
2. 保留失败现场和完整 app 日志；
3. 使用升级前的 `postgres.dump` 恢复到一个空数据库，并恢复同一时间点的文件/对象存储备份；
4. 恢复旧 `.env`、旧代码和旧镜像后再启动；
5. 在副本环境验证数据一致性后才恢复对外服务。

::: warning 不要盲目向下迁移
数据库恢复会覆盖升级后的数据，且 `migrate down` 不等价于完整回滚。没有经过验证的备份时，不要删除数据库、数据卷或强制修改 `schema_migrations`。大版本和跨多个版本升级应先在生产备份的副本上演练。
:::

## 4. 其他部署方式

### 4.1 从源码构建镜像

备份步骤与 Docker Compose 相同。检出目标标签后，先构建前端产物，再重建目标镜像：

```bash
# 构建目标标签对应的前端静态文件
./scripts/build_frontend_dist.sh

# 基于当前源码重建核心镜像，并更新基础镜像
docker compose build --pull app docreader frontend

# 启动并按 §2.3、§2.4 检查迁移日志和功能
docker compose up -d
```

源码构建不要同时拉取同名远端镜像；应通过 `docker compose images` 确认启动的是刚构建的镜像。

### 4.2 Lite / Homebrew

Lite 默认把 SQLite 和本地文件分别保存在 `./data/weknora.db` 与 `./data/files`。停止进程后完整备份 `.env.lite` 和 `data/`，再替换二进制/发行目录并启动；SQLite 迁移同样默认自动执行。不要只备份 `.db` 而漏掉文件目录。

Homebrew 服务可先停止、备份数据目录，再执行升级：

```bash
# 停止后台服务，确保 SQLite 备份一致
brew services stop weknora-lite

# 升级 Formula 安装的软件包；升级前仍需自行备份配置和数据目录
brew upgrade weknora-lite

# 启动并检查日志与历史数据
brew services start weknora-lite
```

### 4.3 Helm

先备份外部数据库和所有 PVC，并保存当前 values 与 manifest。使用目标标签中的 chart，复用原 values 执行 `helm upgrade`，随后检查 rollout、迁移日志和上述功能验收项。

```bash
# 保存当前 release 配置和完整 manifest，文件中可能含敏感信息，请妥善保管
helm get values weknora -n weknora -o yaml > weknora-values.before.yaml
helm get manifest weknora -n weknora > weknora-manifest.before.yaml

# 使用目标版本仓库中的 helm/，复用原配置升级
helm upgrade weknora ./helm -n weknora -f weknora-values.before.yaml

# 等待 app 完成滚动更新，并检查 app Pod 的迁移日志
kubectl rollout status deployment/weknora-app -n weknora
```

实际 release 名、namespace 或 deployment 名若与示例不同，应替换成自己的值。

## 5. 常见问题

### `docker compose up -d` 后还是旧版本

只执行 `up` 可能复用本地缓存镜像。确认 `.env` 中 `WEKNORA_VERSION` 使用带 `v` 的目标标签，然后执行 `docker compose pull`，再用 `docker compose images` 核对镜像。

### 健康检查通过，但页面或查询报数据库字段不存在

这通常表示迁移没有成功。查看 app 启动日志中的 `Database migration failed`，同时核对 `AUTO_MIGRATE`、`AUTO_RECOVER_DIRTY` 和数据库账号权限。不要直接强制迁移版本。

### 新版本启动后模型 API Key 为空

检查 `SYSTEM_AES_KEY` 是否被新的 `.env.example` 默认值覆盖。恢复升级前完全相同的 32 字节密钥并重启 app；如果原密钥已经丢失，已加密数据无法恢复，只能重新填写凭据。

### 可以在 Web 界面一键升级吗

目前没有 Web 一键升级。升级涉及宿主机镜像、数据库与文件备份、迁移验收和回滚，Web 应用本身不应获得 Docker socket 或宿主机管理权限。请按本文流程由部署管理员执行。
