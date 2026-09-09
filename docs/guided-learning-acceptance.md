## 引导式学习验收记录

`feat/guided-learning` 分支已基于 `3e6010e7` 实现限定范围的 v1，包括个人授权、Wiki 主题历史与图状态、基于证据的练习、BKT 估计、推荐、三个 Agent 工具、导出和删除。隔离开发环境仍在运行；真人学习效果和生产规模性能尚无验证结论。

设计见 [guided-learning.md](guided-learning.md)，用户流程见[功能指南](../website-docs/03-features/22-guided-learning.md)，算法和数据集限制见[评估说明](guided-learning-evaluation.md)。

### PR 审查修复

2026-09-10 合入官方 `main`（`b60351f8`），同时保留 MCP 路由句柄和学习工具策略。学习迁移改为 PostgreSQL `000093`，避免与主线 `000092_mcp_metadata` 冲突；SQLite 仍为 `000014`。文档站的仓库外相对链接已改为 GitHub 链接。

数据库未知故障返回 `learning_unavailable` / HTTP 500；可重试锁竞争和容量限制继续返回 429。日志只保留错误类别和安全数据库错误码，不记录 SQL、参数或原始错误正文。

本轮结果：Go 全仓测试、`go vet`、定向 race、真实 PostgreSQL/SQLite 仓储测试通过；增量 golangci-lint v2.12.2 为 0 项问题；前端 808 项测试、类型检查和生产构建通过；文档站构建通过。迁移源加载、MCP/学习策略共存、缺表及断连分类有新增回归测试。未重跑真实模型和浏览器端到端验收，下面保留的是首版记录。

本地日志分别为 `.runtime/logs/pr3145-fix-go-all.log`、`pr3145-fix-lint.log`、`pr3145-fix-postgres.log`、`pr3145-fix-race.log`、`pr3145-fix-frontend-tests.log`、`pr3145-fix-frontend-types.log`、`pr3145-fix-frontend-build.log` 和 `pr3145-fix-docs-build.log`，均未进入版本控制。

本次只处理四项 P1/P2 问题和合并冲突；本机取证材料整理、恢复扫描和 Wiki 重命名锁范围仍为单独的 P3 工作。

### 首版验证记录

记录时间为本地时间 2026-09-09，证据文件名使用 UTC。下列日志路径均相对于当前工作树中已忽略的 `.runtime/` 目录。

| 检查项 | 结果 | 证据 |
| --- | --- | --- |
| Go 全仓测试 | `go test -p 8 ./...` 通过 | `logs/go-test-all.log` |
| 定向竞态测试 | 类型、学习服务、仓储层、工具、对话模型、中间件、处理器、路由和容器均通过 | `logs/learning-race-final.log` |
| 真实 PostgreSQL 和 SQLite | 学习事务与 Wiki 重命名测试在 `-race` 下通过；PostgreSQL 用例使用隔离的临时 schema | `logs/learning-postgres-final.log` |
| 静态分析 | 变更涉及的同一组后端包通过 `go vet` | 见下方命令 |
| 前端 | 693 项测试通过，0 项失败或跳过；TypeScript 检查通过 | `logs/frontend-test.log`、`logs/frontend-typecheck.log` |
| 前端生产构建 | 37.75 秒完成；已有的大分块警告仍存在 | `logs/frontend-build.log` |
| 真实 HTTP 和 Agent | 使用 DeepSeek 的 36 项检查通过；另计 15 条观察结果 | `evidence/api-acceptance-20260908T232006-57726.json` |
| 浏览器最终回放 | 11 项通过、3 项跳过；Chromium 145，桌面端 1280x900，移动端 390x844 | `logs/browser-delivery.tap` |
| 运行环境 | API、前端代理、PostgreSQL、Redis 和 Qdrant 检查通过；migration 92 无异常，共五张学习表 | `evidence/application-smoke.json`、`evidence/middleware-smoke.json` |
| 合成算法测试 | 八个排序用例、九个状态快照和八条答题轨迹通过算术与状态检查 | `logs/learning-offline-final.log` |

真实 HTTP 验收会生成一份三题测验，确认作答前不返回答案，提交并重试答案，检查冲突提交和跨租户访问，随后导出并删除一次性 `learner_b` 档案。测试通过 `/agent-chat` 执行全部三个学习工具；是否执行以成功的工具返回值为准，重复的 SSE 进度事件不计。验收数据使用模型 `deepseek-v4-flash`，测验 prompt 为 `learning-quiz-v1`。

最终的来源删除回归测试覆盖软删除、硬删除、租户迁移、未被题目引文直接引用的生成来源、多来源页面保留、退出授权后的有界恢复、新评估保留，以及导出与 PostgreSQL 删除并发。测验独立保存来源文档 ID，不依赖当前 Wiki 引用。导出在档案锁和来源锁下清除不可用来源的证据；恢复流程无需等待导出，也会执行同样的清理。普通来源编辑会保留历史答案，并将测验标记为过期。

最终浏览器测试回放此前生成并完成作答的真实测验，以及已持久化的 Agent 卡片；另有明确标记的 mock-503 恢复用例。该测试不会生成新测验，也不会重新提交已保存的答案。三项跳过内容分别为：来源现已有效，无法执行来源不完整拒绝；重复的独立导出；会清空 `learner_b` 的破坏性操作。此前的浏览器清除确认测试在 `learner_b` 历史为空时通过；非空历史的删除由真实 HTTP 测试确认，该 UI 测试不构成相关证据。

浏览器证据位于 `evidence/guided-learning-2026-09-08T23-21-24-535Z-61307/`，运行期间源码哈希未变化。此前的清除测试位于 `evidence/guided-learning-2026-09-08T22-23-38-440Z-4123598/`。截图已遮盖账号身份；导出文件仍含合成练习记录，需保持私密。

### 复现

使用 `go.mod` 指定的 Go 工具链，并按仓库 lockfile 安装前端依赖。在工作树根目录执行：

```bash
go test -p 8 ./...
go test -race ./internal/types ./internal/application/service/learning \
  ./internal/application/repository ./internal/agent/tools \
  ./internal/models/chat ./internal/middleware ./internal/handler \
  ./internal/router ./internal/container
go vet ./internal/types ./internal/application/service/learning \
  ./internal/application/repository ./internal/agent/tools \
  ./internal/models/chat ./internal/middleware ./internal/handler \
  ./internal/router ./internal/container
npm --prefix frontend test
npm --prefix frontend run type-check
npm --prefix frontend run build
go test ./internal/application/service/learning \
  -run TestLearningOfflineEvaluation -v -count=1
```

以下命令仅适用于当前保留的开发数据库，凭据通过环境变量传入：

```bash
set +x
source .runtime/secrets.env
export LEARNING_TEST_POSTGRES_DSN="host=127.0.0.1 port=25432 user=weknora-gl password=$DB_PASSWORD dbname=weknora-gl sslmode=disable"
export WEKNORA_WIKI_RENAME_TEST_DSN="$LEARNING_TEST_POSTGRES_DSN"
go test -race ./internal/application/repository \
  -run 'Learning|Wiki.*Rename|Rename.*Wiki' -count=1 -v
```

真实 HTTP 验收会显式授权，并且只清除一次性 `learner_b` 档案。请勿与浏览器隐私测试并发运行：

```bash
python3 -B scripts/test-guided-learning-api.py inspect
python3 -B scripts/test-guided-learning-api.py run --consent-learner-b
```

浏览器回放使用保留的 `learner_a` 测试数据。将 `PLAYWRIGHT_MODULE_PATH` 指向已安装且带有 Chromium 二进制文件的 Playwright 包：

```bash
GL_SINGLE_PROCESS=1 GL_RUN_QUIZ=1 GL_REPLAY_QUIZ=1 \
GL_EXISTING_QUIZ_ID=8203a58e-8761-46d6-9784-81ff78cad9c8 \
GL_CARD_SESSION_ID=8959ba30-9dc6-408f-a83c-d0955168b1c8 \
GL_RUN_MOCK_ERRORS=1 GL_RUN_LEARNER_B_CHECKS=1 \
PLAYWRIGHT_MODULE_PATH=/home/liudebao/opensource/WeKnora-sandbox-workbench/.runtime/node_modules/playwright \
node --test frontend/e2e/guided-learning.spec.mjs
```

这些 ID 仅属于当前保留的测试数据。重新解析或重新播种可能替换页面与分块标识，再次运行前需检查 `.runtime/evidence/seed.json`。`GL_SINGLE_PROCESS=1` 用于适配当前主机的浏览器沙箱限制。此前的导出测试虽通过断言，仍出现 Chromium 退出警告；该模式不能证明浏览器进程已隔离。

### 保留的演示环境

工作树：`/home/liudebao/opensource/WeKnora-guided-learning`。前端地址为 `http://10.37.40.48:25173/`，也可通过 IDE 转发访问 `http://127.0.0.1:25173`。API 和中间件仅绑定回环地址。该 Vite 服务不得暴露到公网。

私有文件 `.runtime/seed-accounts.json` 包含两个一次性登录账号。`learner_a` 保留授权状态、六条已保存答案、两份测验和一段可回放的工具卡片对话；其内置 Guided Learning Agent 已绑定现有模型和 Wiki 知识库。验收完成后，`learner_b` 保持禁用且无数据。测试未使用真实用户账号或同级环境。

```bash
bash .runtime/runtime.sh status
bash .runtime/runtime.sh start
bash .runtime/services.sh api-rebuild
python3 .runtime/verify-app.py
# 只停止本运行环境，保留数据库卷和凭证：
bash .runtime/runtime.sh stop
```

私有文件 `.runtime/README.md` 记录环境设置、进程归属、数据播种和凭据处理方式。`.runtime/configure-demo.py` 可幂等恢复 `learner_a` 的 Agent 绑定，且不改变授权状态。禁止将 `.runtime/` 纳入提交或 Docker 构建上下文。SQL、JWT、加密和模型密钥保存在仅所有者可读的文件中，公开报告不得包含具体值。

### 限制

- V1 仅允许已登录 Web 用户访问当前工作区自有的 Wiki 知识库。共享内容或 Agent、API key、IM 和 Embed 均不在范围内；长期课程计划和推断式先修图延后处理。
- BKT 参数尚未校准。在实现助手编写的合成数据集上，BKT Brier 为 0.277601，固定先验为 0.264400，数值越低越好。排序 NDCG@5 在开发集修正后为 0.813841，该结果不属于未经调整的 heldout 评估。
- 来源原文精确引用和同模型盲测一致，仍不足以证明语义正确性或教学质量。当前没有参与者研究，也没有真人学习收益数据。
- Wiki 重命名会锁定知识库中的存量页面，以保留 UUID 和引用。检索投影发生变化时可能需要重建索引，并会给出警告；尚未测量大型知识库的重命名延迟。
- 每个档案最多包含 2,000 条节点记录、1,000 份测验，以及四份 pending/running 测验。完整生产负载、跨供应商行为和大型档案导出延迟均未测量。
- 清除一个知识库时会保留其他知识库的历史，但也会推进档案 epoch，使其他知识库中 active/ready 状态的测验过期。该机制用于阻止隐私操作后延迟发布旧任务。
- 最终集成检查覆盖身份准入、来源新鲜度、epoch/lease fencing、幂等性、日志和删除。独立风险检查发现并推动修复了上述来源删除缺口；尚未取得完整的独立签字确认。
