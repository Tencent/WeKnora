# 内置技能、技能发现与沙箱镜像

## 当前提供的能力

在「设置 → 技能管理」切换到「技能发现」：

- 内置 8 个技能包：xlsx、docx、powerpoint、pdf、exploratory-data-analysis、statistical-analysis、scientific-visualization、browser。
- PPT Master 和 Frontend Slides 为社区可选技能，按固定版本链接导入后安装，不随默认镜像分发。腾讯 BrowserSkill 为外部推荐，仅提供来源与使用说明链接。
- 内置卡片说明用途、来源和许可；「安装到沙箱」直接打开目标选择，不跳转沙箱列表。已预装的目标标为可直接使用，无法重复勾选；其他目标可手动安装。只有确认安装时才注册技能目录并提交安装，不因浏览或取消创建记录。
- 沙箱列表采用紧凑摘要，点击浮层分别查看预装和自行安装的技能及状态；未知信息不会显示为零。
- 「我的技能」的沙箱下拉与安装抽屉共用预装判断：预装目标显示可直接使用，其他目标才显示安装加号。
- 智能体选择器使用实际可用技能与目录的合并结果，无安装记录的预装技能也可勾选；查询失败不清空保存的选择。对话 `@` 查询携带会话 ID，经归属校验后读取该会话绑定沙箱的预装元数据，遵循下一轮的镜像更新策略；模型中的技能说明来自同一运行时技能列表。
- 沙箱设置就地展示对应镜像/模板的自带技能。智能体选择该沙箱后，可直接按「全部 / 指定技能 / 不使用」使用，无需额外注册、安装、快照或安装模型。
- 卡片、抽屉、间距使用现有设置页组件及主题变量，以不同颜色的图标、标签和细边线区分用途，支持搜索和分类。

内置包的来源、完整 commit 和逐文件 SHA-256 位于
`internal/builtin/skills/sources.lock.json`。每个包保留 LICENSE、UPSTREAM.md 和原始说明，
WeKnora 的 SKILL.md 负责适配工具名称、沙箱路径与依赖范围。Python 依赖同时提供直接依赖列表
和含校验值的完整锁文件，PPT 的 Node 依赖使用 package-lock.json。四个办公 Skill 基于 OpenAI 公开快照适配，由 WeKnora 接管维护；这不代表 OpenAI 当前仍维护这些历史包。xlsx 的重算脚本保留 Hermes 来源及 MIT 许可。

## 构建沙箱镜像

当前只构建和发布 `office-core` 办公镜像，预装 Word（docx）、Excel（xlsx）、PDF 和 PowerPoint 四个技能，以及 Python 3.12、Node、LibreOffice、Poppler、中英文字体和锁定依赖。不包含 Chromium、browser 或科学分析环境。原有基础沙箱镜像继续保留。

浏览器及三个科学分析技能仍在技能发现中提供，可按需安装；不再单独发布对应的办公镜像档位。Cube 使用同一套核心办公内容，另加提供商所需的 envd 和启动入口。

```bash
# Docker / E2B 基础镜像；省略标签时使用 office-core-<资源版本>。
bash scripts/build_sandbox_office.sh sandbox
# 可显式指定标签。
bash scripts/build_sandbox_office.sh sandbox weknora-sandbox:office-core-2026.09.5
# Cube 镜像包含 envd，只构建 linux/amd64。
bash scripts/build_sandbox_office.sh cube your-registry/weknora-sandbox:office-core-2026.09.5-cube
```

本机构建使用当前 Docker 架构；Docker/E2B 的另一架构可以通过
`DOCKER_DEFAULT_PLATFORM=linux/amd64` 或 `linux/arm64` 显式选择。
脚本从 Go registry 获取资源版本，生成 office-core 清单。Docker 标签中的版本或名字本身不能证明能力。
需要将镜像推送到部署可访问的镜像仓库，并按各提供商的流程注册模板。
应用不会自动发布镜像或修改现有配置的模板 ID。

构建分为依赖安装和资源验证两阶段。只有锁文件或安装器变化才使对应依赖层失效；
修改脚本、说明或清单会重新验证，但复用依赖层。
每个包保留自己的 `.venv`，共用 Python 3.12.11 解释器发行版与下载缓存。
现有锁文件对 `lxml`、`numpy` 和 `Pillow` 有不同版本要求，不能直接合并成一个 Python 环境。
安装器使用 `--require-hashes` 和 `uv pip check`，验证包文件摘要、实际安装所用锁文件摘要与
发布清单一致，再逐个执行 `scripts/weknora_smoke.py`，检查真实输出。
缺包、多包、摘要不匹配、依赖冲突或功能失败都会阻止构建。

全部成功后生成 `/opt/weknora/runtime-manifest.json` 和各包的 `.bundle-digest`。
镜像 Label `org.weknora.skills.manifest` 与运行时清单相同，构建脚本还输出
`dist/skills/<镜像名>.skills.json`，用于云端模板发布。
模型、OCR 大模型下载和完整 Linux 桌面不属于这些镜像。
ARM 镜像保留 `OPENSSL_armcap=0`，兼容可能错误报告 CPU 扩展的虚拟机。

### CI 和版本更新

`.github/workflows/sandbox-skills.yml` 在相关 PR 上构建和验证，在 main 的相关变更、
`v*` 标签及手动运行时发布。Docker/E2B 的 office-core 在 AMD64 和 ARM64 原生 runner 上执行，
Cube 版本在 AMD64 上构建并检查 `:49983/health`。验证包括：

- Go 清单、Skill 执行和沙箱测试；安装器摘要不一致回归测试。
- 成品镜像 Label、运行时清单和实际包目录一致；office-core 不含浏览器及科学分析技能文件。
- 实际容器内的 Skill 功能检查，以及交替执行不同 Skill 时的解释器和依赖版本检查。

全部构建验证通过后才合并和发布标签，例如 `main-office-core`、`vX.Y.Z-office-core`；
Cube 对应标签末尾增加 `-cube`。
同时发布 `build-<run_id>-<run_attempt>-<profile>` 标签，保留与构建绑定的版本。
发布 artifact 记录镜像 digest、Skill 清单及源码 commit；部署建议固定 digest。
现有轻量镜像流水线和标签继续保留。

修改内置包后必须重新生成清单并发布配套镜像；单独修改资源而沿用旧镜像可能使对应 Skill
因摘要不兼容而不可用。服务端只保留当前技能源码，不打包开发阶段旧镜像的历史资源。
依赖/基础镜像更新也应走完整功能验证，新建配置试用后再切换，并保留仍被会话或回滚配置
引用的旧镜像和模板。自行安装的技能需要在新配置上重新安装并验证。

### 运行时环境一致性

`shell_exec` 指定 `skill_name` 后，运行包装器把该包的 `.venv/bin` 放在 PATH 首位；
直接执行包内 Python 脚本也选用同一解释器。内置 `.venv` 缺失时明确报错，
不会悄悄回退到系统 Python。自定义技能仍保留原有环境与安装流程。

这些是已验证镜像和标准调用路径的保证，不是对沙箱任意写操作的隔离：会话仍可运行
Shell、显式选择其他解释器或修改其环境。包摘要声明也不代表每次调用都重新扫描所有依赖。
因此不把各技能 `.venv` 合并成可共同修改的目录，也不在运行时自动升级其依赖。

## 识别预装技能：只读取元数据

| 后端 | 识别来源 | 操作位置 |
|---|---|---|
| Docker | Docker Engine 的 ImageInspect / ImageList 返回的完整 Label | 镜像输入框下自动展示，保留原有两步配置流程 |
| Cube | 发布者清单 + 控制面返回的模板 ID、Version | 模板页导入清单后就地展示 |
| E2B | 发布者清单 + 控制面返回的模板 ID、BuildID | 模板页导入清单后就地展示 |

当前引入的 Cube 和 E2B SDK 不返回模板的自定义 OCI Labels，不能假定远程模板列表会透传它们。
云端在发布模板时，管理员可在对应配置的模板页导入构建输出的 JSON；保存的只有声明，
绑定提供商、服务地址、模板 ID 和构建版本，不上传代码、不启动沙箱、不运行安装代理。
也可以通过已有的沙箱配置 API 在 `template_skills` 中提交相同声明。
同一配置当前只保存一份模板声明；换模板或重建版本后需用对应发布清单更新。
云端应发布独立、固定版本的模板，不在活动模板上重建覆盖；声明匹配的是控制面报告的版本，
不代替镜像发布时的功能检查。未报告版本的模板无法进行可靠匹配，因此显示未知。

完整声明格式：

```json
{
  "provider": "cube",
  "endpoint": "https://cube.example.com",
  "template_id": "tpl-office-core-2026-09-5",
  "revision": "<控制面返回的 Version；E2B 使用 BuildID>",
  "manifest": {
    "schema_version": 1,
    "profile": "office-core",
    "version": "2026.09.5",
    "skills": [{"name": "pdf", "digest": "<发布清单中的完整 SHA-256>", "verified": true}]
  }
}
```

清单只能开放与服务端内嵌资源摘要一致、已声明通过构建检查的技能。
资源版本不匹配的项目会提示暂不可用，不会用当前版本的说明去调用旧脚本。
内置说明及资源由服务端提供，执行直接使用 `/opt/weknora/builtin/skills/<name>` 和其中的 `.venv`；
不会写入租户技能目录或数据库安装表。同名且已就绪、已启用的自定义技能优先。

旧镜像只有 `org.weknora.skills.version` 或 profile Label 时，不能证明其中的包内容一致，显示「预装信息未知」。
不会为查询信息进入容器、自动安装依赖或创建快照。也可在技能卡片中手动选择这个沙箱安装，沿用已有安装与快照流程。Docker 可用新构建脚本补齐 Label；
源文件不变时，依赖层会命中缓存。直接构建 Dockerfile 时必须传入 `CORE_SKILLS_MANIFEST` 和 `BUILTIN_SKILLS_VERSION`。
清单由 `go run ./cmd/builtin-skills-manifest` 生成；缺失或错误清单会使构建失败。建议始终使用构建脚本。

老会话不会因为标签或模板变化而被描述成拥有新技能：Docker 查询实际容器的 Image ID；
Cube/E2B 查询沙箱创建时保存的清单。暂停的沙箱不会被唤醒。旧云端沙箱没有这份创建时元数据，
保守显示未知，新会话才使用新的声明。自定义技能安装生成的快照会保留原构建沙箱的预装清单。

自行添加到目录的技能仍使用原有安装、验证、快照和回滚流程。
这条安装流程和模板自带技能的直接使用互相独立。

安装代理以技能包的 `SKILL.md` 运行配置为准，不会因为上游文档列出可选依赖就全部安装。
存在 `requirements.lock` 时按锁文件及哈希安装，不再另行升级其中的依赖。
Python 校验会核对实际安装版本与声明的版本约束（包括锁文件中的间接依赖），并检查原始依赖清单未被改写；
版本不匹配会进入修复流程，恢复声明版本后重新验证。
安装日志显示当前命令、耗时和最近的输出。Docker 与 E2B 会在命令执行期间持续推送输出；
Cube 当前 SDK 仍只返回最终输出，执行期间显示耗时和等待状态。日志预览有长度和推送频率限制，不进入模型上下文。
普通对话中的 `shell_exec` 也通过 `command_output` 事件显示当前命令的耗时和实时输出，
按 `tool_call_id` 归入对应步骤。进度不代表工具完成；最终结果到达后沿用原有结果卡片，历史记录仍以最终工具结果为准。

向新沙箱安装内置技能时，如果空间目录已有内容不同的同名技能，安装抽屉会说明冲突并提供
「更新并安装」。确认后将目录更新为当前内置版本，再安装到所选沙箱；其他沙箱的已有安装
仍保留原包和版本。空间目录的同名冲突不代表目标镜像已经包含该技能。

## 使用新版镜像

1. 在沙箱设置中新建使用新版镜像/模板的配置，填写连接信息并测试连接。
2. 模板自带的技能可直接使用。自行安装的技能从「我的技能」选择新配置安装；需要保留特定历史版本时，使用对应版本的原始技能包重新添加。
3. 验证安装结果及新配置的环境变量后，在智能体设置中选择新配置，并开启新会话验证。

已有会话继续使用原配置和快照；镜像更新不会自动重建这些会话或自行安装的技能环境。

## 浏览器预览与人工接管

右侧抽屉通过已鉴权的 `GET /api/v1/sessions/:id/sandbox/browser/capabilities` 查询能力。
已有会话以实际绑定镜像为准，未创建沙箱的会话按所选智能体的模板判断。office-core 默认不显示浏览器
标签页；按需安装 browser 后根据实际能力判断。切换会话会清除旧结果并忽略迟到响应。查询不会创建、唤醒或
替换沙箱，也不会启动浏览器。旧版/自定义 browser 在运行中的沙箱里通过固定文件检查识别；
未启动或暂停时保留已注册、就绪的 browser 入口，实际启动后再验证控制器。

内置 `browser`（2026.09.7）使用固定版本 agent-browser 0.37.1 的 Rust 原生程序，通过 CDP 控制 Chromium，不依赖 Playwright 或 Node。它仍需安装 Chromium；office-core 不包含浏览器，browser 按需安装。安装器对 AMD64/ARM64 二进制校验 SHA-256，浏览器版本记录在安装结果中，并随沙箱快照保留。

模型通过 `shell_exec` 的 `skill_name="browser"` 调用 `agent-browser`，先读取 `agent-browser skills get core` 获取安装版本自带的上游说明，再使用 `snapshot`、`get text`、`get html` 等原生命令。WeKnora 的 Python 适配层只管理会话、操作串行化和人工接管，实时传输使用锁定哈希的 websocket-client，模型 CLI 与聊天侧栏共用同一个浏览器。无需额外桌面、VNC 或开放 CDP 端口。

已有安装继续使用其固定源码和运行时，不会自动替换为新版本。需要迁移的沙箱应显式安装新版 browser，并在使用新快照的会话中验证。

- 模型可通过 browser 技能打开网页；用户也可直接点击「启动浏览器 / 恢复浏览器」，无需先进入终端。首次启动按当前智能体解析沙箱配置，已有会话继续使用绑定配置。
- 新版预览接入 agent-browser 原生 WebSocket 流，JPEG 质量 80，最多 10 FPS，前端绘制后再 ACK，避免慢连接积压旧帧。画面和操作通过会话专用 WebSocket 及沙箱执行通道转发，不开放浏览器端口。默认缩放至面板宽度，可切换为实际大小（100%）查看小字；两种显示方式使用相同的远端坐标映射。旧版控制器仍每两秒拉取截图。
- 浏览器面板固定地址栏，连续滚动会合并发送，不会切换输入框的禁用状态。browser 2026.09.7 增加 `cursor` 能力：接管后通过现有实时连接查询悬停元素的光标，仅接受标准 CSS 光标名称。旧版仍可操作，以十字光标提示接管；更新技能并使用新快照创建会话后可同步网页光标。
- 握手使用两分钟有效、仅限指定会话浏览器的票据，不能用于终端。连接期间定期复核登录令牌、空间权限和会话归属。关闭或隐藏面板时断开流、释放接管；断线重连使用递增间隔，后台连接不会创建或唤醒沙箱。
- 接管后支持点击、拖动、滚动、按键、文本输入和导航。拖动使用真实的鼠标按下、移动、松开事件；等待中的移动合并为最新位置，取消、失焦或交还控制会释放按键。点击网页输入框后可直接打字和粘贴；中文在输入法组词结束后一次提交，候选输入位置跟随最近一次点击。交还控制或失焦会停止输入。
- 接管租约阻止模型和另一个面板同时操作浏览器。关闭面板会释放；断线或页面进入后台后，租约最多 35 秒过期。
- 打开预览不会创建、唤醒或更换沙箱。仅用户主动点击启动或恢复时才允许创建或恢复，暂停和未启动状态使用就地提示。
- 浏览器控制器只监听沙箱内部 Unix socket。接口不接受任意 shell、脚本或远程浏览器地址，每次调用检查会话归属和空间权限。
- 截图和下载写入 `/workspace/output`，复用现有产物收集。登录状态仅在该会话中使用；15 分钟无请求后控制器关闭，销毁沙箱也会销毁浏览器。

拖动要求浏览器控制器声明 `pointer` 能力；不支持时会明确提示。按需安装当前 browser
后，在使用新快照的会话中即可使用拖动。

### Lightpanda 评估

2026-09-10 在 Linux ARM64 上实测 agent-browser 0.37.1 + Lightpanda 0.4.0：打开本地测试页面、读取 text/html、snapshot、fill 和按选择器 click 可用；PNG 截图仅有简化文本布局，JPEG 返回 `Page.captureScreenshot: unsupported screenshot format`。当前侧栏依赖 JPEG 预览和真实页面布局，因此不直接切换到 Lightpanda。它可以作为后续不需要完整预览的抓取模式单独评估；不能把返回成功的 CDP 命令当作与 Chromium 视觉行为等价。

参考：[agent-browser 的 Lightpanda 支持](https://agent-browser.dev/engines/lightpanda)、[Lightpanda 截图能力说明](https://lightpanda.io/docs/reference/mcp-tools)。

## 社区技能

PPT Master 使用 SVG、模板和图标生成演示文稿；Frontend Slides 面向 HTML 演示文稿，浏览器预览和导出需选择带浏览器的环境。两者通过技能发现卡片打开安装抽屉，来源固定到已选定的 commit。
Frontend Slides 的导入地址指向 `plugins/frontend-slides/skills/frontend-slides` 子目录。仓库根目录还有其他 SKILL.md，不能作为单技能包导入。

腾讯 BrowserSkill 依赖用户本机的 CLI、daemon 和浏览器扩展，目录将其标为 `local_browser`。它仅作为外部链接推荐，不属于沙箱浏览器能力。

## Prompt 安装

添加技能支持来源链接、ZIP 和安装指令。Prompt 模式先选择目标沙箱，提交后通过 `POST /api/v1/skills/catalog/install-prompt` 启动异步安装，复用普通安装的管理员权限、配置锁、进度、日志、停止、重试、验证和快照流程。

Agent 在维护沙箱的临时目录中根据用户提供的文档、命令、技能 ID 或需求获取真实技能包。服务端校验源码并登记目录后，在最终路径安装依赖。尚未获取源码的任务不会作为可执行技能提供给会话；失败时保留指令供重试。已存在的同名技能会保留。来源需要但环境中没有的凭据会阻止安装，并在日志中说明原因。

## 开发验证

```bash
python3 scripts/vendor_builtin_skills.py
go test ./internal/builtin/skills ./internal/application/service ./internal/handler/... ./internal/router ./internal/sandbox
PYTHONDONTWRITEBYTECODE=1 python3 scripts/test_builtin_browser.py
# Linux 环境先运行 browser/scripts/install.py --with-browser 安装原生程序及 Chromium
PYTHONDONTWRITEBYTECODE=1 WEKNORA_TEST_BROWSER=1 python3 scripts/test_builtin_browser.py
cd frontend
npm run type-check
npm run build
npm run check-i18n
```

`vendor_builtin_skills.py` 默认仅离线校验。`--fetch` 或 `--archive REPOSITORY=PATH`
只恢复锁文件列出的上游资源，并校验其 SHA；不会覆盖 WeKnora 自己维护的适配文件。

## 文档生成与依赖限制

PowerPoint 使用 PptxGenJS 和布局辅助函数，支持可编辑文字和原生图表；PDF 使用可嵌入的中文 TrueType 字体。镜像验证检查生成、转换、字体与文本，但不能替代对最终内容和版式的人工或模型看图验收。

The pinned PptxGenJS 4.0.1 dependency graph currently produces npm audit findings for
its transitive `image-size` package (ICNS/JXL/HEIF parser denial of service,
[GHSA-w3rx-r6r6-pgpr](https://github.com/advisories/GHSA-w3rx-r6r6-pgpr),
[GHSA-5p2g-fcmc-qvqq](https://github.com/advisories/GHSA-5p2g-fcmc-qvqq)).
At this check no fixed image-size release was listed in npm. The bundled example
and layout helpers do not call image-size; this does not certify all third-party
or future author scripts. Do not use `npm audit fix --force` here: its proposed
PptxGenJS downgrade to 1.1.5 would break the selected API. This remains a known
dependency limitation to consider before a production rollout.

技能安装抽屉按安装包 SHA-256 比较版本，差异项放在“可升级”分组，可直接更新，无需先卸载。同名同版本但内容变更也会识别为可升级；升级后的新会话使用新安装，已有会话保留绑定的快照。
