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

<img width="950" height="2063" alt="118d078426f42f3d4983c13386085d7f" src="https://github.com/user-attachments/assets/09111ec8-0489-415c-969d-aa3835778e14" />