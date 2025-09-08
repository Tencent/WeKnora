# WeKnora MCP Server 使用示例

本文档提供了 WeKnora MCP Server 的详细使用示例。

## 基本使用

### 1. 启动服务器

```bash
# 推荐方式 - 使用主入口点
python main.py

# 检查环境配置
python main.py --check-only

# 启用详细日志
python main.py --verbose
```

### 2. 环境配置示例

```bash
# 设置环境变量
export WEKNORA_BASE_URL="http://localhost:8080/api/v1"
export WEKNORA_API_KEY="your_api_key_here"

# 或者在 .env 文件中设置
echo "WEKNORA_BASE_URL=http://localhost:8080/api/v1" > .env
echo "WEKNORA_API_KEY=your_api_key_here" >> .env
```

## MCP 工具使用示例

以下是各种 MCP 工具的使用示例：

### 租户管理

#### 创建租户
```json
{
  "tool": "create_tenant",
  "arguments": {
    "name": "我的公司",
    "description": "公司知识管理系统",
    "business": "technology",
    "retriever_engines": {
      "engines": [
        {"retriever_type": "keywords", "retriever_engine_type": "postgres"},
        {"retriever_type": "vector", "retriever_engine_type": "postgres"}
      ]
    }
  }
}
```

#### 列出所有租户
```json
{
  "tool": "list_tenants",
  "arguments": {}
}
```

### 知识库管理

#### 创建知识库
```json
{
  "tool": "create_knowledge_base",
  "arguments": {
    "name": "产品文档库",
    "description": "产品相关文档和资料",
    "embedding_model_id": "text-embedding-ada-002",
    "summary_model_id": "gpt-3.5-turbo"
  }
}
```

#### 列出知识库
```json
{
  "tool": "list_knowledge_bases",
  "arguments": {}
}
```

#### 获取知识库详情
```json
{
  "tool": "get_knowledge_base",
  "arguments": {
    "kb_id": "kb_123456"
  }
}
```

#### 混合搜索
```json
{
  "tool": "hybrid_search",
  "arguments": {
    "kb_id": "kb_123456",
    "query": "如何使用API",
    "vector_threshold": 0.7,
    "keyword_threshold": 0.5,
    "match_count": 10
  }
}
```

### 知识管理

#### 从URL创建知识
```json
{
  "tool": "create_knowledge_from_url",
  "arguments": {
    "kb_id": "kb_123456",
    "url": "https://docs.example.com/api-guide",
    "enable_multimodel": true
  }
}
```

#### 列出知识
```json
{
  "tool": "list_knowledge",
  "arguments": {
    "kb_id": "kb_123456",
    "page": 1,
    "page_size": 20
  }
}
```

#### 获取知识详情
```json
{
  "tool": "get_knowledge",
  "arguments": {
    "knowledge_id": "know_789012"
  }
}
```

### 模型管理

#### 创建模型
```json
{
  "tool": "create_model",
  "arguments": {
    "name": "GPT-4 Chat Model",
    "type": "KnowledgeQA",
    "source": "openai",
    "description": "OpenAI GPT-4 模型用于知识问答",
    "base_url": "https://api.openai.com/v1",
    "api_key": "sk-...",
    "is_default": true
  }
}
```

#### 列出模型
```json
{
  "tool": "list_models",
  "arguments": {}
}
```

### 会话管理

#### 创建聊天会话
```json
{
  "tool": "create_session",
  "arguments": {
    "kb_id": "kb_123456",
    "max_rounds": 10,
    "enable_rewrite": true,
    "fallback_response": "抱歉，我无法回答这个问题。",
    "summary_model_id": "gpt-3.5-turbo"
  }
}
```

#### 获取会话详情
```json
{
  "tool": "get_session",
  "arguments": {
    "session_id": "sess_345678"
  }
}
```

#### 列出会话
```json
{
  "tool": "list_sessions",
  "arguments": {
    "page": 1,
    "page_size": 10
  }
}
```

### 聊天功能

#### 发送聊天消息
```json
{
  "tool": "chat",
  "arguments": {
    "session_id": "sess_345678",
    "query": "请介绍一下产品的主要功能"
  }
}
```

### 块管理

#### 列出知识块
```json
{
  "tool": "list_chunks",
  "arguments": {
    "knowledge_id": "know_789012",
    "page": 1,
    "page_size": 50
  }
}
```

#### 删除知识块
```json
{
  "tool": "delete_chunk",
  "arguments": {
    "knowledge_id": "know_789012",
    "chunk_id": "chunk_456789"
  }
}
```

## 完整工作流程示例

### 场景：创建一个完整的知识问答系统

```bash
# 1. 启动服务器
python main.py --verbose

# 2. 在 MCP 客户端中执行以下步骤：
```

#### 步骤 1: 创建租户
```json
{
  "tool": "create_tenant",
  "arguments": {
    "name": "技术文档中心",
    "description": "公司技术文档知识管理",
    "business": "technology"
  }
}
```

#### 步骤 2: 创建知识库
```json
{
  "tool": "create_knowledge_base",
  "arguments": {
    "name": "API文档库",
    "description": "所有API相关文档"
  }
}
```

#### 步骤 3: 添加知识内容
```json
{
  "tool": "create_knowledge_from_url",
  "arguments": {
    "kb_id": "返回的知识库ID",
    "url": "https://docs.company.com/api",
    "enable_multimodel": true
  }
}
```

#### 步骤 4: 创建聊天会话
```json
{
  "tool": "create_session",
  "arguments": {
    "kb_id": "知识库ID",
    "max_rounds": 5,
    "enable_rewrite": true
  }
}
```

#### 步骤 5: 开始对话
```json
{
  "tool": "chat",
  "arguments": {
    "session_id": "会话ID",
    "query": "如何使用用户认证API？"
  }
}
```

## 错误处理示例

### 常见错误和解决方案

#### 1. 连接错误
```json
{
  "error": "Connection refused",
  "solution": "检查 WEKNORA_BASE_URL 是否正确，确认服务正在运行"
}
```

#### 2. 认证错误
```json
{
  "error": "Unauthorized",
  "solution": "检查 WEKNORA_API_KEY 是否设置正确"
}
```

#### 3. 资源不存在
```json
{
  "error": "Knowledge base not found",
  "solution": "确认知识库ID是否正确，或先创建知识库"
}
```

## 高级配置示例

### 自定义检索配置
```json
{
  "tool": "hybrid_search",
  "arguments": {
    "kb_id": "kb_123456",
    "query": "搜索查询",
    "vector_threshold": 0.8,
    "keyword_threshold": 0.6,
    "match_count": 15
  }
}
```

### 自定义会话策略
```json
{
  "tool": "create_session",
  "arguments": {
    "kb_id": "kb_123456",
    "max_rounds": 20,
    "enable_rewrite": true,
    "fallback_response": "根据现有知识，我无法准确回答您的问题。请尝试重新表述或联系技术支持。"
  }
}
```

## 性能优化建议

1. **批量操作**: 尽量批量处理知识创建和更新
2. **缓存策略**: 合理设置搜索阈值以平衡准确性和性能
3. **会话管理**: 及时清理不需要的会话以节省资源
4. **监控日志**: 使用 `--verbose` 选项监控性能指标

## 集成示例

### 与 Claude Desktop 集成
在 Claude Desktop 的配置文件中添加：
```json
{
  "mcpServers": {
    "weknora": {
      "command": "python",
      "args": ["path/to/main.py"],
      "env": {
        "WEKNORA_BASE_URL": "http://localhost:8080/api/v1",
        "WEKNORA_API_KEY": "your_api_key"
      }
    }
  }
}
```

项目仓库: https://github.com/NannaOlympicBroadcast/WeKnoraMCP

### 与其他 MCP 客户端集成
参考各客户端的文档，配置服务器启动命令和环境变量。

## 故障排除

如果遇到问题：
1. 运行 `python main.py --check-only` 检查环境
2. 使用 `python main.py --verbose` 查看详细日志
3. 检查 WeKnora 服务是否正常运行
4. 验证网络连接和防火墙设置