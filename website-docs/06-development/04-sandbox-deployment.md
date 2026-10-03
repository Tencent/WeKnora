# 沙箱部署与排障

技能使用流程见[技能目录与沙箱](../03-features/22-skills-sandbox.md)，字段定义见[沙箱与技能 API](../04-api/02-api-sandbox-skills.md)。本文补充部署、模板、桌面和后端接入的运维要求。

## 后端与模板

空间命名配置可选择 Docker、CubeSandbox、E2B，本文说明这三类后端的部署。Lite 桌面客户端另有依赖操作系统隔离能力的 `host` 后端，不属于服务端命名配置，构建要求见下文 [Lite 本机沙箱](#lite-本机沙箱)；旧 `local` 宿主机进程后端已移除。Docker 直接使用 Engine API；E2B 使用控制面 REST 与 envd 数据面；Cube 保留专用适配器处理模板和网络策略。

标准镜像由 `docker/Dockerfile.sandbox` 定义，包含 Python 3.12、Node.js 20、Bash、jq 与 `/workspace`。命令和文件操作默认使用沙箱内 root；保留 UID 1000 的 `user` 供显式按账号执行。跨会话隔离由容器或远端沙箱提供，不能把工作目录约定解释为 root 的文件权限限制。

| 构建 target | 镜像标签后缀 | 用途 |
| --- | --- | --- |
| `sandbox` | 无 | Docker 会话镜像；E2B CLI 模板基础镜像 |
| `cube` | `-cube` | Cube CLI 模板，含 envd |
| `desktop` | `-desktop` | E2B 图形桌面模板基础镜像 |
| `desktop-cube` | `-desktop-cube` | Cube 图形桌面模板，含 envd |

镜像名为 `wechatopenai/weknora-sandbox:<版本><后缀>`。模板 ID 属于具体集群或账号，不应把其他部署的 ID 直接复制过来。生产模板应对应已验证的应用版本；构建 target 和发布架构以仓库 Dockerfile、发布工作流为准。

## Docker 部署

Docker 后端默认关闭。系统管理员在「系统设置 → 网络安全」启用；尚未落库时可由 `WEKNORA_SANDBOX_DOCKER_ENABLED=true` 回退。关闭后仍可查看和删除已有配置，但不会新建会话容器。

一个配置连接一个 daemon，一个会话使用一个长驻容器。没有跨主机调度；不同 app 副本若连接各自独立的本机 daemon，不能假定会共享容器和技能快照。

| 配置 | 默认值或要求 |
| --- | --- |
| daemon 地址 | 留空时检测 `DOCKER_HOST` 或当前 Docker context；远端使用 `tcp://host:2376` |
| TLS 证书目录 | 远端 TCP 必填，app 可读的目录中包含 `ca.pem`、`cert.pem`、`key.pem` |
| CPU / 内存 / PID | 默认 2 核 / 2 GiB / 512 进程 |
| 空闲 TTL | 默认 1800 秒 |
| 网络模式 | 仅 `bridge` 或 `none`；不接受 `host`、`container:` 或自定义网络名 |
| OCI runtime | 可选择 daemon 已安装的 runtime，如 `runsc` |

app 运行在容器里时，要挂载实际 Docker socket，或改连远端 daemon。socket 授予控制宿主机 Docker 的能力，只应交给可信 app。入口脚本在降权前根据 socket GID 配置 appuser 的组；不要通过 `chmod 666` 放开 socket。若 socket 为 `root:root` 且仅所有者可写，应先在宿主机配置合适的非 root 组权限。

优先使用标准镜像。自定义镜像需满足适配器的 root、Bash、GNU `find -printf`、coreutils `timeout` 和可写工作区要求；会话文件检查点与回退还依赖 Git。HTTP 请求取消本身不会终止容器进程，适配器通过容器内 `timeout` 执行命令超时控制。

空闲清扫由 Create/Connect 触发并在后台运行，按容器创建时记录的 TTL 判断；不是 daemon 自带的定时器。当前没有硬寿命上限，能执行命令的脚本可以持续更新活跃标记，部署方需单独监控长期存活容器。

### 技能快照与磁盘

技能安装通过 `docker commit` 生成 `weknora-skill/` 本地镜像，后续会话从安装快照启动。它保存文件系统，不保存内存状态；当前不支持租户共享卷或 Docker 图形桌面。

增量快照继承旧镜像层，删除旧 tag 不一定释放磁盘；卸载技能会新增删除标记层，原文件仍可能留在父层。当前流程不自动压平镜像或跨 daemon 分发。监控层数和磁盘，清理前确认会话与快照引用；需要新底模时新建配置并重新安装技能。

## CubeSandbox / E2B 接入

1. 先准备可用的控制面、数据面网关和沙箱域名。在空间设置中填写 API、Proxy、domain 与凭据；私网/回环端点需要显式允许私网地址，仍不允许云元数据地址。
2. 点击「连接并继续」，验证控制面后加载模板目录。连接通过只证明控制面可用。
3. 缺少标准模板时显式创建；需要桌面时另行创建桌面模板。等待状态为 `READY` 再选择。Cube 应使用带 envd 的 `-cube` 变体；普通 Docker 镜像不提供 `:49983/health`。
4. 执行「完整验证」，实际创建沙箱、执行探针并销毁，确认数据面和脚本环境也可用。
5. 保存后在智能体选择该配置，并验证附件读取、命令状态保持和产物下载。

`proxy_url` 用于自托管数据面网关：连接网关时保留沙箱 Host 路由，适合没有泛域名 DNS 的环境。控制面可访问而执行失败时，重点核对 Proxy、sandbox domain、入站凭据和 app 到网关的可达性。

Cube guest DNS 属于模板配置。更改 DNS/镜像后需重建模板才会进入新环境。已经安装技能的配置不能更换或重建底模，也不能在 CLI/桌面间切换，应新建配置并安装技能，避免让快照与底模不一致。

多副本必须配置共享 Redis，保存 session→sandbox 绑定和桌面状态。仅单实例开发可使用内存存储。已有沙箱不会自动应用后端配置变更；技能更新的 `next_turn` / `new_session` 策略见[技能目录与沙箱](../03-features/22-skills-sandbox.md)。

| 现象 | 排查方向 |
| --- | --- |
| 连接验证失败 | API 是否误填 Dashboard 地址；凭据、TLS、私网开关是否正确 |
| 连接成功但执行失败 | Proxy、sandbox domain、网关路由与入站 token |
| 模板构建失败 | 查看集群返回的构建错误；镜像是否能拉取、架构是否匹配、Cube 镜像是否带 envd |
| 沙箱无法出网 | 模板网络策略、guest DNS、集群出站代理；允许私网控制面不等于允许脚本出网 |
| 安装依赖失败 | 默认拒绝出站时是否放行软件源；技能安装与会话使用同一网络策略 |
| 重连后状态丢失 | Redis 是否共享、TTL 是否到期、技能更新是否触发重建 |

## 交互终端与图形桌面

对话侧栏的终端和桌面都通过 WebSocket 连接会话沙箱，仅 Cube/E2B 支持，Docker 后端不提供。浏览器不能在 WebSocket 握手里携带认证头，因此都先用已登录的 POST 换取两分钟有效的短期票据，再把票据放在握手 query 中：

| 能力 | 取票据 | WebSocket |
| --- | --- | --- |
| 终端 | `POST /api/v1/sessions/:session_id/sandbox/terminal-ticket` | `GET /api/v1/sessions/:id/sandbox/terminal?ticket=...` |
| 桌面 | `POST /api/v1/sessions/:session_id/sandbox/desktop-ticket` | `GET /api/v1/sessions/:id/sandbox/desktop?ticket=...` |

入口代理必须为这两个路径转发 WebSocket Upgrade、放宽读超时，并避免在访问日志中记录 ticket query。标准 frontend Nginx 已为 `^/api/v1/sessions/[^/]+/sandbox/(terminal|desktop)$` 配置不含 query 的日志格式；自定义 Ingress 需自行处理。终端连上后，服务端大约每分钟复核一次登录状态、空间成员和会话归属，退出登录或被移出空间会断开终端。接口参数见[终端 API](../04-api/02-api-sandbox-skills.md#会话交互终端)与[桌面 API](../04-api/02-api-chat.md#sandbox-desktop)。

终端和桌面在无操作时按配置的 `terminal_idle_disconnect_sec` 断开（默认 900 秒，最短 60 秒，最长 24 小时），之后沙箱按提供商 TTL 暂停。终端以键盘输入和 PTY 输出计活动，桌面以键鼠计活动。

### 图形桌面

仅 Cube/E2B 桌面模板支持对话侧栏桌面。首次打开时由后端启动桌面进程；配置需选择桌面模板并设置 `desktop_enabled`。技能已安装后不能原地切换底模。

```text
浏览器 noVNC → WeKnora 票据中继 → 提供商网关 → websockify :6080 → 本机 x11vnc :5900
```

浏览器不持有沙箱 API Key、入站 token 或 websockify 密码。桌面票据仅可消费一次，完整定义见[会话 API](../04-api/02-api-chat.md#sandbox-desktop)。Cube 桌面模板的 `exposedPorts` 只暴露 envd 的 49983，**不要把 6080 加入宿主机 NAT**，桌面必须经过网关与 WeKnora 中继。

每个会话同时只允许一条桌面中继，多副本槽位由 Redis 协调。沙箱重建后以 `SANDBOX_REBUILT` 提醒断开，不能把新桌面当成保留了原临时文件的旧实例。空闲判断使用 RFB 键鼠活动，截图请求不算用户操作。

## Lite 本机沙箱

`host` 后端只编译进带 `desktop` 构建标签的 Lite 桌面程序（`cmd/desktop/wails.json` 的 `build:tags`），服务端和单二进制 Lite 不包含。它在未选沙箱配置的会话中运行命令，用法见[技能目录与沙箱](../03-features/22-skills-sandbox.md#lite-host)。

| 平台 | 状态 |
| --- | --- |
| macOS | 使用系统 `sandbox-exec`（Seatbelt）执行每条命令；启动时检测不可用则不启用 |
| Windows | 尚未实现，报告不可用，不会退化为无隔离执行 |
| Linux | 不支持 |

每条命令都是新的本机进程，没有会话级实例，因此不写入会话的沙箱绑定，也不做工作区检查点。用户通过系统目录选择框批准的项目目录保存在 `desktop-prefs.json` 的 `project_dirs`；会话只能绑定列表中的目录本身，不能绑定其子目录或手工输入的路径。`approval_mode` 当前只支持 `auto`（在工作区内自由读写、禁止联网），写入其他值会被拒绝。偏好文件位置见[桌面客户端](../05-clients/05-desktop.md#_5-偏好设置存储-cmd-desktop-prefs-go)。

## 开发验证

单元测试不需要真实 daemon：

```bash
go test ./internal/sandbox -run 'TestDocker' -count=1
```

真实 Docker 验证需要先构建标准镜像，会创建测试容器：

```bash
docker build -f docker/Dockerfile.sandbox --target sandbox -t wechatopenai/weknora-sandbox:dev .
DOCKER_INTEGRATION_IMAGE=wechatopenai/weknora-sandbox:dev \
go test -tags=docker_integration ./internal/sandbox \
  -run '^TestDocker.*Integration' -count=1 -v -timeout=15m
```

远端接入的一致性测试位于 `internal/sandbox/cube_integration_test.go` 与 `e2b_compatible_integration_test.go`，运行前按文件开头配置测试集群凭据。验证范围应包含会话状态保持、Shell 复用、附件暂存、产物收集和超时，而不能只测 Health。测试结束后确认测试实例已清理。
