# Claw Skill

Claw Skill 为 OpenClaw 生态中的智能体提供 WeKnora 接入能力，通过 REST API 上传文档、导入网页和执行跨库检索。

技能托管在 ClawHub，包名为 [`@lyingbug/weknora`](https://clawhub.ai/lyingbug/weknora)，许可证为 MIT-0。调用能力与权限由所连接的 WeKnora API 决定。

## 主要功能 {#能做什么}

| 能力 | 对应接口 |
| --- | --- |
| 上传文件 | 把 PDF / Word / Excel 等文档送进知识库并自动解析向量化 |
| 导入网页 | 按 URL 抓取正文写入知识库，支持轮询解析状态 |
| 写入 Markdown | 以 Markdown 创建或编辑知识条目，适合会议记录、结构化笔记 |
| 混合检索 | 单库 `hybrid-search` 与跨库 `knowledge-search`，向量 + 关键词召回 |
| 浏览知识库 | 列出知识库与条目、查看详情 |

## 安装与连接 {#怎么配}

在「设置 → 集成 → Claw Skill」查看安装指引，复制当前实例的 API 地址、环境变量示例和安装命令。

1. **获取 API 凭证**：「设置 → API 信息」里复制 API Key 与 API 地址；
2. **设置环境变量**：在终端或 `~/.zshrc` / `~/.bashrc` 里设置

   ```bash
   export WEKNORA_BASE_URL=https://your-weknora.example.com/api/v1
   export WEKNORA_API_KEY=sk-xxxxx
   ```

3. **安装 Skill**：在装好 OpenClaw CLI 的环境里执行引导页给出的安装命令，或到 ClawHub 页面按指引安装；
4. **验证**：让 Agent 列一次知识库或跑一次检索，确认凭证与网络可达。

## 和 MCP 的关系

Claw Skill 和 MCP Server 均可供外部智能体调用，选择取决于客户端支持的接入方式及所需功能：

| | Claw Skill | MCP Server |
| --- | --- | --- |
| 面向 | OpenClaw / ClawHub 生态的 Agent | 支持 MCP 协议的客户端（Claude Desktop、VS Code Copilot 等） |
| 安装 | ClawHub 安装 Skill | `pip install tencent-weknora-mcp` 或 `uvx` 运行 |
| 传输 | 直接调 REST | stdio / SSE / Streamable HTTP |
| 能力范围 | 导入、检索、浏览（5 类） | 31 个工具，另含租户、模型、会话、Agent 问答、Wiki |
| 文档 | 本篇 | [MCP 集成](../03-features/08-mcp.md) |

需要会话、模型管理或 Wiki 工具时，使用 MCP Server；导入、检索和浏览资料可使用 Claw Skill。

## 相关文档 {#相关}

- 凭证与能力收窄：[租户、用户与认证授权](../03-features/01-tenant-auth.md)
- 底层接口：[API 总览](../04-api/01-api-overview.md)
- 其它集成方式：[Chrome 插件](06-chrome-extension.md)、[MCP 集成](../03-features/08-mcp.md)
