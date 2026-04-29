# WeKnora HTTP Client

This package provides a client library for interacting with WeKnora services, supporting all HTTP-based interface calls, making it easier for other modules to integrate with WeKnora services without having to write HTTP request code directly.

## Main Features

The client includes the following main functional modules:

1. **Session Management**: Create, retrieve, update, and delete sessions
2. **Knowledge Base Management**: Create, retrieve, update, and delete knowledge bases
3. **Knowledge Management**: Add, retrieve, and delete knowledge content
4. **Tenant Management**: CRUD operations for tenants
5. **Knowledge Q&A**: Supports regular Q&A and streaming Q&A
6. **Chunk Management**: Query, update, and delete knowledge chunks
7. **Message Management**: Retrieve and delete session messages
8. **Model Management**: Create, retrieve, update, and delete models
9. **Evaluation Function**: Start evaluation tasks and get evaluation results

## Usage

### Creating Client Instance

```go
import (
    "context"
    "github.com/Tencent/WeKnora/client"
    "time"
)

// Create client instance
apiClient := client.NewClient(
    "http://api.example.com", 
    client.WithToken("your-auth-token"),
    client.WithTimeout(30*time.Second),
)
```

### Tenant Configuration

You can set a default tenant with `WithTenantID`; the client will automatically send the `X-Tenant-ID` header:

```go
tenantID := uint64(10000)
apiClient := client.NewClient(
    "http://api.example.com",
    client.WithToken("your-auth-token"),
    client.WithTenantID(tenantID),
)
```

If a single request needs a different tenant, set `TenantID` in the request context. The value can be a `uint64`, `*uint64`, or a numeric string, and it will take precedence over the client default:

```go
ctx := context.WithValue(context.Background(), "TenantID", uint64(10000))
// Pass ctx into any client method to switch to tenant 10000 for that request
```

### Example: Create Knowledge Base and Upload File

```go
// Create knowledge base
kb := &client.KnowledgeBase{
    Name:        "Test Knowledge Base",
    Description: "This is a test knowledge base",
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
    // Handle error
}

// Upload knowledge file with metadata
metadata := map[string]string{
    "source": "local",
    "type":   "document",
}
knowledge, err := apiClient.CreateKnowledgeFromFile(context.Background(), kb.ID, "path/to/file.pdf", metadata)
if err != nil {
    // Handle error
}
```

### Example: Create Session and Chat

```go
// Create session
sessionRequest := &client.CreateSessionRequest{
    KnowledgeBaseID: knowledgeBaseID,
    SessionStrategy: &client.SessionStrategy{
        MaxRounds:        10,
        EnableRewrite:    true,
        FallbackStrategy: "fixed_answer",
        FallbackResponse: "Sorry, I cannot answer this question",
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
    // Handle error
}

// Regular Q&A
answer, err := apiClient.KnowledgeQA(context.Background(), session.ID, &client.KnowledgeQARequest{
    Query: "What is artificial intelligence?",
})
if err != nil {
    // Handle error
}

// Streaming Q&A
err = apiClient.KnowledgeQAStream(context.Background(), session.ID, "What is machine learning?", func(response *client.StreamResponse) error {
    // Handle each response chunk
    fmt.Print(response.Content)
    return nil
})
if err != nil {
    // Handle error
}
```

### Example: Managing Models

```go
// Create model
modelRequest := &client.CreateModelRequest{
    Name:        "Test Model",
    Type:        client.ModelTypeChat,
    Source:      client.ModelSourceInternal,
    Description: "This is a test model",
    Parameters: client.ModelParameters{
        "temperature": 0.7,
        "top_p":       0.9,
    },
    IsDefault: true,
}
model, err := apiClient.CreateModel(context.Background(), modelRequest)
if err != nil {
    // Handle error
}

// List all models
models, err := apiClient.ListModels(context.Background())
if err != nil {
    // Handle error
}
```

### Example: Managing Knowledge Chunks

```go
// List knowledge chunks
chunks, total, err := apiClient.ListKnowledgeChunks(context.Background(), knowledgeID, 1, 10)
if err != nil {
    // Handle error
}

// Update chunk
updateRequest := &client.UpdateChunkRequest{
    Content:   "Updated chunk content",
    IsEnabled: true,
}
updatedChunk, err := apiClient.UpdateChunk(context.Background(), knowledgeID, chunkID, updateRequest)
if err != nil {
    // Handle error
}
```

### Example: Getting Session Messages

```go
// Get recent messages
messages, err := apiClient.GetRecentMessages(context.Background(), sessionID, 10)
if err != nil {
    // Handle error
}

// Get messages before a specific time
beforeTime := time.Now().Add(-24 * time.Hour)
olderMessages, err := apiClient.GetMessagesBefore(context.Background(), sessionID, beforeTime, 10)
if err != nil {
    // Handle error
}
```

## Complete Example

Please refer to the `ExampleUsage` function in the `example.go` file, which demonstrates the complete usage flow of the client.
