# 对话提示词拼装与可编辑范围

本文供维护提示词、智能体编辑器和模型消息链路的开发者使用。智能体配置与执行流程见[Agent 引擎](../03-features/07-agent.md)。

## 智能推理的系统段

唯一拼装入口是 `internal/agent/prompts.go` 中的 `BuildSystemPromptSections`，引擎和字符串兼容接口 `BuildSystemPromptWithOptions` 均使用它。以下按实际拼装顺序列出；空段在输出时省略。

| 段名 | 来源与职责 |
| --- | --- |
| `base` | 显式正文优先；否则无绑定知识库用 `pure` 默认模板，有知识库用 `rag` 默认模板 |
| `steering` | 处理中途补充与取消，保留未完成任务 |
| `runtime_contract` | 数据与指令边界、当前文档范围、完成条件 |
| `sources` | 根据实际注册的工具集合选择来源规则；技能安装模式使用专门的安装验证说明 |
| `tools` | 通用执行、文件和沙箱约定；具体参数仍在工具定义中 |
| `output` | 公共输出格式、配图条件和完成检查 |
| `skills` | 可用技能元数据；仅在非技能安装模式、具有 `read_file` 且存在元数据时追加 |
| `memory` | 本轮召回的记忆 |
| `protocol` | 来源句柄与输出引用协议 |

自定义正文只替换 `base`，不移除其他运行时段，也不增加工具权限。分段不是模型 API 的不同权限层级；可调用工具仍由后端注册与执行路径控制。浏览器操作契约保留在 `local_browser` 的工具描述中，接入与操作说明见[本机浏览器](../05-clients/09-local-browser.md)。

`base` 的占位符由 `renderPromptPlaceholdersWithStatus` 展开：

| 占位符 | 当前行为 |
| --- | --- |
| `{{knowledge_bases}}` | 指向用户消息中 `runtime_context` 的知识库目录，不展开全文 |
| `{{web_search_status}}` | 根据实际 `web_search` 工具是否注册，展开为 `Enabled` / `Disabled` |
| `{{current_time}}` | `YYYY-MM-DD` 日期 |
| `{{language}}` | 用户语言名 |
| `{{skills}}` | 清空；技能元数据由独立段提供 |

## 当前轮上下文与消息角色

`internal/agent/observe.go` 在当前用户消息中加入 `runtime_context`：日期、会话 ID、绑定知识库摘要、固定文档和问题来源等本轮信息；不把它持久化为历史指令。知识库名称与描述限长并转义，FAQ 答案和文档全文需要通过检索工具读取。

`@MCP` / `@Skill` 产生当前轮的 `must_use` 提示。MCP 提及只为已授权服务设置优先提示，不移除其他已配置服务；尚未暴露的服务通过 `discover_mcp_tools` 发现。技能提及提示先读取对应 `SKILL.md`。这些选择不能授权无关操作，仍受用户明确的来源限制和后端权限约束。

正常工具结果保留 `tool` 角色与调用 ID；消息修复时无法配对的结果以转义后的 `untrusted_tool_result` 数据块保留，不能提升为系统指令。Agent、普通问答和模型兜底共用来源数据边界规则，但三者并不使用完全相同的提示词拼装流程。

达到迭代上限或错误收尾时，`internal/agent/finalize.go` 沿用当前消息列表，保留历史、图片及工具调用配对，再追加收尾请求。最后一次调用不提供工具，设置 `tool_choice=none` 并关闭 thinking。

## 编辑与保存

前端编辑器展示有效正文，`frontend/src/utils/agentPromptTemplates.ts` 在保存时区分模板引用与自定义内容：

- 正文与已知模板完全相同：保存 `system_prompt_id` / `context_template_id`，正文留空。
- 正文被实际修改：保存正文，清除旧模板 ID。
- 改写、兜底字段与当前默认模板一致：保存空值，继承默认值；意图提示词只保存偏离默认值的覆盖项。

例如未修改的 Wiki 模板保存为：

```json
{"agent_mode":"smart-reasoning","system_prompt_id":"wiki_researcher","system_prompt":""}
```

`internal/config/agent_prompts.go` 的 `ResolveCustomAgentPrompts` 在请求时解析引用，显式正文始终优先，引用仅在所属字段与模式的模板集合中查找。解析结果不会写回保存对象；未知引用返回空正文，由调用方使用默认路径。

默认模板更新不会覆盖已有自定义正文，也不会批量迁移历史默认副本。旧配置需要重新跟随模板时，在编辑器恢复默认后保存。

## 维护时检查什么

新增规则先确定归属：角色与领域方法放 `base`，来源选择放 `sources`，调用约定放工具定义或 `tools`，输出形态放 `output`，引用编码放 `protocol`。避免同一规则在多处重复维护。

`[Agent][Prompt] section=... bytes=...` 日志帮助定位各段体积；验证行为还需检查最终模型消息、实际工具注册表及执行权限，并用任务验证来源选择、失败恢复和输出格式。
