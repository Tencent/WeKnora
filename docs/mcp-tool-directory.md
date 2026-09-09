# MCP 配置、持久目录与按需调用

## 设置页面

MCP 配置分两步，使用现有 SettingDrawer，步骤导航沿用沙箱的圆形编号与连接线，分组使用标题和细分隔线。

1. **连接配置**：名称、启用状态、传输协议、URL、请求头、认证及超时。保存后进入第二步；OAuth 授权仍按用户独立进行。
2. **工具与用途说明**：编辑用途摘要和使用说明。进入本步时若还没有目录，会自动连接并拉取 Tools；已保存的目录直接展示，只有点刷新才会再次访问 MCP Server。连接已变更的旧目录不会自动覆盖，需手动刷新。工具启用和审批开关即时保存。

编辑时无需为展示 Tools 再次连接上游。首次使用旧服务需要同步一次：此前数据库只有工具的启用/审批设置，没有完整工具目录可以迁移。

## 存储及字段来源

迁移 `000092_mcp_metadata` 新增服务使用说明及持久目录表。

| 数据 | 存储位置 | 来源与更新方式 |
|---|---|---|
| 服务名称、用途摘要 | `mcp_services.name / description` | 人工配置 |
| 使用说明 | `mcp_services.usage_instructions` | 人工配置；刷新不会覆盖 |
| 原始服务说明 | `mcp_metadata.instructions` | `initialize.result.instructions` |
| 服务端名称、版本及简介 | `mcp_metadata.server_name / server_version / server_description` | `initialize.result.serverInfo` |
| 工具名称、完整描述、完整 schema | `mcp_metadata.tools` JSONB | 完整分页读取 `tools/list` 后原子保存 |
| 同步时间、连接指纹 | `mcp_metadata.synced_at / config_fingerprint` | 后端生成；指纹不返回前端 |
| 工具启用、审批策略 | 原有 `mcp_tool_approvals` | 单独保存；同步不会覆盖 |

目录按 `(tenant_id, service_id, principal)` 隔离。非 OAuth 服务在同一租户内共享一份快照；OAuth 服务使用有效授权主体的 StorageID，同一服务可以有多份用户目录，不回退读取其他用户的快照。主表的服务列表查询不会携带完整 Tools JSON。

连接指纹覆盖传输方式、URL、请求头、认证配置、stdio 配置和环境变量。密钥只参与指纹计算，不写入目录或模型上下文。修改用途文字不使目录失效；修改连接或认证配置后，旧目录仍可在管理页面查看，但标为 stale，不参与运行时工具加载。

刷新使用独立连接，30 秒超时；获取全部工具成功后才写库。失败保留原有快照，页面展示本次错误；不把不完整目录发布成成功结果。成功返回空列表则移除旧工具。刷新前后核对连接指纹，较早开始的慢刷新不能覆盖较新快照。现有客户端限制为 100 页、2,000 个工具及单个 schema 256 KiB；完整快照另有 8 MiB 上限，超限明确失败。

## API

- `GET /api/v1/mcp-services/:id/metadata`：只读库，未同步返回 `data:null`，旧连接的快照返回 `stale:true`；Viewer 及以上权限。
- `POST /api/v1/mcp-services/:id/metadata/refresh`：显式连接并同步。静态认证写入租户共享快照，需要 Admin（或具备 MCP 管理能力的 API key）；OAuth 写入当前用户快照，Viewer 及以上可调用，以便对话内授权后落库。刷新失败对外返回笼统错误，不带回上游地址。
- 服务原有 POST / PUT 接收 `description` 和 `usage_instructions`。凭证继续走独立子资源，编辑说明不会覆盖密钥。
- 旧 `/test`、`/tools` 和 `/resources` API 保留原有行为；新分步页面不通过它们读取缓存。

## 模型暴露链路

**入库不等于全量发送给模型。** 默认使用普通函数调用实现应用层按需加载，不依赖 Responses 或 Anthropic 的原生 tool-search 协议。

1. `registerMCPTools` 筛选当前租户及 Agent 授权范围内的启用服务，安装受权限约束的目录。生产路径注入只读快照 loader；目录缺失时（升级后尚未同步）可在已授权连接上 live `tools/list` 并写入当前主体的快照。OAuth 服务在预加载阶段不会 live 连接（避免弹出授权窗）；对话内工具执行上下文中才允许 live-fill。连接已变更的 stale 快照不会在预加载时 live-fill。模型调用 `list_tools` 且 `refresh=true` 时会重新拉取上游并尝试落库；设置页刷新对静态认证需要 Admin，OAuth 仍可由授权用户同步。
2. `PrepareMCPTools` 预读数据库快照，不连接上游。读取最多 8 路并发，初始等待窗口 1 秒，整体受请求取消及 30 秒超时约束。未就绪服务仍展示在来源目录中。
3. 首轮模型只看到两个入口 `discover_mcp_tools` / `call_mcp_tool`，以及服务级名称、用途说明和状态。来源摘要有 16 KiB 总预算：能放下时模型应直接 `list_tools` / `describe`，不要先 `list_servers`；超出时才提示通过 `list_servers` 分页枚举。每个工具的完整描述和 schema 不在首轮 tools 中。
4. 模型按服务 `list_tools` / `search`，再 `describe` 一个具体工具。`describe` 返回完整描述、schema、原始服务 instructions、人工使用说明、`function_name` 和 `tool_ref`，并记录本次 engine 已读取的定义版本。
5. 下一次模型请求前，`RefreshMCPTools` 把已 describe、或本会话历史里已经用过的工具发布为普通函数。新 engine 会从对话历史恢复 `mcp_*` 函数名和 `call_mcp_tool` 的 `tool_ref`，避免多轮必须重新 describe。也可继续用 `call_mcp_tool` 兼容代理调用。目录操作和后台读取线程不会在并行执行期间修改 registry。
6. 实际执行目标 MCP 工具时才建立所需连接。权限、审批、OAuth 等待、参数校验和结果处理继续走原有执行链。

没有 mention 也能看到所有获准服务；mention 只设置优先使用项，不缩小或扩大 Agent 配置的 `all / selected / none` 授权范围。

`PrepareMCPToolsDirect` 保留全量函数暴露的兼容路径，生产默认不使用。OpenAI 兼容和 Anthropic Messages 都支持上述普通函数的按需加载；Anthropic 适配器支持 tools、tool_use、并行 tool_result 和流式参数拼接。截断的参数流不会作为完整调用执行。

## 与 Codex 的对应和边界

对照 `openai/codex@2cbbf0c9b542a36a1c3284b5e804917635b6f666`：

- `codex-rs/codex-mcp/src/rmcp_client.rs`：初始化后读取工具目录，普通 MCP 的 namespace description 来自 `initialize.instructions`；Apps 使用 Connector 元数据。
- `codex-rs/core/src/mcp_tool_exposure.rs`：工具搜索启用时 Deferred，否则 Direct。
- `codex-rs/core/src/tools/handlers/tool_search_spec.rs`：首轮按来源去重展示名称及可选说明；world state 已声明来源时省略重复列表。
- `codex-rs/core/src/tools/handlers/mcp.rs` / `tool_search.rs`：逐工具元数据构建后台 BM25 索引，命中后加载完整工具定义。

本项目采用相同的“来源可感知、具体定义按需加载”原则，但当前检索是单服务名称/描述子串匹配，加分页枚举和精确 describe，不是 Codex 的原生 BM25 tool_search 协议。持久化管理目录也是本项目的服务端设计，不宣称照搬 Codex 的存储方式。

## 目录完整性、权限和历史

列表默认 20 项、最大 50 项。遵循 `next_cursor` 直到 `has_more=false`；列表摘要不提供可调用引用，必须 describe 完整定义。搜索为空时可以不带 query 枚举，工具可达性不依赖搜索排名。

当前 engine 的目录绑定租户、调用主体及有效 OAuth 主体。读取和执行重新检查服务配置、工具策略；缓存不缓存权限决策。新增到 Agent 授权范围外的服务不会自动进入当前目录。运行时 `list_tools(refresh:true)` 重新读取持久快照，上游同步由管理页面显式发起。

具体函数使用原始服务 ID 和工具名生成稳定哈希后缀，避免 Unicode、大小写、标点、超长名称清洗后冲突。调用引用绑定服务、原始工具名和 schema，旧 schema 被替换后无法沿用旧引用。新 engine 会根据会话历史恢复已用过的函数，无需再 describe 一次。

完整 JSON Schema 校验覆盖组合约束、额外属性、嵌套结构和本地引用。验证器不访问外部 URL 或文件。参数转换和审批后的修改也经过校验。定义结果超过预算会明确失败或整体替换为说明，不使用半截 JSON。

`ToolCall.Name / Args / ID / ProviderMetadata` 保留模型原始调用；`ToolCall.Target` 保存实际服务、工具名及内层参数，供界面和日志展示。工具图片、外部标识及内容项沿用现有处理，不改写成知识库身份。

服务端元数据是外部使用文档，不具有覆盖用户请求或权限策略的权威。函数说明带 external 前缀；目录结果的 notice 和执行结果的 untrusted 标记保留。人工说明与服务端原文分别保存、展示。

## 验证

覆盖完整 Tools/schema 入库及离线读取、刷新失败保留快照、较旧刷新不覆盖新快照、成功空目录、连接变更失效、人工说明保留、租户及 OAuth 主体隔离。1,000 个工具时，默认首轮仍只暴露来源和入口，describe 后只加载目标工具。

HTTP 集成测试覆盖 OpenAI / Anthropic 两种适配器的 Direct 和按需加载流程，验证未 mention 的模型请求、缓存发现阶段无上游请求、实际 MCP 执行和调用历史回传。既有权限、分页、schema、审批及图片回归测试继续保留。
