# 使用 uv 运行 WeKnora MCP 服务器

> 更推荐使用`uv`来运行基于python的MCP服务。
>
> 也可通过 PyPI 安装：`pip install tencent-weknora-mcp`，或使用 `uvx --from tencent-weknora-mcp weknora-mcp-server`（官方包名 `tencent-weknora-mcp`，由 [Tencent/WeKnora](https://github.com/Tencent/WeKnora) 维护）。

## 1. 安装 uv

```bash
# macOS/Linux
curl -LsSf https://astral.sh/uv/install.sh | sh

# 或使用 Homebrew (macOS)
brew install uv

# Windows
powershell -ExecutionPolicy ByPass -c "irm https://astral.sh/uv/install.ps1 | iex"
```

## 2. MCP 客户端配置

### Claude Desktop 配置

在 Claude Desktop 设置中添加:

```json
{
  "mcpServers": {
    "weknora": {
      "args": [
        "--directory",
        "/path/WeKnora/mcp-server",
        "run",
        "run_server.py"
      ],
      "command": "uv",
      "env": {
        "WEKNORA_API_KEY": "your_api_key_here",
        "WEKNORA_BASE_URL": "http://localhost:8080/api/v1"
      }
    }
  }
}
```

### Cursor 配置

在 Cursor 中，编辑 MCP 配置文件 (通常在 `~/.cursor/mcp-config.json`):

```json
{
  "mcpServers": {
    "weknora": {
      "command": "uv",
      "args": [
        "--directory",
        "/path/WeKnora/mcp-server",
        "run",
        "run_server.py"
      ],
      "env": {
        "WEKNORA_API_KEY": "your_api_key_here",
        "WEKNORA_BASE_URL": "http://localhost:8080/api/v1"
      }
    }
  }
}
```

### KiloCode 配置

对于 KiloCode 或其他支持 MCP 的编辑器，配置如下:

```json
{
  "mcpServers": {
    "weknora": {
      "command": "uv",
      "args": [
        "--directory",
        "/path/WeKnora/mcp-server",
        "run",
        "run_server.py"
      ],
      "env": {
        "WEKNORA_API_KEY": "your_api_key_here",
        "WEKNORA_BASE_URL": "http://localhost:8080/api/v1"
      }
    }
  }
}
```

### 其他 MCP 客户端

对于一般 MCP 客户端配置:

```json
{
  "mcpServers": {
    "weknora": {
      "command": "uv",
      "args": [
        "--directory",
        "/path/WeKnora/mcp-server",
        "run",
        "run_server.py"
      ],
      "env": {
        "WEKNORA_API_KEY": "your_api_key_here",
        "WEKNORA_BASE_URL": "http://localhost:8080/api/v1"
      }
    }
  }
}
```

## 3. 多用户共享部署（网络传输，http/sse）

一个共享部署的 mcp-server 可以同时服务多个用户：每个 MCP 客户端在连接时携带**自己的** WeKnora API Key，网关把它透传给后端，per-key 的能力位、知识库白名单、会话隔离、审计在 MCP 链路端到端生效。后端按 SHA-256 哈希查 `tenant_api_keys` 验真（吊销/过期即时 401），网关本身不做身份裁决。

### 3.1 鉴权语义

| 入站凭证（任一载体） | 行为 |
|---|---|
| `Authorization: Bearer <sk-...>` | 视为个人 key，透传给后端验真 |
| `X-WeKnora-Key: <sk-...>` | 同上（自定义头通道） |
| `X-MCP-Auth-Token: <sk-...>` | 同上（兼容旧载体名） |
| 凭证存在但格式非法（空/超长/控制字符） | 401 拒绝，**不**回落进程级身份 |
| 无任何凭证 | 默认回落 env `WEKNORA_API_KEY`；`MCP_REQUIRE_USER_KEY=1` 时拒绝 |

相关环境变量：

| 变量 | 作用 |
|---|---|
| `WEKNORA_API_KEY` | 进程级回落身份（stdio / 单用户部署用；多用户共享部署建议留空或仅管理员自用） |
| `MCP_REQUIRE_USER_KEY` | `1` = 强制所有网络请求携带个人 key（共享部署推荐开启） |
| `MCP_USER_API_KEY_HEADER` | 个人 key 自定义入站头名，默认 `X-WeKnora-Key` |

> 历史说明：旧版本要求网络传输配置共享口令 `MCP_SERVER_AUTH_TOKEN`，该机制已移除——任何有效后端 API Key 即可通过网关，无效 key 由后端 401 兜底。

### 3.2 客户端配置示例（共享网关）

**方式一：token 字段填个人 key**（兼容所有客户端，最简）：

```json
{
  "mcpServers": {
    "weknora": {
      "type": "http",
      "url": "http://your-gateway:8082/mcp",
      "headers": {
        "Authorization": "Bearer sk-xiaoa-personal-key"
      }
    }
  }
}
```

**方式二：自定义头携带个人 key**（与网关鉴权语义彻底分离）：

```json
{
  "mcpServers": {
    "weknora": {
      "type": "http",
      "url": "http://your-gateway:8082/mcp",
      "headers": {
        "X-WeKnora-Key": "sk-xiaoa-personal-key"
      }
    }
  }
}
```

> SSE 老传输未对个人 key 透传做保证（消息可能在独立任务中处理）；多用户场景请使用 http (Streamable HTTP) 传输——Docker 镜像默认即 `--transport http`。

### 3.3 工具 × 所需能力位映射

API Key 的能力位（Capabilities）逐工具生效。调用无授权的工具会被后端 403。

| 能力位（OR full_access） | 可用的 MCP 工具 |
|---|---|
| `retrieve` | hybrid_search、list_knowledge_bases、get_knowledge_base、list_knowledge、get_knowledge、list_chunks、wiki_search、wiki_read_page、wiki_index_view |
| `chat` | chat、agent_chat、create_session、get_session、list_sessions、delete_session |
| `read_agents` ∨ `chat` ∨ `manage_agents` | list_agents、get_agent |
| `ingest` | create_knowledge_from_file、create_knowledge_from_url、create_knowledge_from_text、update_knowledge_from_text、delete_knowledge、delete_chunk |
| `manage_kbs` | create_knowledge_base、delete_knowledge_base |
| `manage_models`（读也要求） | create_model、list_models、get_model |
| `manage_spaces` | list_shared_knowledge_bases |
| `manage_tenant_settings` | list_tenants |
| 平台 key 专属 | create_tenant（租户 key 永不可用） |

发 key 建议：开发者最小集 `retrieve + chat`；需要上传文档加 `ingest`；`manage_*` 仅管理员。
