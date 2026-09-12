# BrowserSkill 本机浏览器接入

WeKnora 使用 Tencent/BrowserSkill 的官方 daemon 和扩展执行浏览器操作，通过 `local_browser` 薄工具适配接入，无需沙箱、浏览器技能包或用户运行本机命令。

## 使用流程

1. Docker 部署使用包含 BrowserSkill 的 app 和 frontend 镜像，默认从用户当前页面生成连接地址。原生部署需先运行 `./scripts/build_browserskill.sh`，配置下面的两个文件路径并重启服务。
2. 用户在个人设置 → 浏览器连接下载配套扩展，解压后在 Chrome 扩展程序页面加载。当前没有商店发布版本。
3. 在设置页复制一次性配对链接，粘贴到扩展的「远程连接」，核对服务器并连接。同一空间内多个对话共用设备授权。
4. 回到对话描述浏览器任务。首次工具调用默认打开独立任务窗口，后续仅切换该窗口内的任务标签。可在扩展中改用后台标签组，此时在现有 Chrome 窗口创建绿色「WeKnora」标签组，模型切换目标标签不会切走用户正在看的页面。
5. 主对话小预览可按住标题栏上下、左右拖动，也可定位任务标签、暂停操作、继续操作或结束浏览器任务。暂停中断当前浏览器调用并保留页面，结束会关闭任务新建标签、归还借用标签；这些按钮只控制当前对话的浏览器，不会停止整段对话或撤销浏览器配对。请求人工帮助时先显示提示；不会自动激活标签或窗口。借用已有标签必须经过原生确认，归还时解除控制，保留原位置。

浏览器操作期间，从当前任务页通过新窗口链接（包括 `rel=noopener`）或 `window.open` 打开的标签，会按来源登记到当前任务，移入任务窗口或标签组，并在结束时一起回收。后台标签组模式下，若 Chrome 默认激活新标签，扩展会恢复此前查看的标签；浏览器原生激活可能带来短暂切换。任务空闲时手动新建或拖入标签组的页面不会自动获得任务归属。

授权只覆盖任务创建的标签和用户明确批准借用的标签；标签组仅提供视觉标识，不作为权限依据。即使用户把其他标签拖入组内，模型也不会获得其控制权。结束任务只清理任务创建的标签，不关闭共用窗口及其他对话的标签。上游的本机 daemon 连接模式仍使用原有独立窗口。

## 人工参与与恢复

遇到登录、短信验证码、验证码挑战或授权时，Agent 应调用 `request_help`，说明需要用户完成的步骤。对话小预览显示具体提示，点击预览定位浏览器，完成操作后在浏览器帮助提示中确认完成；原工具调用收到完成结果后，Agent 重新观察页面再继续。人工帮助默认等待 5 分钟，上限也是 5 分钟；Agent、网关 RPC 和跨节点转发为结果返回预留额外时间。

取消或超时会保留页面并暂停任务。浏览器窗口里的“中断”和对话预览里的“暂停”都由网关管理暂停状态，并取消正在执行的调用。点击预览“继续操作”后可在同一对话、同一浏览器任务中继续；如果 Agent 已结束本轮，再发送继续指令。恢复前重新读取页面，不自动重放中断的点击或提交。已连接但暂停时无需重新配对；只有设备连接确实断开时才提示等待重连。

## 预览与命令

自动化命令按对话串行执行。UI 预览通过扩展现有 WSS 的独立请求获取，不进入 daemon 自动化队列，因此长导航或人工等待不会占住截图通道。

预览获取当前任务标签的 CDP 画面，不调用激活窗口/标签 API。JPEG 宽度最多 640 像素。预览直接截取视口，再缩小实际位图，不在每次轮询前额外调用布局查询。任务活动期间，前端每次获取画面后间隔 1 秒刷新；服务端短缓存和扩展在途合并限制重复截图。页面隐藏时停止刷新，回到页面后立即更新。它是约 1 fps 的低帧率截图同步，不是视频流；失败超过 5 秒会提示画面暂未更新。

后台任务标签在调试连接期间启用 CDP `Emulation.setFocusEmulationEnabled`，让页面的可见性/焦点与绘制回调继续工作，不激活 Chrome 标签或窗口。仅任务新建或明确借用的标签启用此行为，释放调试连接时由 Chrome 恢复。否则依赖 `requestAnimationFrame` 或可见性事件加载内容的页面可能停在 Loading，直到用户切到该标签。

扩展对截图命令设置 3 秒等待上限，对 DOM 快照、无障碍树等读取命令设置 10 秒上限；读取超时立即终止本次快照，不继续走同一页面的回退读取。错误保留具体 CDP 命令，扩展后台控制台记录超过 2 秒的命令及耗时。此保护不等于取消已经交给 Chrome 的命令，也不自动重放点击或提交。截图超时后仍保留在途标记，直到 Chrome 实际返回或调试连接释放；期间预览不再发送重复截图。框架自动附加配置按调试会话复用，断开后重建，失败后可重试。

上述上限针对单条 CDP 命令，并非整个 `observe`。复杂页面还要在扩展中进行语义树转换和文本渲染；重复操作按钮的上下文收集复用已扫描的兄弟节点前缀，避免逐按钮重复扫描整个列表。缓存仅存在于本次渲染，按作用域、目标名称和已有上下文隔离，每个父节点最多保留 64 种缓存项。慢观察会以 `[bsk observe] slow observation completed` 输出总耗时和分阶段耗时（不含页面内容）；CDP 超过 2 秒时的提示是等待中告警，`slow command settled` 记录 Chrome 实际返回的耗时，`outcome` 区分返回和失败，`late: true` 表示扩展已经超时但 Chrome 此时才完成；本地超时单独记录为 `command failed`。

本轮 Agent 执行结束时，通过工具清理钩子释放当前任务的 Chrome 调试连接。成功完成的普通查询默认关闭任务创建的页面并归还借用标签，避免每轮留下任务分组；输入框的本机浏览器选择偏好仍保留。调用时可设置顶层 `keep_open: true`，为用户要求保留的页面、交付结果或未完成的人工步骤保留整项任务；该标记仅对本轮有效。`request_help` 自动保留任务，人工步骤完成后可通过后续调用的 `keep_open: false` 恢复自动回收。报错、取消、暂停的任务保留现场，不自动关闭。扩展先阻止新截图，再等待在途截图和操作完成，最后释放连接；结束任务也使用同一截图屏障。空闲时服务端只返回缓存画面，卡片显示「已保留最后画面」，不重复截图，下一次浏览器调用恢复连接。Chrome 提示条的消失可能晚于连接释放；同一扩展有其他活动任务时，提示仍可能保留。

导航默认等待 `domcontentloaded`，避免已经可读的页面被慢图片等资源拖住；需要完整加载或网络空闲时可显式指定 `wait_until: "load"` 或 `"networkidle"`。文档就绪不代表异步应用内容已加载，操作前仍需观察目标内容是否出现。

导航返回 `reached: "timeout"` 时，即使 RPC 成功返回，也会标记该次操作未完成并保留任务现场。模型应先重新观察页面，再决定下一步，不能因等待超时自动重放导航。用户点击“结束任务”时会先阻止后续命令、取消正在执行的操作（包括创建任务），再清理任务标签；归还标签失败或清理超时会保留暂停状态，可再次点击结束重试。

模型可调用 `{"method":"screenshot"}` 截取当前视口，或使用最近观察中的元素引用，例如 `{"method":"screenshot","ref":"e3"}` 截取元素区域；可选 `tab_id` 限定到任务内标签。截图通过独立图片内容传给支持视觉的对话模型；非视觉模型使用已配置的图片描述模型，未配置或描述失败时明确告知模型无法查看图片。Base64 不进入工具文本，结果卡片和历史消息保留截图。预览的小尺寸 JPEG 仅用于用户查看，与模型截图分开。

结果卡片展示控制台消息与堆栈、网络请求状态与错误、脚本返回值及具体失败原因；较长结果会截断并提示。小预览展示最近动作、动作耗时、已知页面地址和最近错误；页面地址去掉账号、查询参数和片段。耗时在动作执行期间更新，结束后固定。

支持 Document Picture-in-Picture 的浏览器可点击预览标题栏的“弹出悬浮窗”，把同一个预览和暂停、继续、结束按钮移到置顶窗口。切换标签页时继续同步画面；关闭悬浮窗或点击“返回对话小窗”会恢复页内预览，不结束浏览器任务。任务结束、切换会话或离开对话页面时关闭该会话的悬浮窗。此功能依赖安全上下文（HTTPS 或本机 localhost）及用户点击；不支持的浏览器保留页内小窗，打开失败时显示提示并允许重试。悬浮窗依赖原页面，不能在关闭 WeKnora 页面后独立运行。

模型调用采用扁平参数，例如 `{"method":"navigate","url":"https://example.com"}`、`{"method":"click","ref":"e3"}`、`{"method":"observe"}`。Schema 不再提供 `params` 字段，旧的嵌套格式直接拒绝。服务端校验所选动作的必填字段、字段类型及定位条件，再将参数转换为 BrowserSkill 原生 RPC 信封；会话和设备 ID 始终由服务端绑定。

`wait_ms` 的外层参数为 `duration_ms`，范围 0–10000 毫秒；工具描述、参数 schema 和执行前校验保持一致。命令超时或中断后暂停任务，不自动重放点击/提交。前端显示具体浏览器动作与可读错误，结果卡片分别展示网页内容、截图或标签列表，不展示原始协议 JSON。标签组统一显示 WeKnora，不再附带内部任务 ID。

## 构建与部署

固定源码提交 `5aaa36bf79a201ec40b277ce6c24f2ce23ce37ca`、官方 daemon v0.2.1、协议 v1.1。构建脚本应用 `patches/browserskill/remote-extension-connection.patch`，以冻结依赖构建扩展，并验证 daemon 下载包 SHA-256。产物位于 `artifacts/browserskill/`，不提交二进制。

后台页面绘制和读取超时的修复位于扩展中。更新服务端后，已有用户仍需下载新的配套 ZIP，覆盖原解压目录并在 Chrome 扩展程序页面重新加载；仅重启服务端不会更新已安装的扩展。

### Docker 部署

`docker/Dockerfile.app` 在独立 Node 构建阶段生成配套扩展，按目标 `linux/amd64` 或 `linux/arm64` 下载 daemon，并将 `bsk`、扩展 ZIP 和许可证复制到运行镜像的 `/opt/weknora/browserskill/`。镜像已预设 `BROWSERSKILL_BINARY` 和 `BROWSERSKILL_EXTENSION_PATH`，无需安装 Chrome 或启用沙箱。不要用宿主机的 macOS `bsk` 替换容器中的 Linux 文件。

默认无需设置 `BROWSERSKILL_PUBLIC_URL`。设置页在申请配对时提交 `window.location.origin`，服务端保留域名和端口，将 HTTPS 转为 WSS（本机 HTTP 转为 WS），追加 `/api/v1/local-browser/extension`。例如访问 `https://weknora.example.com:8443`，自动生成 `wss://weknora.example.com:8443/api/v1/local-browser/extension`。地址不依赖反向代理传递的内部 Host 或协议。

仅当网关使用独立域名、自定义代理路径等特殊部署时，在 `.env` 显式覆盖：

```dotenv
BROWSERSKILL_PUBLIC_URL=wss://weknora.example.com/api/v1/local-browser/extension
```

将 app 和 frontend 更新到包含本次修改的镜像后，使用 `docker compose up -d app frontend` 重新创建服务；`docker compose restart` 不会加载修改后的环境变量。需要关闭此功能时，在 `.env` 显式设置 `BROWSERSKILL_BINARY=`。

配套 frontend Nginx 已处理扩展 WebSocket Upgrade，授权 POST 走普通 API 转发，集群内部接口返回 404。若外层还有 Ingress/Nginx，仍需为扩展路径转发 WebSocket，并提供浏览器信任的 TLS 证书；直接暴露 app 的部署须自行屏蔽公共入口的 `/api/v1/local-browser/internal`。

### 原生部署

构建需要 Git、Node.js（镜像使用 Node 24）和 Python 3，以及访问固定的上游源码、npm 依赖和发行包的网络。执行 `./scripts/build_browserskill.sh` 后，将产物复制到下列路径，或将变量改为产物的实际绝对路径。连接地址同样默认从页面自动生成：

```dotenv
BROWSERSKILL_BINARY=/opt/weknora/browserskill/bsk
BROWSERSKILL_EXTENSION_PATH=/opt/weknora/browserskill/browser-skill-weknora-0.2.1.zip
```

构建脚本第二个参数可指定目标平台，例如 `./scripts/build_browserskill.sh ./artifacts/browserskill-linux linux/amd64`；不传时按宿主机系统和架构下载。

本机调试可用 `ws://localhost:8080/api/v1/local-browser/extension`；远程部署要求 WSS，内网可使用受浏览器信任的企业 CA。详细授权、重连、多副本路由、迁移及容量边界见 [生产部署链路](browser-skill-production.md)。

扩展使用官方 MIT 许可，分发时保留 `BrowserSkill-LICENSE`。配对、UI 定位和预览属于本次提出的网关协议约定，并非已经发布的 BrowserSkill 稳定接口。

## 验证

```bash
go test -race ./internal/browserskill ./internal/agent/... ./internal/handler/session \
  ./internal/router ./internal/application/service ./internal/container ./internal/middleware
```

原生/真实扩展测试另设：

```dotenv
BROWSERSKILL_TEST_BINARY=/absolute/path/to/bsk
BROWSERSKILL_TEST_EXTENSION=/absolute/path/to/unpacked-extension
BROWSERSKILL_TEST_CHROMIUM=/absolute/path/to/Chromium
BROWSERSKILL_TEST_PLAYWRIGHT=/absolute/path/to/playwright-core/index.mjs
BROWSERSKILL_TEST_HEADED=1
BROWSERSKILL_TEST_TASK_WINDOW=0 # 可选：通过扩展界面切换后台标签组；不设置时验证默认独立窗口
```

测试使用独立浏览器资料目录，覆盖配对、后台标签组、导航/输入/点击、目标标签截图、人工等待期间预览、显式定位、暂停/继续/结束、任务隔离、授权持久化和双节点路由。目标部署网络、真实模型任务与规模容量仍须现场验收；本机浏览器不保证免除网站验证或 403。

补丁中的 `task-focus.browser.test.ts` 使用独立 Chrome 与原生 CDP 验证后台绘制、内容读取、截图和释放后的可见性恢复，避免 Playwright 默认的焦点模拟掩盖问题。在应用补丁后的 BrowserSkill 源码中，设置 `BSK_GEOMETRY_CHROME` 为测试 Chrome 路径，再执行 `pnpm --filter @browser-skill/extension exec vitest run src/browser-driver/__tests__/task-focus.browser.test.ts`（Node 20 另需 `NODE_OPTIONS=--experimental-websocket`）。

仅验证预览组件的真实 PiP 窗口（使用模拟任务 API，不需要扩展或模型）：

```bash
BROWSERSKILL_TEST_CHROMIUM=/absolute/path/to/Chrome \
BROWSERSKILL_TEST_PLAYWRIGHT=/absolute/path/to/playwright-core/index.mjs \
node frontend/scripts/verify-browser-preview-pip.mjs
```

该验收覆盖真实弹出/还原、窗口样式、切换标签页后同步、隐藏页面分支、共享操作按钮、会话切换清理及不支持/打开失败的回退。

### 输入框选择本机浏览器

扩展弹窗中的“任务打开方式”提供“独立窗口（默认，保持可见）”和“后台标签组”。未保存偏好时默认使用独立窗口，已主动选择后台标签组的用户保持原设置。设置保存在本机扩展中，仅对新浏览器任务生效；已有任务需要先结束，或使用新对话。独立窗口不额外创建 Chrome 标签组，开始时会打开到前台，后续创建、选择任务标签只在该窗口内切换；请手动与聊天窗口并排放置。完全遮挡或最小化仍可能导致读取变慢，因此此模式不是不可见后台运行的性能保证。结束任务只清理明确归属于任务的标签，用户后来移入窗口的页面会保留。

Agent 模式的输入框提供“本机浏览器”开关，可与联网搜索同时开启。选中后，请求携带 `local_browser_enabled: true`；本轮提示词明确要求使用 `local_browser` 完成适用的网页查询与操作，而不是把设备已配对当作使用意图。知识库、MCP、Skill、Shell 等工具仍按智能体配置和本轮选择正常提供，可以配合检索、处理内容及生成文件。联网搜索与浏览器同时开启时，可以先搜索发现链接，再用浏览器读取和操作网页。

浏览器不可用时必须说明原因，不能隐瞒失败或声称已通过浏览器读取。该选择不缩减工具注册范围，实际调用由模型遵循提示词和用户任务安排。选择状态随会话最近一次输入设置恢复，切换智能体会清除；关闭浏览器选择不会改变其他开关。此功能需要使用 Agent 接口，普通问答接口拒绝该字段为 true 的请求。

### 标签组回收

后台标签组模式在结束任务时，先将明确归属于任务的标签移出分组，再关闭这些标签；不会按名称清理所有 WeKnora 分组，也不会移出用户后来加入的标签。取消分组失败时保留任务供重试，不报告清理成功。独立窗口模式不创建标签组。旧版本已经保存且关闭的历史分组不会追溯删除，需要用户在 Chrome 中选择“删除分组”；关闭书签栏显示仅隐藏入口，不等于删除。

Chrome 公开扩展 API 不提供直接删除历史已保存分组的接口。移出分组再关闭利用的是本地分组成员变更路径，参见 [Chromium LocalTabGroupListener](https://chromium.googlesource.com/chromium/src/+/f4e2f70d6dd6d88f0ccb887c1935f0cba9fb802f/chrome/browser/ui/tabs/saved_tab_groups/local_tab_group_listener.cc)。跨设备同步和用户已有历史分组仍需真实 Chrome 验收。
