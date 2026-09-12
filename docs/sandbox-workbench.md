# Sandbox Workbench

工作台为会话沙箱提供受限命令终端、文件管理和产物预览，默认关闭，通过 `WEKNORA_SANDBOX_WORKBENCH_ENABLED=true` 开启。
它复用会话绑定的具名配置与 Docker、E2B、Cube provider，不在 WeKnora 宿主机执行命令，也不改变 Agent 的 ReAct 流程或 `shell_exec` 契约。
Shell 可以访问本会话沙箱内的文件；宿主机和跨会话隔离依赖后端部署，文件管理的路径限制不能约束 Shell。

部署入口：[Docker 后端](sandbox-docker-backend.md) · [E2B 协议接入](sandbox-protocol.md) · [集群与模板](sandbox-cluster.md)。技能安装见 [Agent Skills](agent-skills.md)，示例见 [Skills 示例](../examples/skills/README.md)。

## 部署条件

工作台要求有效的 Web 用户、当前空间成员身份、会话所有权，以及允许脚本执行的空间策略。首次使用需显式绑定具名沙箱配置；已有会话继续使用原配置，客户端不能指定 provider 的 sandbox ID 或进程 ID。

反向代理部署应明确配置浏览器访问的应用 Origin：

```dotenv
WEKNORA_SANDBOX_WORKBENCH_ENABLED=true
WEKNORA_SANDBOX_WORKBENCH_ORIGINS=https://weknora.example.com
```

多实例必须配置 Redis，共享一次性 ticket 和终端租约。仅单实例可设置 `WEKNORA_SANDBOX_WORKBENCH_SINGLE_INSTANCE=true` 使用内存存储；缺少共享状态时不开放终端。Redis 应启用认证并限制网络访问。

代理须支持 WebSocket Upgrade，连接超时需覆盖控制台寿命。认证 ticket 只出现在首帧，代理不能记录帧内容。每会话最多一个工作台控制台，每用户最多四个，部署内最多 256 个。

| 后端 | 命令终端 | 文件管理 | 部署约束 |
| --- | --- | --- | --- |
| Docker | Engine TTY exec | 支持 | 管理员需另行启用 Docker；容器共享宿主机内核 |
| E2B 兼容后端 | envd PTY | 支持 | 控制面、envd、模板和入站认证均须可用；隔离级别由实现决定 |
| Cube | envd 适配器 | 关闭 | 文件 helper 所需的专用日志脱敏边界尚未接入 |

标准运行环境由 [`docker/Dockerfile.sandbox`](../docker/Dockerfile.sandbox) 提供。自定义镜像需具备 Python 3、Bash、Linux `/proc` 和 `prctl`；文件 helper 另需目录描述符 API 及 `renameat2(RENAME_NOREPLACE)`。E2B 模板还需由部署方按所用控制面要求提供 envd。

## 两种终端

工作台的 `CommandTerminal` 每次提交启动一个独立的受限 Shell 命令，只在运行期间接收 stdin。命令退出、取消或 `Close` 时终止执行并清理后代进程；断线后重新申请 ticket，不重放上一条命令。启动结果不确定时也不自动重试。

会话交互 Shell 使用 `RemoteTerminalSession`：`Close` 只 detach 传输连接，Shell 可继续保留并供后续重连。两套接口都使用现有 `SessionBoundManager` 和 provider，生命周期语义不同，调用方不能混用。

显式初始化会准备工作目录并探测运行时契约。探测结果仅缓存到对应 sandbox，最多 4096 项、TTL 30 分钟；失败只影响该 sandbox。状态查询只读 binding 和本地缓存，不连接后端、不创建沙箱。修复镜像或运行环境后，显式 Bind 会强制重新探测。

默认限额如下；客户端以状态接口和 socket 的 `ready` 帧返回值为准。

| 项目 | 限额 |
| --- | --- |
| 单命令时长 / 控制台寿命 | 120 秒 / 30 分钟 |
| CPU 时间 | 命令及后代累计 60 秒 |
| 内存 | 命令及后代 RSS 合计 512 MiB（采样）；同时保留每进程 512 MiB 地址空间上限 |
| 命令文本 / JSON 帧 | 8 KiB / 16 KiB |
| 未读取输出缓冲 / 控制台累计输出 | 256 KiB / 32 MiB |
| 终端尺寸 | 行、列各不超过 500 |

内存监督与 CPU 监督复用 Linux `/proc` 进程树采样，每轮暂停约 25 ms；监督进程不计入命令预算。`memory_bytes` 同时用于每进程 `RLIMIT_AS` 和命令及后代 RSS 合计上限，状态接口与 `ready` 帧以 `memory_enforcement=per_process_as_and_aggregate_rss_sampled` 声明这两项约束。RSS 达到上限后终止整个命令树，包括由 subreaper 收养的后代。

RSS 共享页会在多个进程中重复计入；采样存在延迟和超调，也可能漏掉采样间隔内的短时峰值。它不是容器或整个沙箱的内存硬配额，CPU 时间也不等于容器 CPU 配额；部署方仍需配置容器或 MicroVM 的内存、CPU、PID 限额。超限会结束命令，沙箱及文件不会因此自动销毁。

监督器确认 RSS 超限时返回专用退出码 200，终端和审计原因均为 `memory_limit`。普通 `SIGKILL` / 137 不推断为内存超限；命令自行 `exit 200` 会归一为 1 / `exited`，避免冒用监督器结果。单进程触及 `RLIMIT_AS` 时由程序处理分配失败，不会仅凭 `MemoryError` 或非零退出码推断 `memory_limit`。清理无法确认时不报告限额终止成功。

服务端每五秒及每次启动命令前重新检查身份和空间策略。stdin、resize、ping 只续租；权限撤销、租约丢失或连接结束后，服务端用独立清理期限终止命令，关闭传输本身不作为进程已退出的证据。

Ticket 绑定签发它的访问令牌记录 ID，不保存 JWT。签发和消费时要求令牌未过期且未撤销；活动控制台复查撤销状态，允许访问令牌正常静默轮换。登出或密码重置撤销令牌后，旧 Ticket 被拒绝，活动命令在复查时终止。控制台累计输出超限的审计原因固定为 `output_limit`；清理失败时保留该原因，但结果仍记为 `unknown`。

## 文件与预览

文件路径是 `/workspace/output` 下的相对 POSIX 路径。拒绝绝对路径、`..`、反斜线和控制字符；不允许符号链接、多硬链接文件、设备、FIFO 或 socket。helper 以 Python 隔离模式运行，通过目录描述符和 `O_NOFOLLOW` 完成路径遍历及实际操作，避免检查后替换路径的竞态。

每目录最多列出 500 项，单文件读取或上传最多 8 MiB。上传经临时 inode 完整写入、同步及校验后原子发布；上传和重命名均不覆盖目标。删除仅支持文件和空目录。

实时文件与消息产物分别展示。消息产物使用消息内索引标识，分页列表保留 `message_id` 和 `artifact_index`，不能用列表位置推算下载地址。Agent 只持久化本轮新增或变化的输出，预先上传且未变化的工作台文件不会自动成为回答产物。

预览先检查内容，再调用文档查看器：

- PPTX、XLSX 最多 2048 个 ZIP 条目，单条目解压后 8 MiB、合计 32 MiB；表格最多 10 万个单元格。拒绝外部关系、不安全路径、DTD 和实体。
- 表格内容为 ZIP 时，不论扩展名是否为 XLSX，都先执行 Office 校验再交给表格解析器。Mermaid 等纯文本降级显式传为 `txt`，不让通用查看器重新启用渲染。
- HTML 和表格 HTML 放在 opaque-origin iframe，CSP 禁止脚本和外部请求，不授予同源、表单、弹窗或顶层导航权限。
- PNG、JPEG、GIF、WebP 在浏览器解码前检查文件头，每边最多 4096 像素、合计 1200 万像素。
- 工作台的 PDF、DOCX、音视频仅下载；普通聊天和知识库预览保持原有行为。产物 `kind` 仅作展示提示，不授予执行权限。

## 接入与审计

HTTP 入口位于 `/api/v1/sessions/{id}/sandbox`，提供工作台状态、显式绑定、文件操作和会话审计。受限命令使用 `POST /api/v1/sessions/{id}/sandbox/command-ticket` 申请 ticket；原 `terminal-ticket` 路由保留给会话交互 Shell，两者不能互换。

工作台 WebSocket 位于 `/api/v1/sandbox-terminal`，首帧使用 `{ "type": "auth", "ticket": "..." }`，ticket 不放进 URL，30 秒过期且只可消费一次。申请与消费时的 Origin 必须一致并符合应用配置，消费时再次检查当前身份和会话权限。

控制消息使用 JSON；stdin 使用 base64 字节并按帧限额拆分，服务端二进制帧携带合并后的 PTY 输出。接入细节见[路由](../internal/router/routes_workbench.go)、[前端 API](../frontend/src/api/sandbox-workbench.ts)和[帧处理](../internal/handler/workbench_socket.go)。

命令与文件变更必须先持久化 accepted 审计，写入失败则拒绝启动。结束记录含租户、操作者、会话、执行 ID、耗时、退出码和结果。命令文本只记 `[REDACTED]` 与字节数，不记录 stdin、环境变量值、ticket 或后端凭据。审计覆盖服务端接受的启动动作，不记录 Shell 内嵌命令和每次键盘输入。

会话审计按记录 ID 倒序分页，每页最多 100 条。将响应的 `next_cursor` 作为下一次请求的 `after_id`，直到空页返回 `next_cursor=0`；工作台的“加载更多”使用同一协议。翻页始终限定当前租户、用户及本人会话，不能通过查询参数更改范围。历史记录受部署的审计保留策略约束。

## 自动化测试

从仓库根目录运行不依赖真实后端的检查：

```bash
go test ./internal/sandbox ./internal/handler ./internal/handler/session ./internal/router
go test ./internal/application/service -run '^TestWorkbench'
python3 -B -I internal/sandbox/workbench_files_test.py
python3 -B -I internal/sandbox/terminal_runner_test.py
```

真实测试仅识别 `WORKBENCH_TEST_*`，不会读取生产 E2B 凭据、Docker context 或通用 `DOCKER_HOST`。配置缺失时只跳过对应后端；配置完整后的连接失败会使测试失败。它们会创建沙箱、执行资源限制测试并在结束时销毁自己的会话，应使用专用测试账号或 daemon。

### Docker 准备

在有权限访问测试 daemon 的 Linux 主机上，从仓库根目录构建标准镜像，无需安装 PPTX 依赖：

```bash
export WORKBENCH_TEST_DOCKER_HOST=unix:///var/run/docker.sock
export WORKBENCH_TEST_DOCKER_IMAGE=weknora-sandbox:workbench-test
docker --host "$WORKBENCH_TEST_DOCKER_HOST" build \
  -f docker/Dockerfile.sandbox --target sandbox \
  -t "$WORKBENCH_TEST_DOCKER_IMAGE" .
```

远程 daemon 使用 `tcp://host:2376`，同时设置 `WORKBENCH_TEST_DOCKER_TLS_CERT_PATH`，指向测试执行机上的 `ca.pem`、`cert.pem`、`key.pem` 目录。构建镜像时用相同地址和证书：

```bash
docker --host "$WORKBENCH_TEST_DOCKER_HOST" --tlsverify \
  --tlscacert "$WORKBENCH_TEST_DOCKER_TLS_CERT_PATH/ca.pem" \
  --tlscert "$WORKBENCH_TEST_DOCKER_TLS_CERT_PATH/cert.pem" \
  --tlskey "$WORKBENCH_TEST_DOCKER_TLS_CERT_PATH/key.pem" build \
  -f docker/Dockerfile.sandbox --target sandbox \
  -t "$WORKBENCH_TEST_DOCKER_IMAGE" .
```

私网 TCP 地址还需 `WORKBENCH_TEST_DOCKER_ALLOW_PRIVATE=true`。测试容器使用 `network=none`，不需要出网；镜像必须存在于所配置的 daemon 上。

### E2B 准备

按[协议接入](sandbox-protocol.md)和[模板部署](sandbox-cluster.md)准备专用后端及可用模板。推荐以标准 `sandbox` 镜像为基础，由所用平台注入或配置 envd。模板应支持 root 执行、可写 `/workspace`、PTY、入站认证及上述运行时契约；测试不创建模板、不假定模板名称，也不依赖模板目录 API。

部署方显式提供以下环境变量，不要将凭据提交到仓库：

| 变量 | 要求 |
| --- | --- |
| `WORKBENCH_TEST_E2B_API_URL` | 必填。测试控制面；E2B Cloud 显式填 `https://api.e2b.app` |
| `WORKBENCH_TEST_E2B_API_KEY` | 必填。专用测试凭据 |
| `WORKBENCH_TEST_E2B_TEMPLATE` | 必填。部署方已准备的模板 ID 或别名 |
| `WORKBENCH_TEST_E2B_SANDBOX_DOMAIN` | 必填。对应的数据面域名；E2B Cloud 为 `e2b.app` |
| `WORKBENCH_TEST_E2B_PROXY_URL` | 自建后端的数据面网关；E2B Cloud 留空 |
| `WORKBENCH_TEST_E2B_ALLOW_PRIVATE` | 私网或 loopback 端点需显式设为 `true` |

keepalive 测试使用 3 秒 TTL，运行 5 秒命令并检查实际续租请求；测试控制面须支持该 TTL。Kubernetes 容器后端的测试结果不代表 MicroVM 隔离能力。

### 执行命令

配置任一后端后，可分别运行或同时启用两组 build tag：

```bash
go test -tags=sandbox_terminal_integration ./internal/sandbox \
  -run '^TestTerminalReal' -count=1 -v -timeout=15m
go test -tags=workbench_integration ./internal/sandbox \
  -run '^TestWorkbenchFilesIntegration$' -count=1 -v -timeout=15m
go test -tags='sandbox_terminal_integration workbench_integration' ./internal/sandbox \
  -run '^TestWorkbenchIntegrationConfig$' -count=1 -v
```

终端测试覆盖提前输出、二进制 stdin、resize、interrupt、CPU/地址空间/命令树 RSS/输出限额及后代清理。`AggregateDescendantMemory` 在每进程地址空间低于上限时触发 RSS 总量限制，并检查包括 double-fork 后代在内的进程清理；另验证 137 和命令自报 200 不会误记为内存超限。文件测试覆盖 8 MiB 往返、特殊节点、路径注入和不覆盖写入。套件未配置后端时的 skip 不能计作真实运行通过。测试进程被强制结束时，应在测试控制面检查遗留沙箱。

前端测试、类型检查和构建见 `frontend/package.json`。浏览器安全检查与 PPTX fixture 的完整准备命令见 [Skills 示例](../examples/skills/README.md#本地测试与预览)。[Frontend CI](../.github/workflows/frontend.yml) 会生成 fixture 并运行 builder、文件 helper 和浏览器安全测试，不需要真实沙箱凭据。
