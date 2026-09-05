# Agent Tools 设计评审与重构

## 结论与参考范围

WeKnora 的通用沙箱操作需要四个稳定原语：读取、写入、编辑、执行命令。技能通过指令和运行环境扩展这些原语；知识库、Wiki、历史记录等业务能力继续通过服务端工具提供，因为它们包含租户隔离、访问控制和检索语义。

本次参考 PI，克隆 `badlogic/pi-mono`，固定提交 `17de82d7bea18a6589677a9761baabc2060c9efb`（仓库现转到 `earendil-works/pi`）。重点阅读 coding-agent 的 README、系统提示词、工具注册与实现、路径及文件变更处理，以及 agent-core 的执行循环。本次没有审阅其整个 monorepo，也没有复制实现。

- [设计哲学与默认能力](https://github.com/earendil-works/pi/blob/17de82d7bea18a6589677a9761baabc2060c9efb/packages/coding-agent/README.md)：最小核心、扩展机制、按需技能。
- [工具组合](https://github.com/earendil-works/pi/blob/17de82d7bea18a6589677a9761baabc2060c9efb/packages/coding-agent/src/core/tools/index.ts)：默认 read、bash、edit、write；grep、find、ls 为可选工具。
- [提示词](https://github.com/earendil-works/pi/blob/17de82d7bea18a6589677a9761baabc2060c9efb/packages/coding-agent/src/core/system-prompt.ts)：根据实际工具集组合说明。
- [执行循环](https://github.com/earendil-works/pi/blob/17de82d7bea18a6589677a9761baabc2060c9efb/packages/agent/src/agent-loop.ts)：工具结果驱动后续步骤，支持不同执行策略。

## 评审发现及处理

| 发现 | 后果 | 本次处理 |
| --- | --- | --- |
| read_skill 和 read_sandbox_file 各自维护读取入口，技能读取没有连续分页 | 选择工具和读取大技能都多一层成本 | 合并为 read_file(path, offset, limit, max_bytes)，共享分页及输出预算；按来源保留访问控制 |
| shell、技能脚本执行、目录浏览存在重复入口 | 模型需要先判断工具，再判断解释器，容易来回切换 | 已安装技能统一走 shell 的 `skill_name`；有 shell 时隐藏目录列表工具 |
| 每条工具失败都追加“换一种方式” | 权限问题反复改用其他工具尝试 | 删除统一重试提示；提示词要求先改变相关条件再重试 |
| Cube SDK 文件 API 默认 root，脚本使用 user | 文件工具创建的目录可能无法被脚本修改 | 仅在文件 API 路由统一身份；控制面认证和命令身份保持各自语义 |
| 安装器写技能文件也使用普通账号 | 安装到受保护技能目录失败 | 由服务端安装路径携带局部 maintenance 身份，不对模型开放 root 参数 |
| 目录准备失败被忽略，或移动原目录再创建 | 命令延迟失败，附件和产物路径可能改变 | 同一执行账号准备目录，失败立即返回；保留已有目录、文件和链接 |
| 底层 shell 空 cwd 依赖提供商默认，mkdir 错误被忽略 | 工具之间工作目录不一致 | 默认 `/workspace`，相对文件路径和工具 work_dir 统一解析，准备失败阻止执行 |
| 所有工具可同时执行 | 同轮写脚本、执行、读取结果之间有竞态 | 仅显式列出的读取工具并发；写入、任意命令、未知/MCP 工具按原顺序形成屏障 |
| shell 的超时结果仍标记成功 | 模型可能误判完成 | 超时、终止和执行器错误返回工具失败；普通非零退出仍保留 exit_code 语义 |
| Cube 客户端全局 HTTP 超时覆盖命令流 | 长命令被较短的 HTTP 超时提前终止 | 普通 RPC 受 HTTP 超时控制，命令流使用执行超时和调用者 context |
| Docker 实际使用 sh，但工具宣称 Bash | 数组等合法 Bash 语法失败 | Docker shell 命令显式使用 Bash |
| Prompt 重复描述工具，并要求固定检索和阅读步骤 | 无必要的工具调用与上下文成本 | 简化通用/RAG 模板；已有完整证据时不重复展开；按实际注册工具追加运行说明 |

## 最终工具组合

| 场景 | 默认或自动注册行为 |
| --- | --- |
| 支持 Shell 的技能沙箱 | `shell_exec`、`read_file`、`write_sandbox_file`、`edit_sandbox_file`；read_file 同时承担技能资源读取 |
| 已安装技能 | `shell_exec(skill_name=..., command=...)`，不额外注册 `execute_skill_script` |
| 宿主机技能 | shell_exec 自动准备脚本、辅助文件和二进制资源，再使用同一执行链 |
| 无 Shell 后端 | 技能可阅读、脚本不可执行；按后端能力继续提供文件读写 |
| 只有文件能力 | 保留 `list_sandbox_files` 加读、写、编辑；不依赖 Shell |
| 技能安装器 | 保留独立的安装命令和技能文件写入权限 |
| 未指定 allowed_tools 的后端默认值 | 从 11 项缩为 5 项：knowledge_search、grep_chunks、list_knowledge_chunks、get_document_info、search_conversations |
| 前端自动补齐知识库工具 | 只选四个知识库读取工具；图谱、SQL 需要主动启用 |

业务工具通过默认选择和按能力注册收紧；三个重复的读取/执行 Tool 已删除实现。现有业务 allowlist、预设、MCP 配置继续生效，过期的 Tool 名称不会恢复注册。Web、Memory、Wiki 等仍遵循原有配置及权限过滤。原有 `SkillsEnabled` 执行开关保留；本次没有扩大原先只允许文件访问的会话权限。

## 第二轮合并判断

| 候选 | 决定 | 原因 |
| --- | --- | --- |
| read_skill + read_sandbox_file | 合并为 read_file | 操作都是读取文本，差异由资源来源处理；模型只选路径 |
| execute_skill_script + shell_exec | 全部合并 | 已安装环境选择、宿主机文件准备和 stdin 统一由 Shell 入口承担 |
| list_sandbox_files + shell 的 ls/find | Shell 场景只留 Shell | 无 Shell 时保留目录发现能力 |
| write_skill_file/edit_skill_file 与沙箱写入/编辑 | 保留各自的权限实现 | 两组只在不同角色下注册，不会同时占用普通会话工具列表；安装器写入共享镜像，普通会话写工作区 |
| write + edit | 保留两个操作 | 全量写入和唯一匹配替换具有不同输入及失败语义，强行加 mode 会让 Schema 更难使用 |
| knowledge_search + grep_chunks | 保留 | 语义检索和关键词定位使用不同查询方式和结果结构，适用问题不同 |
| 文档信息、分块、Wiki、历史、数据工具 | 按业务启用 | 对象身份、访问范围和结果语义不同，不通过“万能 read”绕过业务服务 |

统一读取示例：

```json
{"path":"skill://pdf-processing/SKILL.md"}
{"path":"skill://pdf-processing/scripts/analyze.py","offset":1,"limit":100}
{"path":"scripts/report.py","offset":1,"limit":100}
```

`skill://` 使用技能管理器加载已授权的包资源，主文档包含指令、执行方式和附加文件列表；它不是容器路径，也不保证是安装器修改后的镜像文件。工作区路径仍使用沙箱文件 API。宿主机技能资源通过 os.Root 限制在技能目录中，禁止符号链接逃逸。新注册表只向模型暴露 `read_file`；旧读取 Tool 实现已删除，工作区读取成为私有适配器；仅保留历史名称识别和展示。@Skill 指引、模板、模型结果策略、历史压缩、前端名称和图标同步使用新入口。

## 技能执行契约

```json
{"skill_name":"pdf-processing","command":"python3 scripts/report.py"}
```

脚本可先用 `write_sandbox_file(path="scripts/report.py", ...)` 写入。默认 cwd 为 `/workspace`；`work_dir="output/report"` 会解析为 `/workspace/output/report` 并以普通用户创建。每次命令独立设置 cwd，文件在当前会话内持续存在。

指定技能时，运行时检查它是否为允许使用的已安装技能，并为本次命令设置：

- 技能 `.venv/bin`、`node_modules/.bin` 优先的 PATH；使用独立 Bash 子进程执行原命令。
- Python/Node 会话依赖目录及技能 Node 模块目录。
- `WEKNORA_SKILL_DIR`、附件目录及产物目录变量。
- 原有按调用者解析的凭据，不写入容器全局环境。

未指定技能的下一条命令使用系统运行环境。命名了不存在或未接入的技能环境时直接报错，不静默回退到系统解释器。

技能树保持只读。Python 临时依赖通过不带 `skill_name` 的系统 Python 安装到 `/workspace/.skill-packages/<skill>`，再带 `skill_name` 执行。Node 的 `NODE_PATH` 支持 CommonJS；自建 ESM 脚本应在可写项目目录安装依赖，或调用技能目录里的原始脚本，不能假定 NODE_PATH 支持 ESM。

文件工具继续保留写入预算、精确编辑校验、读取分页和二进制处理。Shell 输出保留原来的字节限额和截断策略；完整输出不自动保存，需要时显式重定向到工作区日志。产物仍由 `/workspace/output` 收集，以 `sandbox:<filename>` 引用。

`/workspace/input` 的文件工具写入限制保留；Shell 中“保留附件原件”仍是行为约定，不应解释为本次新增了文件系统只读挂载。

## 旧工具删除与执行统一

删除 `ReadSkillTool`、`ExecuteSkillScriptTool` 和独立 `ReadSandboxFileTool`；共享文件分页与工作区缓存迁入私有 `workspaceFileReader`，技能文件树为普通辅助函数。技能管理器中只服务旧工具的 ExecuteScript、执行配置构造及重复凭据处理也已移除。

`LegacyTool*` 常量和前端旧事件展示仅用于读取历史消息及忽略过期 allowlist，不提供运行时别名或回退执行。新会话没有隐藏的第二套读/执行 Tool。

Shell 的补齐能力：

- `stdin` 可传入最多 65536 字节的文本，保留字面引号、Unicode 和尾部换行；更大的输入使用工作区文件重定向。
- 宿主机技能按资源内容生成版本目录，复制到 `/workspace/.skills/<name>/<revision>`。脚本和二进制资源一起准备；单次准备上限 1000 个文件、合计 32 MiB。文件准备失败不会启动命令或标记准备完成。
- 宿主机虚拟环境、node_modules 和缓存目录不复制；这些技能使用镜像系统运行时及会话依赖目录。需要固定依赖时应安装到标准镜像。
- 相同会话及管理器内复用已准备目录；目录被清理后重新准备。其他会话独立准备。
- 三个正式远程后端都支持 Shell。没有 Shell 时只阅读技能，明确告知执行不可用，不再暴露一个实际无法承接同等能力的执行器。

原来只验证旧 Tool 包装和参数兼容的测试随旧实现删除；对应的环境、凭据、目录、资源及 stdin 行为通过新 Shell 单元测试和 Docker 集成测试验证。旧记录回放测试保留。

删除后的后端回归与专项竞态检查均通过。真实 Docker 验证覆盖已安装虚拟环境和宿主机技能资源准备，并验证二进制资源、带空格参数、Unicode、字面 `$HOME` / `$()` 和尾部换行的 stdin 传递；容器验证镜像的范围见下方验证说明。

## 事实内容与产物生成

截图中的 PPT 工作流只展示了技能/脚本读取、内容文件写入、生成及文件检查，没有展示知识库或网页内容检索。它不能证明当时已注册哪些检索工具，但暴露了 Prompt 中的真实缺口：原检索规则主要围绕“知识库问答”，而技能工作流强调执行，事实性产物容易绕过内容查证。

本次补充统一的运行时内容查证指引，应用于默认及已保存的自定义系统 Prompt：

- PPT、报告、教程、技术操作说明等事实性内容在编写正文之前查证；读取生成技能或脚本、生成文件成功，都不能作为主题事实的证据。
- 优先使用用户材料、指定文档和相关绑定知识库；用户没有写“搜索”也应查询可能相关的知识库。范围不明确时进行聚焦检索，不扫描所有无关知识库。已有充分内容不重复读取。
- 根据本轮真实工具注册表列出可用知识库/Web 检索能力；不因配置中残留工具名或开关而虚构可调用能力，也不扩大检索授权范围。相关连接资源继续按各自工具描述使用。
- `@Skill` / `@MCP` 不排除其他内容来源。纯翻译、排版、创作及已有充分证据的任务不强制检索；尊重用户限定来源或不联网的要求。
- 无可用来源、查询失败或证据不足时说明具体缺口，区分未验证背景知识和受支持结论。事实性产物适当保留来源标题/URL及重要限制，分别核验内容与文件生成。

这属于模型行为指引，没有在文件写入前加入机械的“必须先调用一次搜索”拦截。单次检索不能证明证据充分，纯排版也不应被拦截。自动测试验证默认/自定义模板注入、实际注册能力路由、无检索能力时的缺口说明，以及 PPT 技能选择仍保留知识库范围；不等同于真实模型遵循率评估。

上线后的模型回放应覆盖：

| 场景 | 预期行为 |
| --- | --- |
| 技术 PPT + 相关知识库 + 生成技能 | 写正文前检索并读取足够证据，再生成、核验产物 |
| 知识库无相关内容 + 已启用 Web | 补充权威外部来源，检查版本与前提 |
| 只有生成技能，无相关资料或检索能力 | 明确内容未查证及资料缺口，不声称验证通过 |
| 给定完整文稿，只做排版或翻译 | 直接使用文稿，不新增无关检索 |
| 用户限定只用指定资料 | 保持限定范围，缺口不以外部信息偷偷填补 |
| 有记忆但没有当前主题证据 | 不把个性化记忆或旧结论当作已查证主题事实 |

本次没有调用线上模型回放截图中的真实会话，也没有更改其绑定知识库或 Web 开关。

## 镜像契约与迁移

三个后端的标准模板均须提供：

1. 名为 `user` 的普通账号，以及可用的 HOME。
2. Bash 和工具所需的运行时。
3. 由 `user` 可写、可进入的 `/workspace`；标准镜像预建 input、output。
4. 安装流程维护的只读技能目录；普通会话不能修改它。

当前 `docker/Dockerfile.sandbox` 已满足账号和工作区所有权要求。本次现场检查发现，本机缓存的 `wechatopenai/weknora-sandbox:main` 仍只有名为 `sandbox` 的 UID 1000 账号，`/workspace` 由 root 所有。这类旧镜像不能靠换工具解决，必须使用当前 Dockerfile 重建镜像，并更新所用模板/镜像及后续会话。

不要在运行时自动提权、递归 chown、移动目录或删除链接来兼容旧镜像。已有会话中的错误所有权、目录链接会明确报错并保留数据；必要的恢复应由管理员检查具体路径后执行。切换到新模板前，应先保存所需附件和产物。

已有自定义系统提示词或存储在智能体配置里的旧模板文本不会被批量覆盖。如果其中写死了旧的读取或执行工具，应在采用新运行方式时更新；运行时会根据实际工具集追加新指引及旧读取调用的转换方式。

## 验证

后端回归已通过：

```bash
go test ./internal/sandbox ./internal/agent/... ./internal/application/service \
  ./internal/modelcontext ./internal/config ./internal/types ./internal/container
```

专项 `go test -race` 已通过，覆盖并发读取与变更屏障、结果归一化、技能环境、工作区准备、文件身份和 Cube 超时隔离。前端 `npm run type-check` 已通过。

统一读取的追加验证已通过：技能与工作区路由、无沙箱读取、技能白名单、路径穿越及宿主机符号链接逃逸、连续分页不丢行、二进制抑制、已安装技能的 Shell 指引。前端工具展示和国际化检查共 36 项通过；本机缺少 tsx，使用 Node 原生 TypeScript stripping 运行同一组测试（`node --experimental-strip-types --test src/utils/agent-tool-display.test.mjs src/views/chat/components/AgentStreamDisplay.style.test.mjs src/i18n/localeKeyAudit.test.ts`）。Docker 集成用例也已切到 read_file，并验证读取技能指令后执行脚本的路径。

Docker 集成测试已通过，覆盖文件写入 → 技能 Python 运行 → 编辑 → 再运行、独立命令不继承技能环境、安装器写入、普通命令无法修改技能目录、Bash/HOME、超时、附件上传及产物收集。还验证了超时实际停止进程，以及 output 被替换为链接时保留链接并拒绝执行。既有 Docker conformance 测试本次选择运行上述相关用例，未运行依赖外网安装软件包的用例。

新集成测试可以使用正式构建的标准镜像复跑：

```bash
DOCKER_INTEGRATION_IMAGE=your-standard-sandbox-image \
  go test -tags=docker_integration ./internal/agent/tools \
  -run '^TestShellRuntimeIntegration$' -count=1 -v -timeout=3m
```

本次正式 Dockerfile 构建在 Docker daemon 拉取 `node:20-slim` 时因 DNS 故障失败。实际容器验证使用本机缓存镜像构建的独立 fixture，按当前 Dockerfile 的账号和工作区契约修正后运行；这证明了相关运行路径，不代表完整正式镜像已构建或发布。Cube/E2B 通过本地协议测试验证，本次没有连接真实远端服务。没有进行模型端效果 A/B 测试，因此不宣称具体的 Token 或重试率降幅。
