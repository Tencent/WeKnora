# 更新日志

所有重要的项目更改都将记录在此文件中。

格式基于 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.0.0/)，
并且本项目遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [1.0.0] - 2024-01-XX

### 新增
- 初始版本发布
- WeKnora MCP Server 核心功能
- 完整的 WeKnora API 集成
- 租户管理工具
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
- `create_tenant` - 创建新租户
- `list_tenants` - 列出所有租户
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