# ParadeDB 存量库升级

仓库的生产与开发 Compose 使用 `paradedb/paradedb:v0.22.6-pg17`。本文处理 **0.22.2 → 0.22.6、PostgreSQL 主版本保持 17** 的升级；不适用于 PostgreSQL 跨主版本升级，也不代表 Helm 中其他旧版本可以直接套用。

## 升级步骤

所有命令在仓库根目录执行。开发环境的数据库命令使用 `docker compose -f docker-compose.dev.yml`，开发后端在宿主机上手动停止；开发 Compose 没有 `app` 服务。保留原数据卷，**不要执行 `down -v`、删除卷或换成 PG18 镜像**。

1. 停止 app 和其他数据库写入方；启用了 Langfuse 时也要停止其 web/worker，本地运行的开发后端同样需要停止。

   ```bash
   # 标准部署；开发环境请停止宿主机上的后端进程
   docker compose stop app
   # 仅在启用了 Langfuse 时执行
   docker compose stop langfuse-web langfuse-worker
   ```

2. 备份所有数据库和角色，包括 Langfuse 库，并确认能够恢复。备份存放在数据库数据卷之外。

   ```bash
   umask 077
   docker compose exec -T postgres sh -c 'pg_dumpall -U "$POSTGRES_USER"' > paradedb-before-upgrade.sql
   ```

   若旧镜像在当前 CPU 上无法启动，先保存停止状态的数据卷快照，在兼容机器上制作逻辑备份或验证快照可恢复，再改动唯一的数据副本。

3. 使用更新后的 Compose，只替换数据库容器：

   ```bash
   docker compose pull postgres
   docker compose up -d --no-deps --wait postgres
   ```

4. 完成扩展的 SQL 升级。仅替换镜像不会更新已有数据库内的 `pg_extension` 版本。

   ```bash
   docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1' <<'SQL'
   ALTER EXTENSION pg_search UPDATE TO '0.22.6';
   SELECT extname, extversion FROM pg_extension WHERE extname IN ('pg_search', 'vector');
   SELECT * FROM paradedb.version_info();
   SQL
   ```

   在**其他已安装 `pg_search` 的数据库**中同样执行，包括安装过该扩展的 `postgres`、模板库或 Langfuse 库。不要为了升级而向原本不需要的数据库安装扩展。catalog 与 `paradedb.version_info()` 应均报告 0.22.6。

5. 恢复原先运行的写入服务，确认数据库迁移状态，执行有代表性的关键词与向量检索。这个补丁升级无需专门重建全部索引或重新导入文档；保留备份直到验证完成。

## 自动迁移的范围

迁移 `000099` 仅在 WeKnora 数据库内执行扩展升级：已安装版本须为 0.22.2–0.22.5，且服务器提供 0.22.6 扩展包。它遵守 `app.skip_embedding`，不安装缺失的扩展，也不处理其他版本线。

如果这条迁移在替换数据库镜像**之前**已经执行，安装新镜像后不会自动再跑一次，需要手动执行上面的 SQL。没有 `000099` 的旧应用也需手工升级。其他数据库的扩展不能靠 WeKnora 的迁移代管。

## 回滚与复现验证

回滚需要将升级前备份/快照恢复到独立数据卷，配合旧镜像使用。仅把镜像标签改回旧版不会撤销扩展 SQL 变更，`000099` 的 down 文件也不会尝试降级扩展。

仓库提供隔离验证脚本：

```bash
docker pull paradedb/paradedb:v0.22.2-pg17
docker pull paradedb/paradedb:v0.22.6-pg17
python3 scripts/test_paradedb_upgrade.py
```

脚本使用临时容器和卷，不映射端口；验证同一数据卷升级、表内容、关键词/向量检索、迁移幂等性和重启后结果，并输出备份与日志。该用例验证固定测试数据，部署时仍需检查自己的数据和检索负载。
