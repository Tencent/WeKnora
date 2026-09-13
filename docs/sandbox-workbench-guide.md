# 可视化沙箱工作台：用户使用与测试手册

本手册用于首次配置、实际操作和验收 PR #3146 的工作台。已有可用环境的用户从「快速上手」开始；管理员先完成「首次部署与配置」。每项练习都给出操作位置、预期结果和失败时的检查方向。

适用代码为 `rhino-2026-final-2-v3`（`28d676539ca25fcff06aa18aa315eff14c092e43`），以及其后仅增加元数据或文档的提交。本手册是 Tag 创建后的补充文档，旧 Tag 不移动。根目录 `submission.yaml` 记录的是该代码版本，分支 HEAD 可包含之后的文档提交。

## 阅读入口

- [快速上手](#快速上手)：已有环境，先完成终端和文件练习。
- [首次部署与配置](#首次部署与配置)：开发环境、Docker、E2B、权限和模型。
- [终端操作](#终端操作)：命令、交互输入、中断、缩放和重连。
- [文件操作与预览](#文件操作与预览)：上传、下载、移动、删除、HTML/表格预览。
- [Agent 生成演示文稿](#agent-生成演示文稿)：安装 Skill、配置智能体、生成及核对 PPTX。
- [审计与资源限制](#审计与资源限制)、[手工验收](#手工验收)、[自动化测试](#自动化测试)。
- [故障排查](#故障排查)、[清理与结果记录](#清理与结果记录)。

协议和安全设计见 [Sandbox Workbench](sandbox-workbench.md)。本文中的「工作台命令框」指网页内的输入框；「开发机终端」指运行 Git、Docker、Go、npm 的命令行。两者不要混用。

## 快速上手

前提：管理员已启用工作台，当前空间有可用 Docker 或 E2B 配置，允许脚本执行；你使用本人 Web 账号打开本人会话。仅验证终端、文件时不需要调用模型；Agent 练习另需模型及已安装技能。

1. 登录 WeKnora 并进入正确的工作空间。打开一个专用测试会话，避免使用含重要草稿的会话。
2. 在聊天页右上角找到提示为「沙箱终端」的按钮，打开「沙箱可视化」侧栏，再点「打开工作台」。工作台包含「终端、文件、产物、审计」四个标签；原侧栏的终端是另一种可重连 Shell，不作为下面练习的入口。
3. 若显示「尚未绑定沙箱」，选择配置并点「绑定到当前会话」。配置应与本次使用的智能体的「运行沙箱」一致。若无可选项，请管理员配置，勿填写供应商 sandbox ID。
4. 成功后顶部显示「已绑定」及 `docker` 或 `e2b`，终端状态变为「空闲」。若已绑定但显示能力不可用，修复环境后点「重新初始化沙箱」。
5. 在下方「命令」框输入 `printf 'WORKBENCH_OK\n'`，点运行图标，或按 Ctrl+Enter（macOS 为 Cmd+Enter）。上方黑色终端出现 `WORKBENCH_OK`，随后显示退出码 0。
6. 在「文件」中点「新建目录」，目标路径填 `manual-demo`。再回到命令框运行：

```bash
printf 'name,count\nalpha,2\nbeta,3\n' > /workspace/output/manual-demo/table.csv
```

7. 回到「文件」，刷新后进入 `manual-demo`，点 `table.csv`，应看到两列、两行数据。文件行右侧「文件操作」菜单可下载、重命名或删除。
8. 切到「审计」，刷新后应看到命令的 accepted 与完成记录，命令文本显示 `[REDACTED]`。结束时先下载需要保留的文件，再关闭工作台。

`manual-demo/table.csv` 是合成练习数据。重复练习请新建会话，或换目录和文件名。工作台文件 API 不允许覆盖；上面的 Shell 重定向本身会覆盖同名文件，因此只用于这份专用测试数据。

## 首次部署与配置

### 选择环境

最容易复现的方式是 Linux 上运行项目后端和前端，使用同机专用 Docker daemon。不要直接运行官方 `main`/`latest` 应用镜像来验证本 PR，它不保证包含本分支功能。已有部署必须使用本分支源码构建的后端与前端。

| 角色 | 需要准备的内容 |
| --- | --- |
| 部署管理员 | 本分支程序、数据库、Redis、工作台开关、可信 Origin、WebSocket 代理 |
| 系统管理员 | 允许使用 Docker 后端（仅选择 Docker 时） |
| 空间所有者或管理员 | 沙箱配置、脚本策略、模型、Skill 安装、测试智能体 |
| 测试用户 | Web 登录、空间成员身份、本人会话；双租户验收另需第二个独立空间及账号 |

浏览器优先使用桌面 Chrome/Chromium。手机或窄窗口可以操作工作台，但组合键和开发者工具检查更适合桌面。

### 获取并运行代码

以下命令在开发机终端执行，使用新的克隆目录，避免覆盖已有工作区：

```bash
git clone --branch feat/sandbox-workbench \
  https://github.com/mingri31164/WeKnora.git WeKnora-workbench
cd WeKnora-workbench
git checkout --detach rhino-2026-final-2-v3
git rev-parse HEAD
```

最后一行应为 `28d676539ca25fcff06aa18aa315eff14c092e43`。此 Tag 本身不含稍后提交的 `submission.yaml` 或本手册，这是先固定代码、再补元数据和文档的正常顺序。

开发工具要求：Docker Engine/Compose、Go 1.26.0（见 `go.mod`）、Node.js 24、npm；Python 3.11 用于构造器测试。系统初始化及基础组件说明见[项目 README](../README.md)和[开发指南](../website-docs/06-development/01-dev-guide.md)。

1. 在新克隆的根目录复制 `.env.example` 为 `.env`，按其中注释配置数据库、Redis 和存储。不要覆盖现有部署的密钥；新环境必须更换示例密码、JWT 和 AES 密钥。
2. `SYSTEM_AES_KEY` 要求 32 字节；新环境可用 `openssl rand -hex 16` 生成 32 个 ASCII 字符，JWT 可用 `openssl rand -hex 32`。把结果填入本地 `.env`，不要提交、截图或发送到对话中。启动后不要随意更换 AES 密钥，否则旧凭据无法解密。
3. 在 `.env` 中新增或更新：

```dotenv
WEKNORA_SANDBOX_WORKBENCH_ENABLED=true
WEKNORA_SANDBOX_WORKBENCH_ORIGINS=http://127.0.0.1:5173
WEKNORA_SANDBOX_DOCKER_ENABLED=true
```

4. 在仓库根目录启动基础组件：

```bash
make dev-start DEV_ARGS=--no-langfuse
```

5. 另开两个开发机终端，分别回到仓库根目录执行 `make dev-app` 和 `make dev-frontend`。后端默认端口为 8080，前端为 5173；以启动日志为准。
6. 访问 `http://127.0.0.1:5173`，完成注册/登录、空间初始化。已有部署关闭注册时，由管理员邀请测试账号。

此路径的基础组件由 `docker-compose.dev.yml` 提供，不需要为工作台额外安装图数据库。没有 anydoc 静态库时，文档解析引擎会不可用；终端和合成文件练习不依赖解析引擎。首次下载依赖或构建沙箱镜像需要网络。

检查后端进程存活可运行 `curl --fail http://127.0.0.1:8080/health`；返回正常不等于模型、沙箱或工作台已配置好。

### 地址、Redis 与代理

- `127.0.0.1` 永远指浏览器所在机器。远程开发机应使用管理员提供的 IP/域名，或通过端口转发访问；不能在个人电脑直接打开开发机的 loopback 地址。
- Origin 必须与浏览器地址的「协议 + 主机 + 端口」一致，不含路径。`localhost`、`127.0.0.1` 和开发机 IP 是不同 Origin。改端口或域名后，更新后端配置并重启。
- Vite 默认代理 `/api` 到 8080，支持 WebSocket。后端不在默认位置时，设置 `VITE_DEV_PROXY_TARGET`；前端端口用 `VITE_DEV_PORT`。如果端口占用导致 Vite 换端口，Origin 也需同步。
- 本文使用 Redis。多实例必须共享 Redis；只有明确单实例时才能用 `WEKNORA_SANDBOX_WORKBENCH_SINGLE_INSTANCE=true` 的内存票据存储。单实例内存模式还存在重启后沙箱绑定丢失的限制，不能拿来验证多实例行为。
- Nginx/网关需转发 `/api/v1/sandbox-terminal` 的 WebSocket Upgrade。HTTPS 页面必须使用 WSS。连接超时应覆盖 30 分钟控制台寿命，且代理不得记录 Ticket、帧正文或 Authorization。
- Docker 许可若已写入系统设置，以落库设置为准，不能只改环境变量期待覆盖。工作台开关与 Docker 许可是两个不同开关。

### 配置 Docker

仅在有权限的专用开发 daemon 上操作。本机 `docker.sock` 的权限等同宿主机 root，不应对不受信任的用户开放，更不能挂入生成代码所用的沙箱容器。

开发机终端，在仓库根目录构建：

```bash
docker build -f docker/Dockerfile.sandbox --target sandbox \
  -t weknora-sandbox:workbench-manual .
```

然后由管理员在网页执行：

1. 「设置 → 系统设置 → 网络安全」确认「启用 Docker 沙箱」已开启。
2. 「设置 → 沙箱配置」，打开「允许在沙箱中执行技能脚本」，点 Docker 下的「添加沙箱」。
3. 名称填 `workbench-docker`；镜像填 `weknora-sandbox:workbench-manual`。Linux 同机示例的 daemon 地址为 `unix:///var/run/docker.sock`。镜像必须构建在这份配置实际连接的 daemon 上。
4. 点「连接并继续」。Docker 的镜像在连接步骤填写，没有 E2B/Cube 的独立模板选择步骤。
5. 在「运行配置」保留默认资源值，或明确填写 CPU 2 核、内存 2048 MB、进程数 512、空闲回收 1800 秒。首次 Skill 安装需要下载依赖，演示环境可使用 `bridge`；网络隔离要求高时请先准备离线依赖，再使用 `none`。保存配置。
6. 运行「完整验证」。它会创建并销毁临时沙箱，应看到执行检查成功。若出网被策略禁止，出网检查受限属于预期，不能把端点连通当成全部检查通过。

后端运行在 app 容器内时，daemon 地址也从 app 容器内部解释。使用现有受控 socket 挂载或带 mTLS 的远程 daemon；不要用 `chmod 666` 放宽 socket 权限，不要暴露无 TLS 的 TCP daemon。具体部署见 [Docker 后端](sandbox-docker-backend.md#配置)。

### 配置 E2B 兼容后端

E2B 适合已有云账号或自建控制面的环境；从零搭集群请按[协议接入](sandbox-protocol.md)和[模板部署](sandbox-cluster.md)准备，不能只填写一个 URL 就获得可运行沙箱。

1. 管理员在「设置 → 沙箱配置」添加 E2B，填写专用测试 API Key；自建环境填写控制面 API、数据面域名和需要的 Proxy，私网端点还需打开允许私网访问。
2. 点「连接并继续」，在「模板」选择已经就绪的标准模板；若正在构建，等待完成再刷新。模板须包含 Python 3、Bash、Linux `/proc`、`prctl`、envd PTY、可写 `/workspace` 和入站认证。
3. 完成运行配置并保存，再执行「完整验证」。密钥只能由管理员填写，不要发给测试用户。
4. 建立新的测试会话绑定这份配置；重复 Docker 上的相同练习，再分别记录结果。

当前本地兼容控制面的模板目录 API 返回 404，演示使用预配置。遇到同样情况，设置向导无法凭空完成新建配置：先补齐控制面模板目录能力，或使用管理员已验证的现有配置；无配置时先走 Docker。本文没有提供改数据库绕过配置流程的步骤。

Cube 文件能力在本版本关闭；原生 Cube/E2B Cloud/MicroVM 未实测。E2B 兼容容器环境通过测试不等于这些云环境已通过。

### 准备智能体与权限

在「智能体」创建专用测试智能体，选择支持 Tool Calling 的对话模型和「智能推理」模式，先发送简单文本确认模型可调用。进入编辑页的「技能」，为「运行沙箱」选择上述配置；Skill 选择在完成安装后设置。

更改智能体的运行沙箱不会把旧会话迁移到新配置。对比 Docker/E2B 或更新 Skill 快照时，使用新会话，并重新确认工作台顶部的 provider。工作空间成员身份、会话归属、脚本策略分别由服务端检查；使用 API Key 调用不能代替工作台要求的 Web 用户身份。

## 终端操作

所有本节命令均粘贴到**工作台下方「命令」框**，不在开发机执行。正常绑定后状态为「空闲」。普通 Enter 在命令框中换行，Ctrl/Cmd+Enter 或运行图标提交整段命令。

| 操作 | 命令或步骤 | 预期结果 |
| --- | --- | --- |
| 实时输出 | `printf 'EARLY\n'; sleep 2; printf 'LATE\n'` | 先出现 EARLY，约两秒后出现 LATE；退出码 0 |
| 交互输入 | `printf 'READY\n'; read -r value; printf 'INPUT:%s\n' "$value"` | 出现 READY 后，点上方黑色输出区输入 `hello` 并回车；出现 INPUT:hello |
| 中断 | 执行 `sleep 90`，在运行中点「中断」 | 命令结束，显示退出状态，终端恢复空闲；不要要求退出码必须为 0 |
| 排版与缩放 | 执行 `stty size; read -r value; stty size`；改变浏览器窗口宽度，再在输出区输入 `ok` 回车 | 两次尺寸随可用空间变化，文本不越界；工作台本身没有独立拖拽缩放手柄 |
| 多行命令 | 在命令框输入两行 `printf` 后提交 | 两行属于一次执行、对应一组命令审计；输入法组合期间不误提交 |
| 清空输出 | 点「清空输出」 | 清除本地显示，不删除文件或审计，不重置服务端累计输出预算 |
| 断开 | 运行 `sleep 90` 后点「断开」 | 关闭连接并触发命令清理；重新点「连接」申请新 Ticket，不自动重放旧命令 |

每次提交都是独立的 Shell。第一次运行 `cd /workspace/output` 后，下一次的 `pwd` 不会继承该目录；需要时在同一条命令里写 `cd /workspace/output && ...`。`export` 同样不跨命令保存。

切换工作台内部标签不会主动断开当前命令。关闭整个工作台、切换会话/空间、退出登录或页面离开会关闭连接。运行中若需要确认结果，先看「审计」及文件，不能将“断线”理解为“这条命令从未执行”，也不要自动重复有副作用的操作。

## 文件操作与预览

### 路径、上传与下载

工作台根目录固定为 `/workspace/output`。对话框中填写的是**相对于根目录的完整路径**：在 `manual-demo` 目录里重命名文件时应填 `manual-demo/renamed.csv`；只填 `renamed.csv` 会移动到根目录。

1. 打开「文件 → 上传文件」，选择自己电脑上的一个小文件，确认目标路径为 `manual-demo/input.csv` 后提交。也可以拖入一个文件，不支持文件夹或多文件拖拽。
2. 点文件名进入预览，左上角「返回」回到文件列表。下载使用该文件右侧「文件操作 → 下载」，不是让浏览器访问沙箱物理路径。
3. 选择「重命名 / 移动」，填尚未存在的目标路径。移动到子目录前先创建父目录。
4. 对专用测试文件执行删除，取消确认框时文件保留；确认后文件消失。目录必须先删空，不支持递归删除。
5. 重复上传同名文件或重命名到已有目标，应提示「目标已存在，不允许覆盖」。现有文件不应改变。

单文件上限为 8 MiB，每目录最多 500 项。不要用普通办公大文件第一次试跑。绝对路径、`..`、反斜线、空路径段及链接/特殊文件均会被拒绝；只有前端拦截不能证明服务端安全，服务端检查见手工验收。

### 无需模型的 HTML 和表格练习

先按快速上手创建 `manual-demo`。在工作台命令框运行以下完整片段，生成两行合成工单数据及静态网页，使用独占写入避免覆盖：

```bash
python3 - <<'PY'
from pathlib import Path
root = Path("/workspace/output/manual-demo")
with (root / "tickets.csv").open("x", encoding="utf-8", newline="") as f:
    f.write("team,total,resolved\nPlatform,450,417\nSearch,300,280\n")
with (root / "report.html").open("x", encoding="utf-8") as f:
    f.write('<!doctype html><html><head><meta charset="utf-8"><title>Test report</title></head><body>'
            '<h1>Synthetic ticket report</h1><table><tr><th>Team</th><th>Total</th><th>Resolved</th></tr>'
            '<tr><td>Platform</td><td>450</td><td>417</td></tr>'
            '<tr><td>Search</td><td>300</td><td>280</td></tr>'
            '<tr><td>Total</td><td>750</td><td>697</td></tr></table></body></html>')
print("CREATED tickets.csv report.html")
PY
```

回到「文件」刷新，分别打开 `tickets.csv` 和 `report.html`。应在界面直接看到表格和网页，无须先点击下载。HTML 禁止脚本和外网资源，静态内容可显示，JS 应用和依赖 CDN 的页面不能按普通网站运行。

这些文件由人工命令创建，只应要求「文件」中可见；「产物」为空不算失败。消息产物由 Agent 在一轮对话后收集本轮新增或变化的文件并持久化，两者不是同一个列表。

### 三类预览的边界

| 类型 | 操作与预期 | 不能据此认定的能力 |
| --- | --- | --- |
| PPTX | 在文件或产物入口点文件名，使用预览器页导航/滚动查看各页，再下载原文件 | 不支持在线编辑；排版受字体影响，不保证像素级还原 PowerPoint |
| HTML | 显示静态页面，工作台使用受限 iframe | 不允许脚本、外网、同源权限、弹窗或顶层导航 |
| CSV/XLSX/XLS | 显示表格；多工作表文件检查所需工作表 | 不等同 Excel 编辑器，不承诺外部公式和链接刷新 |
| PDF/DOCX/音视频 | 显示下载降级或使用文件菜单下载 | 本工作台未提供这几类在线预览 |

Office 文件最多 2048 个 ZIP 条目、单条目解压后 8 MiB、总展开 32 MiB、表格 10 万单元格；外部关系或异常压缩包会拒绝预览。改扩展名不会绕过校验。图片每边最多 4096 像素、总计 1200 万像素。

## Agent 生成演示文稿

### 安装 presentation-builder

在开发机仓库根目录执行：

```bash
bundle_dir="$(mktemp -d)"
bundle_zip="$bundle_dir/presentation-builder.zip"
(
  cd examples/skills
  zip -r "$bundle_zip" presentation-builder -x '*/__pycache__/*' '*.pyc'
)
printf '%s\n' "$bundle_zip"
```

这个 ZIP 是**运行时技能安装包**，不是赛事材料附件。只把 `SKILL.md` 单文件上传不能安装完整技能；脚本、requirements 和 assets 均需保留。远程开发时，把生成文件取到浏览器所在电脑再选择上传。

1. 在「设置 → 技能管理 → 添加技能」上传此 ZIP。也可粘贴本仓库公开技能目录的固定版本链接：`https://github.com/mingri31164/WeKnora/tree/rhino-2026-final-2-v3/examples/skills/presentation-builder`。
2. 在「安装」步骤勾选 `workbench-docker` 或你的 E2B 配置。若只完成登记，回列表点「安装到沙箱」。
3. 查看安装进度，等待完成并启用。Python 依赖通过安装流程写入技能环境；首次需要访问包源，网络失败时先解决下载，不要在工作台反复 `pip install` 代替安装。
4. 编辑测试智能体，在「技能」中选相同的「运行沙箱」，选择「指定」并勾选已就绪的 `presentation-builder`（或选择使用全部就绪技能），保存。
5. 新建该智能体的会话。技能更新可能在下一轮重建沙箱并清空工作区草稿，取决于配置的快照生效策略；不要用已有重要会话验证安装。

### 首次生成：使用内置样例

在**聊天输入框**发送下面的完整请求，不要发到工作台命令框：

```text
请用当前沙箱已安装的 presentation-builder 生成 runtime-demo.pptx。
先通过 read_file 读取 skill://presentation-builder/SKILL.md，
再读取 skill://presentation-builder/assets/sample.json，按样例原样生成，不检索网络，不安装依赖。
调用 shell_exec 时指定 skill_name="presentation-builder"，使用技能返回的脚本路径与 Python 环境。
如果同名文件已经存在，请改用 runtime-demo-2.pptx，不要覆盖。
成功后给出文件下载入口、页数和文件大小；失败时保留真实错误，不宣称生成成功。
```

观察调用过程：读取技能和样例，调用 `shell_exec`，构造器成功返回 `path`、`slide_count`、`size_bytes`。内置样例包含四条正文页定义，所以结果应为**封面 1 页 + 正文 4 页，共 5 页**。此前 PR 截图的四页稿是另一份历史输入，不能用作本样例页数标准。

打开「工作台 → 产物」，必要时点刷新，选生成的 PPTX，逐页检查后下载；「文件」根目录也应有对应文件。若只看到 Agent 的文字“已生成”，没有工具成功结果、文件或下载入口，不能判定通过。

`$WEKNORA_SKILL_DIR` 与技能 Python 环境由 `shell_exec(skill_name=...)` 注入。工作台命令框是普通 Shell，不能假设它自动拥有这组变量和依赖。`skill://` 是 Agent 读取资源的地址，不是 Shell 文件路径。

### 完整任务：同一数据生成三类产物

在上述智能体的新测试会话中绑定同一配置，按「文件操作与预览」的片段创建 `manual-demo/tickets.csv`。保留原始输入，在聊天框发送：

```text
请读取 /workspace/output/manual-demo/tickets.csv，这是合成测试数据。
保持输入不变，按团队汇总并增加 Total 行：总数 750，已解决 697；百分比保留一位小数。
读取已安装的 presentation-builder 说明，生成三页 PPTX：封面、数据表、结论。
同时生成不依赖外网和脚本的静态 HTML 表格，以及包含相同汇总结果的 CSV。
分别保存到 /workspace/output/manual-review.pptx、/workspace/output/manual-report.html、
/workspace/output/manual-summary.csv，并在三份输出内标注 Synthetic data。
PPTX 使用 presentation-builder；CSV/HTML 使用 Python 标准库即可。
不要安装新依赖，不要修改输入，不覆盖已有同名文件。
返回三份文件的下载入口；任何生成失败都如实说明。
```

检查 Platform 为 `450/417/92.7%`，Search 为 `300/280/93.3%`，Total 为 `750/697/92.9%`；PPTX 3 页。三份输出应在「产物」直接查看并可下载，未变化的输入不应被误收成本轮产物。重命名或删除沙箱文件不应被理解成修改已持久化的消息附件。

这是模型任务，不保证每次生成完全一致。数值、页数、标注遗漏或工具失败均需记录并要求 Agent 修正；不能手工修完文件后把它记为 Agent 一次成功。当前已有证据的模型运行版本与提示词偏差见 PR，本文增加操作步骤不代表 v3 已重新完成完整 Agent/浏览器验收。

## 审计与资源限制

「审计」按记录 ID 倒序显示，刷新获取新记录，「加载更多」访问较早记录。每次服务端接受的命令启动先写 accepted，结束后再写完成记录；两条记录属于一次执行，不是执行了两次。

分页验收可使用已有超过 100 条记录的测试会话，确认「加载更多」后能看到最早执行且没有重复项。新环境无需手工重复执行几十次命令：后面的 Handler 回归包含 `TestWorkbenchAuditCursorKeepsOlderCommandsQueryable`，用真实 SQLite 的 120 条记录核对分页和权限过滤；它不代替页面按钮的实际操作。

- 命令正文固定为 `[REDACTED]`，stdin 和密钥不落审计；不支持按命令明文搜索。
- 页面展示时间、耗时、退出码和结果。细节中的 `execution_id`、`reason` 可通过审计 API 核对。
- 审计按一次提交计数，不拆分 Shell 中的 `;`、子命令或每次键盘输入，也不是全部 Agent 工具调用的追踪日志。
- `unknown` 表示执行/清理结果未能确认，需要排查；不能作为“已安全结束”的通过结果。

展开工作台的「资源限制」检查当前返回值。默认命令 120 秒、控制台 1800 秒、命令树 CPU 累计 60 秒、RSS 合计 512 MiB（采样）及每进程地址空间 512 MiB、单文件 8 MiB、控制台累计输出 32 MiB。每个会话最多 1 个控制台，每用户最多 4 个。

界面的「会话超时」指本工作台控制台的寿命，不是整个沙箱硬寿命。命令级 CPU/RSS 预算每条命令重置，超限不会销毁沙箱或删除文件。Docker 配置的 CPU/内存容器限额属于另一层。若验收要求整沙箱生命周期累计资源超限后销毁，当前实现尚未满足。

## 手工验收

使用专用测试会话；资源测试会中止程序，文件测试只操作自己的演示目录。下表是待执行清单，不是本次已经全部通过的声明。

| 编号 | 操作 | 通过标准 |
| --- | --- | --- |
| U01 | 绑定 Docker，执行 WORKBENCH_OK | 状态空闲，输出正确，退出码 0 |
| U02 | EARLY/LATE 与 read 输入练习 | 有提前输出，stdin 正确回显 |
| U03 | sleep 中断、窗口尺寸练习 | 命令确实结束，stty 尺寸变化，布局不遮挡 |
| U04 | 上传、下载、移动、取消删除、确认删除 | 文件完整；取消不删除；同名覆盖和非空目录删除被拒绝 |
| U05 | CSV/HTML 手工文件与 Agent PPTX | 三类均可直接查看；样例 PPTX 5 页，数据任务 PPTX 3 页 |
| U06 | 刷新页面、重开工作台 | 绑定配置保持；不自动执行旧命令；已持久化产物可继续查看 |
| U07 | E2B 新会话重复 U01–U05 | 实际 provider 为 e2b；记录兼容部署或云端类型，不混写 |
| U08 | 双租户同时运行与越权请求 | 自身进程/文件可见，另一租户标记不可见；越权拒绝 |
| U09 | 直接请求非法路径 | 服务端 400 拒绝，无目录外内容返回 |
| U10 | 网页隔离检查 | 无同源权限、脚本或外部资源请求；主站测试标记不泄漏 |
| U11 | 超时、CPU/RSS 与关闭清理 | 超限或中断后命令树结束，原因正确；unknown 不能算通过 |
| U12 | 审计、分页与撤销权限 | accepted/完成可对应，较早记录可翻页；撤销后旧终端不能继续启动命令 |

### 双租户与双控制台

准备 A、B 两个 Web 账号，分别属于不同测试空间，各自有一份可用配置；浏览器用两个独立用户配置文件，不要用共享登录态的两个标签页。各自新建会话并绑定。

A 的工作台命令框执行：

```bash
printf 'tenant-A\n' > /workspace/output/tenant-A-only.txt
bash -c 'printf "TENANT_A_READY\n"; read -r value' wb-tenant-A-process
```

B 同时执行（保持 A 等待输入）：

```bash
printf 'tenant-B\n' > /workspace/output/tenant-B-only.txt
python3 - <<'PY'
from pathlib import Path
for path in Path('/proc').glob('[0-9]*/cmdline'):
    try:
        print(path.parent.name, path.read_bytes().replace(b'\0', b' ').decode(errors='replace'))
    except OSError:
        pass
PY
test ! -e /workspace/output/tenant-A-only.txt && printf 'A_FILE_NOT_VISIBLE\n'
bash -c 'printf "TENANT_B_READY\n"; read -r value' wb-tenant-B-process
```

B 应看到自己的命令，不能看到 `wb-tenant-A-process`。A 输入 `ok` 回车后，再单独执行上面的 `python3` 片段和 `test ! -e /workspace/output/tenant-B-only.txt`，不能看到仍在 B 运行的标记或文件。两边文件列表仅有自身标记。最后 B 输入 `ok` 结束，核对双方审计。默认每条命令 120 秒，检查应在此窗口内完成；超时则重新开始，不把已经退出的对端进程当成隔离成功。

Python 片段只读取 `/proc`，不依赖镜像是否安装 `ps`。进程参数也可能含敏感信息，不将原始输出直接公开。

同一会话在两个标签页打开工作台是另一项测试：第二个控制台应被拒绝占用，不应中断第一个。关闭第一个后再连接，必要时等待约 15 秒租约回收。

### 服务端路径与会话权限

本节面向熟悉开发者工具的验收者。浏览器 Network 面板中找到本人的成功 `GET /api/v1/sessions/<会话ID>/sandbox/files?path=`；使用浏览器的复制请求功能在本机复现，保留本人 Authorization 和 X-Tenant-ID，仅修改目标路径或会话 ID。复制请求含登录凭据，不粘贴到报告、PR 或聊天，不保存带凭据的 HAR。

| 请求变化 | 预期 |
| --- | --- |
| `path=manual-demo` | 200，列出自己的练习目录，作为阳性对照 |
| `path=%2Fworkspace%2Finput` | 400，绝对路径被拒绝 |
| `path=..%2Finput` | 400，路径越界被拒绝 |
| `files/download?path=%2Fetc%2Fhostname` | 400，不返回目录外文件 |
| B 的有效登录请求中，仅把会话 ID 改成 A 的 | 404，不泄漏 A 的会话、文件或审计 |

同空间的非会话所有者也应被拒绝。测试前先确认双方自己的请求都成功，否则 401 或服务故障不能证明隔离。符号链接、inode 竞态、硬链接和特殊节点用后面的自动化套件覆盖，勿在实际用户目录制造攻击样本。

### 网页隔离

在工作台文件/产物入口打开自己的静态 `report.html`，用 Elements 查看该预览的 iframe：应有空 `sandbox` 属性及 `referrerpolicy="no-referrer"`，不包含 `allow-scripts` 或 `allow-same-origin`。Network 不应因这份静态预览请求外网资源。不要用普通聊天的其他预览入口替代本次工作台检查。

只看网页正常显示不能证明它无法访问登录态。包含脚本、外链和主站测试标记的攻击回归由 `test:workbench-browser` 完成；它使用隔离浏览器和合成标记，不读取真实账号。

### 超时与撤权

在专用会话执行 `sleep 130`，默认应约 120 秒终止，结果包含 `timeout`；留出清理时间，超时后文件仍可查看。CPU/RSS 使用自动化套件的短预算子测试，不建议手工耗尽机器内存或运行 fork bomb。单进程 Python 报 `MemoryError` 只说明地址空间分配失败，不等于命令树 RSS 超限；后者应明确记录 `memory_limit`。

撤权测试：保持 `sleep 90` 运行，由空间管理员暂时关闭测试空间脚本策略。活动终端会周期复查（约 5 秒加清理时间），之后命令应结束且新命令被拒绝；恢复策略后重新连接。也可在同一浏览器的另一标签页退出登录，再确认旧连接不能执行；登出测试结束后重新登录。不要为了此项验收撤销真实成员权限。

## 自动化测试

### 不依赖模型和真实 provider

在开发机仓库根目录执行。Python 的安全路径测试需要较短的物理临时目录，不能含软链接；macOS 用户可在 Linux 测试环境运行。

```bash
TMPDIR="$(mktemp -d)"
export TMPDIR="$(cd "$TMPDIR" && pwd -P)"
go test ./internal/application/service ./internal/handler ./internal/router \
  -run Workbench -count=1
go test ./internal/sandbox -run 'Workbench|CommandTerminal' -count=1
go test -race ./internal/application/service ./internal/handler ./internal/sandbox \
  -run 'Workbench|CommandTerminal' -count=1
python3 -B -I internal/sandbox/workbench_files_test.py
python3 -B -I internal/sandbox/terminal_runtime_probe_test.py
python3 -B -I internal/sandbox/terminal_runner_test.py
```

构造器及前端：

```bash
test_venv="$(mktemp -d)/venv"
python3 -m venv "$test_venv"
"$test_venv/bin/python" -m pip install -r examples/skills/presentation-builder/requirements.txt
"$test_venv/bin/python" -B -m unittest discover -s examples/skills/presentation-builder/tests -v
(
  cd frontend
  npm ci --no-audit --no-fund
  npm test
  npm run type-check
  npm run build
)
```

依赖下载需网络；不要在正在运行且有重要任务的开发环境中重装依赖。测试通过仅代表这些测试的覆盖范围，不代替真实界面操作。

### 真实 Docker/E2B 及命令树资源限制

先按[测试配置](sandbox-workbench.md#自动化测试)准备专用后端。Docker 可使用本手册构建的镜像：

```bash
export WORKBENCH_TEST_DOCKER_HOST=unix:///var/run/docker.sock
export WORKBENCH_TEST_DOCKER_IMAGE=weknora-sandbox:workbench-manual
go test -tags='sandbox_terminal_integration workbench_integration' ./internal/sandbox \
  -run 'TestTerminalReal|TestWorkbenchFilesIntegration|TestWorkbenchIntegrationConfig' \
  -count=1 -v -timeout=15m
```

远程 daemon 还需 TLS 目录；E2B 需专用 `WORKBENCH_TEST_E2B_*` 凭据和端点，详见配置文档。缺少某后端变量会 skip，那一后端不能记为通过。

重点查看 `StreamingStdinResize`、`Interrupt`、`DescendantCleanup`、`CPU`、`AggregateDescendantCPU`、`AddressSpace`、`AggregateDescendantMemory` 和文件集成测试。套件使用自己的沙箱并负责清理；强制终止测试进程后，管理员应检查遗留资源。只有明确的 `memory_limit` 才记作 RSS 超限，普通 137 或命令自行退出不能冒充。

双租户自动化脚本位于[公开证据分支](https://github.com/mingri31164/WeKnora/blob/submission/sandbox-workbench/submission/verify-concurrent-tenants.mjs)，可由验收者另行取得并审阅。它使用两个独立空间的专用账号，各需 Docker/E2B 配置；凭据 JSON 的 `alice`、`bob` 对象各包含 `email`、`password`、`docker_config_id`、`e2b_config_id`，文件权限设为 600。

```bash
node /path/to/verify-concurrent-tenants.mjs \
  /absolute/path/to/WeKnora-workbench /private/test-accounts.json \
  http://127.0.0.1:8080/api/v1 http://127.0.0.1:5173
```

脚本会登录、创建并删除自己的四个会话，核对同时运行、PID namespace、跨租户拒绝、路径限制和审计；会消耗测试资源。默认参数来自开发机演示环境，复跑时应像上面这样显式指定 API 和 Origin。

### 浏览器安全回归

先按 [Skills 示例的完整命令](../examples/skills/README.md#本地测试与预览)生成 `agent-workbench-demo.pptx`、`workbench-report.html`、`backend-summary.csv`，启动该代码版本的 Vite。然后在开发机的 `frontend/` 执行：

```bash
npx playwright install --with-deps chromium
PREVIEW_ORIGIN=http://127.0.0.1:5173 \
PREVIEW_ARTIFACT_DIR=/absolute/path/to/generated-fixtures \
npm run test:workbench-browser
```

该测试拦截合成 fixture API，不登录真实账号、不访问真实 provider，不证明 Agent 端到端成功。若执行器禁止 GPU、`/proc` 或浏览器启动，记录未执行和错误，不修改隔离权限去绕过，也不把组件测试记成完整浏览器验收。

## 故障排查

| 现象 | 先检查 | 处理 |
| --- | --- | --- |
| 看不到「打开工作台」 | 是否在普通 Web 聊天页、已有会话 ID；前后端是否为本 PR 版本 | 开启后端工作台开关并重启，刷新页面；嵌入聊天不显示工作台 |
| 按钮灰色或未生成会话 | 是否仍在新建聊天页，模型/智能体是否完成设置 | 先创建本人会话；必要时发送一句测试消息建立会话，再打开 |
| 没有沙箱配置或 Docker 添加按钮 | 当前空间、管理员权限、Docker 系统许可、脚本策略 | 管理员在设置页修正；不要用另一空间配置 ID |
| 连接检查成功但终端不可用 | 端点健康与 PTY/文件运行时是不同检查 | 执行完整验证，检查镜像，再显式重新初始化 |
| E2B 模板列表 404 | 控制面是否实现模板目录 | 修复目录服务或使用管理员现有配置；首次体验可改用 Docker |
| Docker socket 权限不足 | app 实际访问的 socket/用户组、远程 TLS 路径 | 按 Docker 部署文档处理，不能 chmod 666 |
| 一直「连接中」或 invalid_ticket | Origin、WebSocket 代理、Ticket 是否过期/复用、登录态 | 重新登录并连接；Ticket 30 秒且一次性，不能复用旧 URL/帧 |
| 提示控制台占用 | 同一会话是否在其他窗口打开工作台 | 先断开旧控制台，必要时等待租约过期再连接 |
| 已绑定但无法切换 provider | 会话绑定锁定原配置 | 新建会话，勿手工改绑定、sandbox ID 或数据库 |
| policy_disabled、forbidden 或 404 | 当前登录、成员状态、脚本策略、会话所有者 | 恢复测试权限或换本人会话；跨租户 404 是预期拒绝 |
| 文件列表没有产出 | 是否写到 `/workspace/output`，当前目录、实际 provider、命令是否成功 | 刷新文件，检查退出码；写在 `/workspace` 的文件不自动出现在文件根目录 |
| 「产物」为空而文件存在 | 文件是否由本轮 Agent 新增/修改并收集 | 等本轮回答完成再刷新；人工创建或未变化的输入不要求成为产物 |
| 找不到 Skill 或 `No module named pptx` | 是否仅登记、安装到另一配置、智能体未选用、旧会话快照 | 完成正确配置的安装、选用后新建会话；普通命令框不自动激活 Skill venv |
| HTML 空白或 Office 被拒绝 | 脚本/CDN 依赖、外部关系、文件/解压预算 | 使用资源内嵌的静态文件；不得通过关闭 iframe/Office 校验解决 |
| 旧内容仍显示 | 是否仍在已打开的文件预览 | 返回列表、刷新、重新打开；消息产物是持久化版本，不跟随沙箱文件修改 |
| 审计只有 `[REDACTED]` | 是否把脱敏当成缺数据 | 按时间、执行 ID、结果核对；不应恢复命令明文 |
| audit_unavailable / 503 | 数据库、审计持久化、Redis、provider | 管理员查服务日志和请求 ID；审计写入失败时拒绝执行属于预期 |
| 时间或内存超限后文件还在 | 是否混淆命令结束与沙箱销毁 | 下载后按清理步骤处理；这是当前生命周期语义 |

## 清理与结果记录

1. 结束运行中的测试命令并关闭控制台。需要留存的结果先从文件或产物入口下载。
2. 仅删除自己的练习文件，清空后再删 `manual-demo`；不要递归清理 `/workspace`，其中可能有会话依赖和上传资料。
3. 删除专用测试会话会触发沙箱销毁，也会影响该会话的记录访问，先保存验收记录。不要删除真实用户会话。
4. Skill 测试涉及快照；Docker daemon 上的技能镜像不是关闭工作台就会释放。由管理员按[快照文档](sandbox-docker-backend.md#快照)处理，勿执行全局 `docker system prune`。
5. 验收记录填写：日期、应用代码 SHA、后端类型/部署形态、用例编号、实际结果、PASS/FAIL/SKIP、失败原因及资源清理结果。截图隐藏邮箱、密钥、Authorization 和供应商信息。

手册中的预期结果用于判定测试，不自动构成已通过证据。尤其要分清 Docker/E2B 兼容与真实云端、组件回归与真实浏览器、手工文件与 Agent 产物、命令树限额与整沙箱配额。
