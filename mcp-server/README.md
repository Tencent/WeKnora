# WeKnora MCP Server

这是一个 Model Context Protocol (MCP) 服务器，提供对 WeKnora 知识管理 API 的访问。

## 快速开始

> 推荐直接参考 [MCP配置说明](./MCP_CONFIG.md)，无需进行以下操作。

### 1. 安装依赖
```bash
pip install -r requirements.txt
```

### 2. 配置环境变量
```bash
# Linux/macOS
export WEKNORA_BASE_URL="http://localhost:8080/api/v1"
export WEKNORA_API_KEY="your_api_key_here"

# Windows PowerShell
$env:WEKNORA_BASE_URL="http://localhost:8080/api/v1"
$env:WEKNORA_API_KEY="your_api_key_here"

# Windows CMD
set WEKNORA_BASE_URL=http://localhost:8080/api/v1
set WEKNORA_API_KEY=your_api_key_here
```

### 3. 运行服务器

**推荐方式 - 使用主入口点：**
```bash
python main.py
```

**其他运行方式：**
```bash
# 使用原始启动脚本
python run_server.py

# 使用便捷脚本
python run.py

# 直接运行服务器模块
python weknora_mcp_server.py

# 作为 Python 模块运行
python -m weknora_mcp_server
```

### 4. 命令行选项
```bash
python main.py --help                 # 显示帮助信息
python main.py --check-only           # 仅检查环境配置
python main.py --verbose              # 启用详细日志
python main.py --version              # 显示版本信息
```

## 安装为 Python 包

### 开发模式安装
```bash
pip install -e .
```

安装后可以使用命令行工具：
```bash
weknora-mcp-server
# 或
weknora-server
```

### 生产模式安装
```bash
pip install .
```

### 构建分发包
```bash
# 使用 setuptools
python setup.py sdist bdist_wheel

# 使用现代构建工具
pip install build
python -m build
```

## 测试模组

运行测试脚本验证模组是否正常工作：
```bash
python test_module.py
```

## 功能特性

该 MCP 服务器提供以下工具：

### 租户管理
- `create_tenant` - 创建新租户
- `list_tenants` - 列出所有租户

### 知识库管理
- `create_knowledge_base` - 创建知识库
- `list_knowledge_bases` - 列出知识库
- `get_knowledge_base` - 获取知识库详情
- `delete_knowledge_base` - 删除知识库
- `hybrid_search` - 混合搜索

### 知识管理
- `create_knowledge_from_url` - 从 URL 创建知识
- `list_knowledge` - 列出知识
- `get_knowledge` - 获取知识详情
- `delete_knowledge` - 删除知识

### 模型管理
- `create_model` - 创建模型
- `list_models` - 列出模型
- `get_model` - 获取模型详情

### 会话管理
- `create_session` - 创建聊天会话
- `get_session` - 获取会话详情
- `list_sessions` - 列出会话
- `delete_session` - 删除会话

### 聊天功能
- `chat` - 发送聊天消息

### 块管理
- `list_chunks` - 列出知识块
- `delete_chunk` - 删除知识块

## 故障排除

如果遇到导入错误，请确保：
1. 已安装所有必需的依赖包
2. Python 版本兼容（推荐 3.10+）
3. 没有文件名冲突（避免使用 `mcp.py` 作为文件名）

## 调用效果

通过 Claude Code 调用 WeKnora MCP 服务器，可实现以下交互流程：

1. **首次使用** — 自动列出可用智能体，引导用户选择
2. **智能体对话** — 基于已选智能体的知识库进行 RAG 问答，支持 SSE 流式返回
3. **会话保持** — 自动管理会话上下文，连续对话无需重复选择
4. **智能体切换** — 随时切换到不同智能体，开始新的对话
5. **新建对话** — 清空上下文，开启全新对话

> 配置方法详见 [MCP配置说明](./MCP_CONFIG.md)

## 常见问题

### Claude Code 中 MCP 返回 401 Unauthorized

**现象：** 在 Claude Code 中配置了 `~/.mcp.json`，但 MCP 工具调用返回 HTTP 401。

**根因：** Claude Code 可能从 `~/.claude.json` 中的 `mcpServers` 字段读取 MCP 配置，而非 `~/.mcp.json`。如果该文件中存有旧的 API Key，会覆盖正确的配置。

**解决方案：**

1. 检查 `~/.claude.json` 中的 `mcpServers.weknora.env.WEKNORA_API_KEY` 是否正确
2. 同时更新 `~/.mcp.json` 和 `~/.claude.json` 中的配置
3. 或在 MCP server 目录下创建 `.env` 文件（从 `.env.example` 复制），填入正确的 API Key
4. 重启 Claude Code 使配置生效

### MCP 工具调用报 "takes 0 positional arguments"

**根因：** MCP SDK 会向所有工具处理函数传递 `arguments` 参数，但部分处理函数未声明该参数。

**解决方案：** 确保所有 `_handle_*` 函数签名包含 `args: dict = None` 参数。已在 v3 版本中修复。

### Windows 上环境变量未传递

**现象：** `.mcp.json` 中配置了 `env` 字段，但 MCP server 未能获取到环境变量。

**解决方案：** 在 MCP server 目录下创建 `.env` 文件作为备选配置，MCP server 会自动读取。