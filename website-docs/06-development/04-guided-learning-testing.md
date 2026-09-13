# 引导式学习测试手册

本手册覆盖夹具准备、真实 API、桌面与移动端操作、数据隔离和删除。日常使用从[使用手册](../03-features/24-guided-learning.md)开始；本页的测试会创建账号、调用模型或删除测试记录，请勿连接生产实例。

## 测试范围

按下表顺序执行，不要同时运行 seed、API 验收和浏览器验收。多个进程会共用题集、授权和清理状态，干扰结果。

| 阶段 | 服务与模型要求 | 数据影响 | 通过标准 |
| --- | --- | --- | --- |
| 离线回归 | 不需要运行中的服务或模型密钥 | 临时测试数据库和文件 | 命令退出码为 0 |
| seed | 专用后端、数据库、队列和真实对话模型 | 创建账号、独立空间、模型、知识库、来源和 Wiki 页面 | 六份来源均完成解析，固定页面引用有效 |
| inspect | 已初始化的后端，无模型调用 | 登录会话、本地结果；导出会清理失效来源记录 | 身份与模型绑定检查通过，来源状态可读 |
| API run | 同上，并调用模型 | `learner_b` 的授权、练习、临时会话及学习数据清理 | 启用断言通过，无 `fail` |
| 浏览器默认用例 | 前后端及 Chromium | `learner_a` 授权、阅读信号和导出 | 导航、来源、图谱、布局和隔离断言通过 |
| 浏览器真实测验 | 同上，并调用模型 | 三题作答；按开关创建 Agent 与会话 | 判分、引文、刷新恢复通过 |
| 浏览器删除用例 | 同上 | 永久删除 `learner_b` 当前知识库学习数据 | 非空删除成功，`learner_a` 导出摘要不变 |

`pass`、`observed`、`skip` 分开统计。`observed` 是状态或数量记录，`skip` 是未启用或不满足前置条件的场景，均不能算作通过。真实模型没有生成合格题目时应报告失败，不能用模拟答案补齐通过数。

## 环境准备

### 启动与配置

按[从源码启动](../03-features/24-guided-learning.md#从源码启动)启动独立 checkout 的前后端及基础设施。需要 Linux/macOS、Go 1.26、CGO 编译工具、Node.js 24、Python 3.10+。浏览器使用 Node Test Runner 驱动 Playwright，无需另建 Playwright 配置。

专用测试后端必须允许注册，注册后自动创建个人空间，且关闭跨空间访问。按使用手册启动的新实例已具备以下配置；已有实例才需核对 `.env`，修改后重启：

```dotenv
DISABLE_REGISTRATION=false
WEKNORA_AUTH_DEFAULT_TENANT_MODE=create_personal
WEKNORA_TENANT_ENABLE_CROSS_TENANT_ACCESS=false
```

数据库系统设置若已将注册模式改为 `invite_only`，只改环境变量不能恢复公开注册。请由测试实例的系统管理员改为 `self_serve`，或使用全新的专用实例。单独更换运行目录不会隔离后端数据。

后续命令在仓库根目录执行；新终端需重新设置三个变量：

```bash
export GL_RUNTIME_DIR=.runtime/guided-learning
export GL_API_BASE=http://127.0.0.1:8080
export GL_FRONTEND_BASE=http://127.0.0.1:5173

curl --fail --silent --show-error "$GL_API_BASE/health"
curl --fail --silent --show-error "$GL_API_BASE/api/v1/auth/config"
curl --fail --silent --show-error "$GL_FRONTEND_BASE/api/v1/auth/config"
```

健康接口应返回 `{"status":"ok"}`；两次认证配置请求都应返回 JSON，不能是 Vite 的 HTML 首页。浏览器与脚本必须访问同一后端。健康检查不能替代迁移和异步任务检查。

脚本只接受 HTTP loopback 地址：`127.0.0.1`、`localhost` 或 `[::1]`。地址不能带账号、查询参数或片段。API 地址写入清单后，不要更换端口或 loopback 写法。远程开发机上的测试命令也在开发机执行；人工访问可使用 SSH 端口转发。

### 运行目录与密钥

`GL_RUNTIME_DIR` 必须位于当前 checkout 的 `.runtime/` 内，不能使用软链接或其他仓库的账号清单。checkout 本身不要嵌套在另一仓库的 `.runtime/` 下。

seed 需要三个变量：

```dotenv
MODEL_NAME='实际可用的模型名'
MODEL_BASE_URL='https://模型服务的OpenAI兼容入口'
MODEL_API_KEY='你自己的模型密钥'
```

可由密钥管理工具注入进程环境；也可写入当前用户所有、权限为 `0600` 的普通文件，再传 `--model-env /absolute/path/model.env`。文件按字面值解析，不能写 `export`、变量展开或 shell 命令。模型地址使用 HTTPS，不要填 `/chat/completions` 的完整请求路径。

模型须支持 OpenAI 兼容对话与 Tool Calling。seed 的知识库仅开启 Wiki，关闭向量、关键词和知识图谱索引，因此不需要 Embedding、Rerank 或 Neo4j；文档分块与异步处理仍须可用。不要手动更改夹具的索引策略。

不要把密钥放进命令参数、Git、截图或报告。后续 inspect、opt-in、configure-agent、API 与浏览器测试使用已保存的模型 ID，无需再提供模型密钥。

## 离线回归

首次安装依赖需要网络；以下测试执行时不访问真实模型：

```bash
go test ./internal/application/service/learning ./internal/application/repository ./internal/types ./internal/database
go test -race ./internal/application/service/learning ./internal/application/repository ./internal/types
go test ./internal/application/service/learning -run '^TestLearningOfflineEvaluation$' -count=1 -v
python3 -B -m unittest discover -s scripts -p 'test_guided_learning_fixtures.py' -v
# 启动阶段已安装依赖时跳过下一行
npm --prefix frontend ci
npm --prefix frontend run test:e2e:config
npm --prefix frontend run type-check
```

仓储测试默认使用临时 SQLite。验证 PostgreSQL 时，通过安全的环境注入为 `LEARNING_TEST_POSTGRES_DSN`、`WEKNORA_WIKI_RENAME_TEST_DSN` 提供专用测试库连接，再运行仓储测试。这些测试会创建和删除临时 schema，禁止使用生产数据库。

需要完整回归时，再执行：

```bash
go test ./...
go vet ./...
npm --prefix frontend exec -- sh -c \
  "find src -type f \( -name '*.test.ts' -o -name '*.test.mjs' \) -print0 | sort -z | xargs -0 npx tsx --test --test-concurrency=1"
npm --prefix frontend run build
npm --prefix website-docs ci
npm --prefix website-docs run check
npm --prefix website-docs run build
```

前端测试串行执行，避免固定 WebSocket 端口争用。算法评估使用固定合成数据，适合检查回归，不能证明真人学习收益；记录 NDCG、Brier 等指标时，应一并记录基线。

## 创建测试数据

### 初始化夹具

确认允许创建一次性账号和发生模型费用后执行：

```bash
# 已通过当前进程环境提供 MODEL_* 时
python3 -B scripts/seed-guided-learning.py seed --consent-create-fixtures

# 使用私密文件时，替代上一条，不需要两条都运行
# python3 -B scripts/seed-guided-learning.py seed \
#   --consent-create-fixtures --model-env /absolute/path/model.env
```

seed 通过公开 API 创建 `learner_a`、`learner_b` 两个一次性用户，每人拥有独立空间、一个知识库和一个模型配置。每个库发布三份合成来源，并建立三个带实际分块引用的固定 Wiki 页面。

后台自动生成的其他 Wiki 页面数量可能变化，不应断言整个知识库恰好只有三个页面。固定夹具用于稳定定位主题，不能替代对原生 Wiki 自动生成质量的人工检查。

脚本等待来源达到 `completed`，只引用非空、已启用且 `index_status=ready` 的分块。默认每份文档最多等待 900 秒；可用 `--timeout 1800` 调整，允许范围为 1-3600 秒。

成功后应存在：

| 文件 | 用途 | 保管要求 |
| --- | --- | --- |
| `$GL_RUNTIME_DIR/seed-accounts.json` | 随机账号、密码、用户/空间/知识库/模型 ID | 私密文件，权限 `0600` |
| `$GL_RUNTIME_DIR/evidence/seed.json` | 六份来源、六个固定页面及分块清单 | 检查后再分享 |
| `$GL_RUNTIME_DIR/evidence/seed-*-graph.json` | 固定页面之间的图链接检查 | 检查后再分享 |

seed 不开启学习记录。以下两个动作分别授权学习和配置 Agent：

```bash
python3 -B scripts/seed-guided-learning.py opt-in --user learner_a
python3 -B scripts/seed-guided-learning.py configure-agent --user learner_a
python3 -B scripts/test-guided-learning-api.py inspect
```

预期 `learner_a` 已开启，初次 seed 的 `learner_b` 仍关闭；两人的来源各有三份 `completed`。重跑时学习记录数量可能非零。`configure-agent` 只绑定已有模型和知识库，不改变学习授权。

### 人工登录夹具

在本机编辑器中私下打开 `seed-accounts.json`，找到 `users.learner_a` 的 `email`、`password`，访问 `GL_FRONTEND_BASE` 的登录页。不要把账号文件打印到共享终端、聊天或截图中。知识库名称为 `Topic4 Synthetic Learning learner_a`。

按[练习流程](../03-features/24-guided-learning.md#练习流程)体验。需要同时验证两个账号时，用独立浏览器配置或互相隔离的会话，避免登录状态覆盖；不要在自动测试运行期间手动作答或删除。

## 真实 API 验收

确认 `learner_b` 的学习记录可以被清空后执行：

```bash
python3 -B scripts/test-guided-learning-api.py run --consent-learner-b
```

脚本在 `learner_b` 上检查默认关闭与授权、概览、推荐、阅读、overlay、真实出题和判分，验证 attempt 幂等与冲突、另一租户不可读取、Agent 工具及隐私清理。它选择固定选项交给服务端判分，不要求碰巧答对。

结果写入 `$GL_RUNTIME_DIR/evidence/api-acceptance-*.json`。检查退出码和每项 `outcome`，不能只看文件是否生成。正常结束后 `learner_b` 关闭学习且学习记录清空，`learner_a` 历史保持不变。进程被强杀时不保证清理完成。

`--source-timeout`、`--quiz-timeout`、`--agent-timeout` 默认分别为 180、360、300 秒，允许范围分别为 1-900、180-900、180-900 秒。增大超时前先检查模型与队列。

## 浏览器验收

### 安装与基本路径

```bash
npm --prefix frontend exec -- playwright install chromium
npm --prefix frontend run test:e2e:guided-learning
```

Linux 提示缺少系统库时，由开发机管理员安装 Playwright 所需依赖。测试不会自动启动前后端。默认运行覆盖桌面 `1280x900`、移动端 `390x844` 的导航、图谱、来源、导出与隔离。

正常夹具下，默认用例不会生成测验；若来源异常，不完整证据用例会尝试真实 prepare 请求，仍可能调用模型。

### learner_a：真实测验与 Agent 卡片

API 验收结束后，再顺序运行：

```bash
GL_RUN_QUIZ=1 GL_CREATE_CARD=1 GL_RUN_LEARNER_B_CHECKS=1 \
  npm --prefix frontend run test:e2e:guided-learning
```

该组检查真实出题、三题提交、来源引文、刷新恢复，以及 Agent 工具生成的测验卡片。`learner_a` 保留作答；`learner_b` 必须处于关闭状态，用于检查关闭后仍可导出和取消删除。新建的 Agent 与会话不会自动删除。

### learner_b：非空删除

这组先生成真实作答，再通过界面清空 `learner_b` 当前知识库，比较 `learner_a` 导出摘要：

```bash
GL_RUN_QUIZ=1 GL_QUIZ_ACTOR=learner_b GL_ALLOW_LEARNER_B_CLEAR=1 \
  node --test --test-name-pattern='APPROVAL: real quiz|APPROVAL: learner_b' \
  frontend/e2e/guided-learning.spec.mjs
```

不要加入 `GL_CREATE_CARD=1`。创建卡片只支持 `learner_a`；选择 `learner_b` 必须显式允许清理。只开启清理、却没有生成作答，不能证明非空删除通过。清除后的页面读取可能重新记录阅读信号，不应断言所有计数永久为零。

### 可选场景

模拟 HTTP 错误单独运行、单独报告：

```bash
GL_RUN_MOCK_ERRORS=1 node --test --test-name-pattern='MOCK ERROR ONLY' \
  frontend/e2e/guided-learning.spec.mjs
```

重放 `learner_a` 在本夹具 `concept/topic4-retrieval` 页面上已全部答完、仍为 `ready` 的题集；使用上一组真实测验结果中的 `quiz.id`，不能填其他主题的题集：

```bash
GL_RUN_QUIZ=1 GL_REPLAY_QUIZ=1 GL_EXISTING_QUIZ_ID='<真实的quiz UUID>' \
  node --test --test-name-pattern='APPROVAL: real quiz' \
  frontend/e2e/guided-learning.spec.mjs
```

重放只能证明已有结果恢复，不能算一次新出题。仅设置 `GL_EXISTING_QUIZ_ID` 不等于重放，测试仍可能生成新题。不要同时开启 `GL_CREATE_CARD`。

已有 `learner_a` 会话须包含成功的 `prepare_learning_quiz` 卡片，指向夹具三个固定页面之一的 `ready` 题集，且个人学习记录仍开启。优先使用上一组 `card-fixture.json` 中的 `session_id`：

```bash
GL_CARD_SESSION_ID='<真实的session UUID>' \
  node --test --test-name-pattern='APPROVAL: real existing learning tool card' \
  frontend/e2e/guided-learning.spec.mjs
```

布尔开关仅值 `1` 生效。像示例一样让开关仅作用于单条命令，避免残留在 shell 中影响下一组测试。

结果写入 `$GL_RUNTIME_DIR/evidence/guided-learning-<时间戳>-<进程号>/`，包括 `summary.json`、`report.md`、用例结果、导出和截图；创建卡片时另有 `card-fixture.json`。早期参数错误可能还未生成结果文件。

真实出题默认等待 180000 毫秒；`GL_QUIZ_TIMEOUT_MS` 使用正数，最大 300000。受限开发机可用 `GL_SINGLE_PROCESS=1` 兼容 Chromium 子进程限制，报告必须注明；这不验证浏览器进程隔离。

## 人工验收清单

对普通自建知识库执行这份清单，可补足固定夹具未覆盖的首次使用路径。逐项记录通过、失败或未执行及原因。

| 场景 | 操作 | 预期结果 |
| --- | --- | --- |
| 默认授权 | 新用户打开自有 Wiki | 学习默认关闭，须本人开启 |
| Wiki 准备 | 上传资料并等待处理，打开概念页和来源 | 页面已发布，来源可读，引用分块非空且启用 |
| 熟悉度 | 开启后阅读主题，查看状态 | 可标记「已接触」，不会仅因阅读成为「已掌握」 |
| 出题 | 点击「练习当前页面」 | 最终出现三道四选一题；未作答时无答案 |
| 等待恢复 | 「停止等待」后「再次检查」 | 恢复查询；停止等待不取消后台任务 |
| 作答恢复 | 提交一题后，在同一标签页刷新 | 保留判分，不重复增加有效作答 |
| 图谱跳转 | 选另一节点，关闭详情，切回 Wiki | 标题、URL slug 与练习对象一致 |
| Agent | 请求推荐并准备练习 | 工具结果可打开页面/测验，用户在卡片提交答案 |
| 关闭 | 关闭后尝试练习、打开学习数据菜单 | 不允许新练习，仍可导出和清除 |
| 取消删除 | 打开清除确认后点击「取消」 | 原练习仍在 |
| 非空删除 | 一次性账号答题、导出，再永久删除 | 删除计数非零；另一账号记录不变 |
| 来源变化 | 仅在测试库修改引用分块，提交旧题集 | 提示来源变化，旧题不能继续提交 |
| 来源删除 | 仅在测试库删除引用来源，再导出 | 涉及该来源的题集及答案被清理 |

来源变化、删除和页面重命名等行为另由仓储/服务测试覆盖。不要为触发分支而修改真实学习资料。

## 失败处理与复跑

| 现象 | 检查与处理 |
| --- | --- |
| 注册被拒绝或两账号落在同一空间 | 核对生效的注册模式与 `create_personal`；不手改清单中的身份 |
| API / frontend 地址不匹配 | 恢复 seed 地址；更换后端实例时换新的 `GL_RUNTIME_DIR` |
| 账号文件或路径校验失败 | 核对 checkout、所有者、`0600` 权限及软链接；勿使用 `chmod 777` |
| `fixtures_in_use` | 等待持锁进程退出，不删锁绕过检查；E2E 仍需协调串行 |
| 来源超时 | 查看后端、docreader、队列和模型限流 |
| 来源完成但不可出题 | 检查分块非空、启用、索引状态及页面引用 |
| `fixture_source_edited` 或引用变化 | 核对人工修改，再决定是否刷新一次性夹具 |
| 测验失败或超时 | 核对模型、结构化输出、引文和队列；保留失败，不换 mock 冒充成功 |
| Chromium 未找到 | 重跑浏览器安装；系统依赖与模型失败分别排查 |
| 导出返回 429 | 避免重复或并发导出，等限流窗口恢复后再跑 |
| 清理后题集不可读 | 删除的预期结果；重新开启并生成，不复用已删除的 quiz ID |

初始化中断后，保留 `seed-accounts.json`，用同一目录、地址和模型配置重跑 seed。不要删除账号文件后再次注册同一组夹具。更换模型名称或地址应建立新夹具；重复 seed 不会自动更新既有模型密钥。

只重提失败来源：

```bash
python3 -B scripts/seed-guided-learning.py reparse --user all
```

已运行任务只等待，完成后重跑 seed 检查引用。只有确认允许覆盖页面编辑及丢弃相关页面历史后，才执行：

```bash
python3 -B scripts/seed-guided-learning.py seed \
  --consent-create-fixtures --refresh-pages
```

这条命令仍需要 MODEL 配置；使用文件时仍需传 `--model-env`。正文变化使用版本检查更新，失效引用可能导致删除并重建页面。不要把 `--refresh-pages` 当作通用修复。

脚本没有删除账号、知识库、模型或整个实例的全局清理命令。学习面板只清除个人学习数据。测试后先停止前后端，再停止基础设施；销毁测试卷前确认归属，勿运行针对其他实例的 `clean-db`。

## 记录测试结果

报告保留完整代码 SHA、运行时与数据库/浏览器版本、模型名称、命令及非敏感开关、执行时间、通过/失败/跳过/观测数量和失败原因。真实模型、固定合成数据与模拟 HTTP 故障分别描述。

不要上传整个 `.runtime/`。账号文件、模型配置、日志和导出 JSON 可能含身份、来源内容或答案；截图也应检查个人数据。历史记录只证明其标注版本，不自动证明新一次执行成功。
