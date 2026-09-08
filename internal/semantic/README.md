# 数据建模模块 (Semantic Modeling)

WeKnora 内置的 BI 语义层模块: 配置数据库连接 → 可视化建模 (Cube.js 语义模型) →
按数据组控制"谁能查什么"。自研 Cube 运行时直接对接 (官方 `cubejs/cube` 镜像),
不再依赖独立中间层。

## 架构

```
WeKnora 前端 (frontend/src/semantic/, 自包含)
   │  /api/v1/semantic/*
后端 internal/semantic/ (自包含: types/repo/service/handler/deployer
   │                     + cubeclient + dbinspector)
   ├─ 元数据: semantic_connections / semantic_models / semantic_model_versions
   │          semantic_data_groups(+members) / semantic_audit_logs (migration 000091)
   ├─ 发布: 校验 YAML → 原子写 model/auto/*.yaml + datasources.yaml → 轮询编译确认
   ├─ cubeclient: 自签 HS256 JWT (CUBEJS_API_SECRET) → Cube /v1/meta /v1/load /v1/sql
   ├─ dbinspector: mysql/postgres/clickhouse/sqlserver 直连探测 (测试连接/表结构)
   └─ 智能体工具: cube_meta / cube_query / cube_sql (internal/agent/tools/cube_tools.go)
        ▼
Cube (cubejs/cube:latest, dev mode 模型热加载)
   └─ driverFactory(datasources.yaml) ──> 业务数据库
```

## 部署

```bash
# .env 中追加:
#   CUBE_ENABLE=true
#   CUBE_API_URL=http://cube:4000/cubejs-api/v1
#   CUBEJS_API_SECRET=<随机串>
#   CUBE_MODEL_DIR=/cube-workspace/model
#   CUBE_DATASOURCES_FILE=/cube-workspace/datasources.yaml

docker compose -f docker-compose.yml -f docker-compose.cube.yml up -d
```

`deploy/cube/` 说明:

- `cube.js` — Cube 运行时配置 (挂载到 `/cube/conf/cube.js`): 按 `ctx.dataSource`
  从 datasources.yaml 取连接参数, 任意驱动参数原样透传; 文件按 mtime 缓存,
  WeKnora 改写后立即生效, 无需重启
- `workspace/` — 运行时状态 (gitignore): `model/` 模型目录、`datasources.yaml` 连接配置。
  首次启动由后端自动初始化; 手工写的 JS/YAML 模型直接放进 `workspace/model/` 即可与
  自动管理的 `model/auto/` 共存 (Cube 同时加载)

也可不启动内置 Cube, 直接指向外部已有实例 (只配 `CUBE_API_URL` + `CUBEJS_API_SECRET`
+ 一个可写的模型目录)。

## 权限模型

**建模权限** (复用租户角色):

| 角色 | 能力 |
| --- | --- |
| viewer | 查看模型/连接/数据组、预览已发布数据 |
| contributor | 建模 (草稿增删改、从表生成、发布前编辑) |
| admin | 全部: 管理数据源、发布/下线/回滚、数据组与成员、审计 |

**数据权限** (数据组 → Cube accessPolicy):

- admin 在模块内维护数据组 (如 sales/purchase/stock) 与成员
- 模型勾选可见数据组 → 发布时写入 YAML 的 `access_policy` (default-deny + admin 恒通过)
- 用户查询时后端按其所属数据组签 JWT securityContext, Cube 强制执行
- 智能体工具同样按当前会话用户的身份过滤: `cube_meta` 只返回可见模型,
  无权查询时给出显式"权限不足"提示 (含 dev 模式泄漏修补)

## 数据源类型

- **引导式**: mysql / postgres / clickhouse / sqlserver — 测试连接、表结构浏览、
  一键生成草稿 (count 度量 + 按列类型推断维度 + 自动软删过滤)。扩展:
  在 `dbinspector/` 注册一个 Adapter 即可
- **透传**: 其余全部 Cube 官方驱动 — 连接表单参数原样下发 datasources.yaml,
  建模从空白 YAML 起步, 发布后由 Cube 编译校验兜底

## 发布流程与版本

1. 草稿 (YAML 为准, 表单是其结构化视图; 复杂 SQL 建议用 YAML 源码模式)
2. 发布: 校验 → 原子写文件 → 轮询 `/v1/meta` 确认编译 → 版本快照 +1
3. 编译失败: 文件自动移除 (不拖垮线上查询), 模型标记 publish_failed 带错误
4. 回滚: 版本历史一键恢复任意快照
5. 下线: 删除模型文件, 草稿与历史保留

## API (/api/v1/semantic)

连接: `GET/POST /connections` `GET/PUT/DELETE /connections/:id`
`POST /connections/test` (未保存参数) `POST /connections/:id/test`
`GET /connections/:id/tables|columns|draft` — 凭据永不下发 (has_password 代替)

模型: `GET/POST /models` `GET/PUT/DELETE /models/:id`
`POST /models/:id/publish|unpublish|rollback` `GET /models/:id/versions`
`POST /models/:id/preview` — 预览按当前用户身份执行, 与真实查询权限一致

数据组: `GET/POST /groups` `GET/PUT/DELETE /groups/:id`
`GET/PUT /groups/:id/members`; 审计: `GET /audit`; 健康与元数据:
`GET /info` `GET /meta`

## 智能体集成

CUBE_ENABLE 时, 智能体配置的"工具勾选"列表出现:

- `cube_meta` — 查看当前身份可见的模型与字段 (建模描述即提示词, title/description 质量直接影响字段选择)
- `cube_query` — 按指标/维度/时间/筛选查数
- `cube_sql` — 展开 SQL 不执行, 用于核对口径

典型链路: `cube_meta` 摸字段 → `cube_query` 查数 → `cube_sql` 核对。

## 安全边界

- 凭据 AES-256-GCM 加密存储 (SYSTEM_AES_KEY, 与平台一致); 响应永不回显
- Cube 4000 端口默认绑定 127.0.0.1 回环 (本地开发需要); 生产部署删除该映射, 仅容器网络内可达
- `datasources.yaml` 含明文密码 (Cube 需要明文建连): 0600 权限写入,
  `deploy/cube/workspace/` 已 gitignore, 注意宿主机目录权限收窄
- 业务库账号建议只读; 模块查询只走 Cube 只读语义层

## 与上游的关系 / 迁移

- 模块关闭 (`CUBE_ENABLE` 未设置) 时行为与上游完全一致
- 对上游共享文件的全部改动 (合并冲突面):
  `router.go` +3 行、`agent_service.go` +5 行、`definitions.go` +10 行、
  `menu.ts` +2 行、`router/index.ts` +6 行、migration 一个新文件、
  `package.json` +2 依赖、`go.mod` +3 驱动依赖; 其余全部在新文件里
- 从 cube-mcp 迁移: 并行运行 → 在本模块重建连接与模型 (旧 JS 模型可原样拷入
  workspace/model 继续生效) → 智能体勾选新工具 → 移除 cube-mcp 的 MCP 服务注册

## 已知限制 (v1)

- 多租户共享一个 Cube 实例时模型文件全局生效; v1 面向单组织自部署,
  跨租户隔离 (per-tenant orchestrator 或多实例) 留二期
- view 类型与预聚合 (pre_aggregations) 通过 YAML 源码模式编辑, 表单暂不覆盖
- 写库 (原 cube-mcp 写入引擎) 未实现; 拖拽画布编辑器留二期
