# WeKnora

自托管、多租户的 RAG 知识库与 Agent 平台。本文件记录 agent 协作约定。

## Agent skills

### Issue tracker

Issues 跟踪在 fork 的 GitHub Issues（`UnknownObject777/WeKnora`）。注意：① 所有 `gh` 命令显式加 `-R UnknownObject777/WeKnora`（clone 里同时存在 upstream 腾讯仓库，防止解析错目标）；② fork 的 Issues 功能已手动开启（fork 默认关闭，POST 返回 410 即是此问题）；③ 本机 `gh` 的 POST 请求偶发 TLS 超时但 curl 正常，写操作失败时改用 curl + `gh auth token`。See `docs/agents/issue-tracker.md`.

### Triage labels

使用默认五件套：`needs-triage` / `needs-info` / `ready-for-agent` / `ready-for-human` / `wontfix`。See `docs/agents/triage-labels.md`.

### Domain docs

单上下文：根目录 `CONTEXT.md` + `docs/adr/`（ADR 按需懒创建）。See `docs/agents/domain.md`.

## E2E 测试与验收标准

### 验收分层

每张 ticket 的验收标准按手段分四层标注：

| 层 | 手段 | 验证什么 |
|---|---|---|
| `[unit]` | `go test ./<目标包>/...` | 逻辑正确性（规则引擎、scope 解析、verdict 映射） |
| `[cli]` | CLI 直查 Docker 中间件 | 数据层正确性（表结构、落库数据、队列状态） |
| `[api]` | `curl` 打本地 app HTTP 接口 | 接口行为（策略 CRUD、错误码、鉴权） |
| `[e2e-ui]` | Playwright 驱动真实浏览器 | 用户可见行为（页面元素、操作路径、截图证据） |

### 环境前提

- 中间件：`DOCKER_API_VERSION=1.47 docker compose -f docker-compose.dev.yml -f docker-compose.dev.fix.yml up -d postgres redis`（端口已发布到 localhost）
- 后端：CGO 三件套（gcc + CGO_ENABLED=1 + sqlite3 shim）必需，完整命令见 `docs/agents/dev-environment.md`；lite 模式（sqlite+内存流）无需中间件即可启动，迁移随启动自动执行
- 前端：`cd frontend && npm run dev`
- 浏览器：`npx playwright install chromium` 已执行

### CLI 检查中间件的标准命令

```bash
# postgres
 docker exec WeKnora-postgres-dev pg_isready -U postgres
 docker exec WeKnora-postgres-dev psql -U postgres -d WeKnora -c "\dt"          # 表清单
 docker exec WeKnora-postgres-dev psql -U postgres -d WeKnora -c "SELECT ..."   # 数据断言

# redis（密码见 .env 的 REDIS_PASSWORD）
 docker exec WeKnora-redis-dev redis-cli -a "$REDIS_PASSWORD" ping
 docker exec WeKnora-redis-dev redis-cli -a "$REDIS_PASSWORD" keys '*'
```

### 浏览器验收（Playwright）

- 涉及前端的 ticket 必须附 Playwright 脚本或逐步操作清单 + 截图断言
- 基线流程：打开前端 → 登录/初始化 → 执行 ticket 涉及的操作 → 断言页面元素或网络响应
- 验收失败先查 app 日志和 `docker compose -f docker-compose.dev.yml ps`，再下结论

### 验收标准书写规范（每张 ticket 必须遵守）

1. 每条标准标注验证手段：`[unit]` / `[cli]` / `[api]` / `[e2e-ui]`
2. 禁止不可验证的表述（"功能正常""体验良好"）；必须写成可判定真假的句子
3. 数据变化类标准必须给出具体查询命令和预期结果
4. UI 类标准必须给出操作路径和可观察的页面证据（元素/文本/截图）
5. 拦截类标准必须双向断言：该拦的拦住 **且** 不该拦的放行
6. 涉及观测数据（verdict、span、audit）的标准必须给出查询位置（表名/span 字段/audit action）
