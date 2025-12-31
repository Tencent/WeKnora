# WeKnora HTTP 客户端

这个包提供了与WeKnora服务进行交互的客户端库，支持所有基于HTTP的接口调用，使其他模块更方便地集成WeKnora服务，无需直接编写HTTP请求代码。

## 主要功能

该客户端包含以下主要功能模块：

1. **会话管理**：创建、获取、更新和删除会话
2. **知识库管理**：创建、获取、更新和删除知识库
3. **知识管理**：添加、获取和删除知识内容
4. **租户管理**：租户的CRUD操作
5. **知识问答**：支持普通问答和流式问答
6. **Agent问答**：支持基于Agent的智能问答，包含思考过程、工具调用和反思
7. **分块管理**：查询、更新和删除知识分块
8. **消息管理**：获取和删除会话消息
9. **模型管理**：创建、获取、更新和删除模型

## 使用方法

### 创建客户端实例

```go
import (
    "context"
    "github.com/Tencent/WeKnora/internal/client"
    "time"
)

// 创建客户端实例
apiClient := client.NewClient(
    "http://api.example.com", 
    client.WithToken("your-auth-token"),
    client.WithTimeout(30*time.Second),
)
```

### 示例：创建知识库并上传文件

```go
// 创建知识库
kb := &client.KnowledgeBase{
    Name:        "测试知识库",
    Description: "这是一个测试知识库",
    ChunkingConfig: client.ChunkingConfig{
        ChunkSize:    500,
        ChunkOverlap: 50,
        Separators:   []string{"\n\n", "\n", ". ", "? ", "! "},
    },
    ImageProcessingConfig: client.ImageProcessingConfig{
        ModelID: "image_model_id",
    },
    EmbeddingModelID: "embedding_model_id",
    SummaryModelID:   "summary_model_id",
}

kb, err := apiClient.CreateKnowledgeBase(context.Background(), kb)
if err != nil {
    // 处理错误
}

// 上传知识文件并添加元数据
metadata := map[string]string{
    "source": "local",
    "type":   "document",
}
knowledge, err := apiClient.CreateKnowledgeFromFile(context.Background(), kb.ID, "path/to/file.pdf", metadata)
if err != nil {
    // 处理错误
}
```

### 示例：创建会话并进行问答

```go
// 创建会话
sessionRequest := &client.CreateSessionRequest{
    KnowledgeBaseID: knowledgeBaseID,
    SessionStrategy: &client.SessionStrategy{
        MaxRounds:        10,
        EnableRewrite:    true,
        FallbackStrategy: "fixed_answer",
        FallbackResponse: "抱歉，我无法回答这个问题",
        EmbeddingTopK:    5,
        KeywordThreshold: 0.5,
        VectorThreshold:  0.7,
        RerankModelID:    "rerank_model_id",
        RerankTopK:       3,
        RerankThreshold:  0.8,
        SummaryModelID:   "summary_model_id",
    },
}

session, err := apiClient.CreateSession(context.Background(), sessionRequest)
if err != nil {
    // 处理错误
}

// 普通问答
answer, err := apiClient.KnowledgeQA(context.Background(), session.ID, &client.KnowledgeQARequest{
    Query: "什么是人工智能?",
})
if err != nil {
    // 处理错误
}

// 流式问答
err = apiClient.KnowledgeQAStream(context.Background(), session.ID, &client.KnowledgeQARequest{
    Query:            "什么是机器学习?",
    KnowledgeBaseIDs: []string{knowledgeBaseID}, // 可选：指定知识库
    WebSearchEnabled: false,                      // 可选：是否启用网络搜索
}, func(response *client.StreamResponse) error {
    // 处理每个响应片段
    fmt.Print(response.Content)
    return nil
})
if err != nil {
    // 处理错误
}
```

### 示例：Agent智能问答

Agent问答提供更强大的智能对话能力，支持工具调用、思考过程展示和自我反思。

```go
// 创建Agent会话
agentSession := apiClient.NewAgentSession(session.ID)

// 进行Agent问答，带完整事件处理
err := agentSession.Ask(context.Background(), "搜索机器学习相关知识并总结要点", 
    func(resp *client.AgentStreamResponse) error {
        switch resp.ResponseType {
        case client.AgentResponseTypeThinking:
            // Agent正在思考
            if resp.Done {
                fmt.Printf("💭 思考: %s\n", resp.Content)
            }
        
        case client.AgentResponseTypeToolCall:
            // Agent调用工具
            if resp.Data != nil {
                toolName := resp.Data["tool_name"]
                fmt.Printf("🔧 调用工具: %v\n", toolName)
            }
        
        case client.AgentResponseTypeToolResult:
            // 工具执行结果
            fmt.Printf("✓ 工具结果: %s\n", resp.Content)
        
        case client.AgentResponseTypeReferences:
            // 知识引用
            if resp.KnowledgeReferences != nil {
                fmt.Printf("📚 找到 %d 条相关知识\n", len(resp.KnowledgeReferences))
                for _, ref := range resp.KnowledgeReferences {
                    fmt.Printf("  - [%.3f] %s\n", ref.Score, ref.KnowledgeTitle)
                }
            }
        
        case client.AgentResponseTypeAnswer:
            // 最终答案（流式输出）
            fmt.Print(resp.Content)
            if resp.Done {
                fmt.Println() // 结束后换行
            }
        
        case client.AgentResponseTypeReflection:
            // Agent的自我反思
            if resp.Done {
                fmt.Printf("🤔 反思: %s\n", resp.Content)
            }
        
        case client.AgentResponseTypeError:
            // 错误信息
            fmt.Printf("❌ 错误: %s\n", resp.Content)
        }
        return nil
    })

if err != nil {
    // 处理错误
}

// 简化版：只关心最终答案
var finalAnswer string
err = agentSession.Ask(context.Background(), "什么是深度学习?", 
    func(resp *client.AgentStreamResponse) error {
        if resp.ResponseType == client.AgentResponseTypeAnswer {
            finalAnswer += resp.Content
        }
        return nil
    })
```

### Agent事件类型说明

| 事件类型 | 说明 | 何时触发 |
|---------|------|---------|
| `AgentResponseTypeThinking` | Agent思考过程 | Agent分析问题和制定计划时 |
| `AgentResponseTypeToolCall` | 工具调用 | Agent决定使用某个工具时 |
| `AgentResponseTypeToolResult` | 工具执行结果 | 工具执行完成后 |
| `AgentResponseTypeReferences` | 知识引用 | 检索到相关知识时 |
| `AgentResponseTypeAnswer` | 最终答案 | Agent生成回答时（流式） |
| `AgentResponseTypeReflection` | 自我反思 | Agent评估自己的回答时 |
| `AgentResponseTypeError` | 错误 | 发生错误时 |

### Agent问答测试工具

我们提供了一个交互式命令行工具用于测试Agent功能：

```bash
cd client/cmd/agent_test
go build -o agent_test
./agent_test -url http://localhost:8080 -kb <knowledge_base_id>
```

该工具支持：
- 创建和管理会话
- 交互式Agent问答
- 实时显示所有Agent事件
- 性能统计和调试信息

详细使用说明请参考 `client/cmd/agent_test/README.md`。

### Agent问答的高级用法

更多高级用法示例，请参考 `agent_example.go` 文件，包括：
- 基础Agent问答
- 工具调用跟踪
- 知识引用捕获
- 完整事件跟踪
- 自定义错误处理
- 流取消控制
- 多会话管理

```

### 示例：管理模型

```go
// 创建模型
modelRequest := &client.CreateModelRequest{
    Name:        "测试模型",
    Type:        client.ModelTypeChat,
    Source:      client.ModelSourceInternal,
    Description: "这是一个测试模型",
    Parameters: client.ModelParameters{
        "temperature": 0.7,
        "top_p":       0.9,
    },
    IsDefault: true,
}
model, err := apiClient.CreateModel(context.Background(), modelRequest)
if err != nil {
    // 处理错误
}

// 列出所有模型
models, err := apiClient.ListModels(context.Background())
if err != nil {
    // 处理错误
}
```

### 示例：管理知识分块

```go
// 列出知识分块
chunks, total, err := apiClient.ListKnowledgeChunks(context.Background(), knowledgeID, 1, 10)
if err != nil {
    // 处理错误
}

// 更新分块
updateRequest := &client.UpdateChunkRequest{
    Content:   "更新后的分块内容",
    IsEnabled: true,
}
updatedChunk, err := apiClient.UpdateChunk(context.Background(), knowledgeID, chunkID, updateRequest)
if err != nil {
    // 处理错误
}
```

### 示例：获取会话消息

```go
// 获取最近消息
messages, err := apiClient.GetRecentMessages(context.Background(), sessionID, 10)
if err != nil {
    // 处理错误
}

// 获取指定时间之前的消息
beforeTime := time.Now().Add(-24 * time.Hour)
olderMessages, err := apiClient.GetMessagesBefore(context.Background(), sessionID, beforeTime, 10)
if err != nil {
    // 处理错误
}
```

## 完整示例

请参考 `example.go` 文件中的 `ExampleUsage` 函数，其中展示了客户端的完整使用流程。