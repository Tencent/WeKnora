# WeKnora 本地 MCP Demo

最小外部 MCP 服务，用来测试 WeKnora **作为 MCP 客户端**接入第三方工具。

提供 6 个演示工具：

| 工具 | 作用 |
| --- | --- |
| `echo` | 连通性自检 |
| `add` | 两数相加 |
| `server_time` | 返回服务器 UTC 时间 |
| `lookup_policy` | 查询演示政策（保修、报销、POC 等，与 `website-docs/sample-data/` 一致） |
| `list_team_contacts` | 列出演示项目团队成员 |
| `send_demo_alert` | 模拟外发通知（适合测工具人工审批） |

## 1. 启动

```bash
cd examples/mcp-demo
chmod +x start.sh
./start.sh
```

`start.sh` 会自动创建 `.venv` 并安装依赖。默认监听 `http://127.0.0.1:8010/mcp`，鉴权令牌 `weknora-demo-token`。

自定义：

```bash
export MCP_SERVER_AUTH_TOKEN=my-secret
export MCP_PORT=9000
./start.sh
```

## 2. 自检

另开终端：

```bash
cd examples/mcp-demo
source .venv/bin/activate
python test_tools.py
```

应列出 6 个工具。

## 3. 接入 WeKnora

1. 打开 **设置 → MCP 服务 → 新建**
2. 填写：

| 字段 | 值 |
| --- | --- |
| 名称 | `本地 MCP Demo` |
| 传输 | **HTTP Streamable** |
| URL | `http://127.0.0.1:8010/mcp` |
| 认证 | **Bearer** |
| 令牌 | `weknora-demo-token`（与 `MCP_SERVER_AUTH_TOKEN` 一致） |

3. 保存后点 **测试连接**，应发现 6 个工具。
4. 在 **智能体** 配置里勾选该 MCP 服务（或选全部工具）。
5. （可选）对 `send_demo_alert` 开启**人工审批**，对话时 Agent 调用前会弹出确认。

## 4. 建议试的问题

在 Agent 对话里问：

- 「调用 MCP 工具查一下智能家居中控保修多久」→ 应触发 `lookup_policy`
- 「研发部 POC 负责人是谁」→ `lookup_policy` 或 `list_team_contacts`
- 「现在 MCP Demo 服务器几点」→ `server_time`

若同时导入了 `website-docs/sample-data/` 里的文档，可以对比 **知识库检索答案** 与 **MCP 工具返回** 是否一致。

## 5. 注意事项

- WeKnora UI **不支持 stdio** 传输；必须用 **HTTP Streamable** 或 **SSE**。
- Demo 只绑定 `127.0.0.1`，不要暴露到公网。
- `send_demo_alert` 不会真正发送消息，仅返回模拟结果。

## 6. SSE 模式（可选）

```bash
MCP_TRANSPORT=sse MCP_PORT=8011 ./start.sh
```

WeKnora 里传输选 **SSE**，URL 填 `http://127.0.0.1:8011/sse`。
