# 更新日志

所有重要的项目更改都将记录在此文件中。

格式基于 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.0.0/)，
并且本项目遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### 修复
- HTTP 传输恢复 `stateless_http=True`，与迁移前 `StreamableHTTPSessionManager(stateless=True)` 行为一致。
- SSE 传输恢复消息端点 `/sse/messages/`，与迁移前路由一致。
- `WeKnoraClient` 使用线程本地 `requests.Session`，避免 MCP 2.x 在线程池中并发调用时出现 Session 竞态。

## [1.1.0] - 2026-07-30

### 修复
- 修复 MCP Python SDK 2.x 下服务器启动崩溃（`AttributeError: 'Server' object has no attribute 'list_tools'`）。
  SDK 2.0 移除了低层 `Server` 的装饰器 API（`@app.list_tools()` / `@app.call_tool()` / `app.get_capabilities()`），
  而通过 `uvx` 拉起已发布包时会解析到最新 2.x，导致连接被关闭。

### 变更
- 将 MCP 服务器实现从低层 `Server` 迁移到高层 `MCPServer` API（mcp 2.x，原 FastMCP 改名）。
  - 28 个工具重写为 `@mcp.tool()` 函数签名式：输入参数走类型标注（schema 由框架自动生成），
    描述走 docstring，返回纯 Python 值由框架序列化。
  - 传输层改用 `run_stdio_async()` / `sse_app()` / `streamable_http_app()`，鉴权仍由 `MCPAuthMiddleware` 包裹。
  - `WeKnoraClient` 业务逻辑（REST/SSE 调用、resolve_*、wiki）保持不变。
- 依赖上限调整为 `mcp>=2,<3`（发布包与开发环境一致，避免再次因无上限被解析到未来破坏性版本）。

### 注意
- 本版本要求运行环境 `mcp>=2`。若临时需要旧版 SDK，可在启动命令加 `--with "mcp<2"`，但建议升级到本版本。

## [1.0.1] - 2026-07-28

### 修复
- 将入口脚本（`run_server.py`、`main.py`、`run.py`）的诊断输出改到 stderr，避免破坏 MCP stdio 协议流导致客户端启动失败
- 修复 wheel 漏打包 `upload_paths.py` 导致的 `ModuleNotFoundError`
- 将 `__init__.py` 改为绝对导入，修复 unittest/pytest 收集测试时的包导入错误

### 变更
- PyPI 分发包名统一为 `weknora-mcp`（命令行入口 `weknora-mcp-server` / `weknora-server` 不变）
- 新增 CI workflow（`.github/workflows/mcp-server.yml`），发布 tag 格式为 `mcp-server-v*`

## [1.0.0] - 2024-01-XX

### 新增
- 初始版本发布
- WeKnora MCP Server 核心功能
- 完整的 WeKnora API 集成
- 空间管理工具
- 知识库管理工具
- 知识管理工具
- 模型管理工具
- 会话管理工具
- 聊天功能工具
- 块管理工具
- 多种启动方式支持
- 命令行参数支持
- 环境变量配置
- 完整的包安装支持
- 开发和生产模式
- 详细的文档和安装指南

### 工具列表
- `create_tenant` - 创建新空间
- `list_tenants` - 列出所有空间
- `create_knowledge_base` - 创建知识库
- `list_knowledge_bases` - 列出知识库
- `get_knowledge_base` - 获取知识库详情
- `delete_knowledge_base` - 删除知识库
- `hybrid_search` - 混合搜索
- `create_knowledge_from_url` - 从 URL 创建知识
- `list_knowledge` - 列出知识
- `get_knowledge` - 获取知识详情
- `delete_knowledge` - 删除知识
- `create_model` - 创建模型
- `list_models` - 列出模型
- `get_model` - 获取模型详情
- `create_session` - 创建聊天会话
- `get_session` - 获取会话详情
- `list_sessions` - 列出会话
- `delete_session` - 删除会话
- `chat` - 发送聊天消息
- `list_chunks` - 列出知识块
- `delete_chunk` - 删除知识块

### 文件结构
```
WeKnoraMCP/
├── __init__.py              # 包初始化文件
├── main.py                  # 主入口点 (推荐)
├── run.py                   # 便捷启动脚本
├── run_server.py           # 原始启动脚本
├── weknora_mcp_server.py   # MCP 服务器实现
├── test_module.py          # 模组测试脚本
├── requirements.txt        # 依赖列表
├── setup.py               # 安装脚本 (传统)
├── pyproject.toml         # 现代项目配置
├── MANIFEST.in            # 包含文件清单
├── LICENSE                # MIT 许可证
├── README.md              # 项目说明
├── INSTALL.md             # 详细安装指南
└── CHANGELOG.md           # 更新日志
```

### 启动方式
1. `python main.py` - 主入口点 (推荐)
2. `python run_server.py` - 原始启动脚本
3. `python run.py` - 便捷启动脚本
4. `python weknora_mcp_server.py` - 直接运行
5. `python -m weknora_mcp_server` - 模块运行
6. `weknora-mcp-server` - 安装后命令行工具
7. `weknora-server` - 安装后命令行工具 (别名)

### 技术特性
- 基于 Model Context Protocol (MCP) 1.0.0+
- 异步 I/O 支持
- 完整的错误处理
- 详细的日志记录
- 环境变量配置
- 命令行参数支持
- 多种安装方式
- 开发和生产模式
- 完整的测试覆盖

### 依赖
- Python 3.10+
- mcp >= 1.0.0
- requests >= 2.31.0

### 兼容性
- 支持 Windows、macOS、Linux
- 支持 Python 3.10-3.12
- 兼容现代 Python 包管理工具