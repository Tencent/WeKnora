# 事件系统使用示例

## 在 Chat Pipeline 中集成事件系统

### 1. 在服务初始化时设置事件总线

```go
// internal/container/container.go 或 main.go

import (
    "github.com/Tencent/WeKnora/internal/event"
)

func InitializeEventSystem() {
    // 获取全局事件总线
    bus := event.GetGlobalEventBus()
    
    // 注册监控处理器
    event.NewMonitoringHandler(bus)
    
    // 注册分析处理器
    event.NewAnalyticsHandler(bus)
    
    // 或者注册自定义处理器
    bus.On(event.EventQueryReceived, func(ctx context.Context, e event.Event) error {
        // 自定义处理逻辑
        return nil
    })
}
```

### 2. 在查询处理服务中发送事件

#### 示例：在 search.go 中添加事件

```go
// internal/application/service/chat_pipline/search.go

import (
    "github.com/Tencent/WeKnora/internal/event"
    "time"
)

func (p *PluginSearch) OnEvent(
    ctx context.Context,
    eventType types.EventType,
    chatManage *types.ChatManage,
    next func() *PluginError,
) *PluginError {
    // 发送检索开始事件
    startTime := time.Now()
    event.Emit(ctx, event.NewEvent(event.EventRetrievalStart, event.RetrievalData{
        Query:           chatManage.ProcessedQuery,
        KnowledgeBaseID: chatManage.KnowledgeBaseID,
        TopK:            chatManage.EmbeddingTopK,
        RetrievalType:   "vector",
    }).WithSessionID(chatManage.SessionID))
    
    // 执行检索逻辑
    results, err := p.performSearch(ctx, chatManage)
    if err != nil {
        // 发送错误事件
        event.Emit(ctx, event.NewEvent(event.EventError, event.ErrorData{
            Error:     err.Error(),
            Stage:     "retrieval",
            SessionID: chatManage.SessionID,
            Query:     chatManage.ProcessedQuery,
        }).WithSessionID(chatManage.SessionID))
        return ErrSearch.WithError(err)
    }
    
    // 发送检索完成事件
    event.Emit(ctx, event.NewEvent(event.EventRetrievalComplete, event.RetrievalData{
        Query:           chatManage.ProcessedQuery,
        KnowledgeBaseID: chatManage.KnowledgeBaseID,
        TopK:            chatManage.EmbeddingTopK,
        RetrievalType:   "vector",
        ResultCount:     len(results),
        Duration:        time.Since(startTime).Milliseconds(),
        Results:         results,
    }).WithSessionID(chatManage.SessionID))
    
    chatManage.SearchResult = results
    return next()
}
```

#### 示例：在 rewrite.go 中添加事件

```go
// internal/application/service/chat_pipline/rewrite.go

func (p *PluginRewriteQuery) OnEvent(
    ctx context.Context,
    eventType types.EventType,
    chatManage *types.ChatManage,
    next func() *PluginError,
) *PluginError {
    // 发送改写开始事件
    event.Emit(ctx, event.NewEvent(event.EventQueryRewrite, event.QueryData{
        OriginalQuery: chatManage.Query,
        SessionID:     chatManage.SessionID,
    }).WithSessionID(chatManage.SessionID))
    
    // 执行查询改写
    rewrittenQuery, err := p.rewriteQuery(ctx, chatManage)
    if err != nil {
        return ErrRewrite.WithError(err)
    }
    
    // 发送改写完成事件
    event.Emit(ctx, event.NewEvent(event.EventQueryRewritten, event.QueryData{
        OriginalQuery:  chatManage.Query,
        RewrittenQuery: rewrittenQuery,
        SessionID:      chatManage.SessionID,
    }).WithSessionID(chatManage.SessionID))
    
    chatManage.RewriteQuery = rewrittenQuery
    return next()
}
```

#### 示例：在 rerank.go 中添加事件

```go
// internal/application/service/chat_pipline/rerank.go

func (p *PluginRerank) OnEvent(
    ctx context.Context,
    eventType types.EventType,
    chatManage *types.ChatManage,
    next func() *PluginError,
) *PluginError {
    // 发送排序开始事件
    startTime := time.Now()
    inputCount := len(chatManage.SearchResult)
    
    event.Emit(ctx, event.NewEvent(event.EventRerankStart, event.RerankData{
        Query:      chatManage.ProcessedQuery,
        InputCount: inputCount,
        ModelID:    chatManage.RerankModelID,
    }).WithSessionID(chatManage.SessionID))
    
    // 执行排序
    rerankResults, err := p.performRerank(ctx, chatManage)
    if err != nil {
        return ErrRerank.WithError(err)
    }
    
    // 发送排序完成事件
    event.Emit(ctx, event.NewEvent(event.EventRerankComplete, event.RerankData{
        Query:       chatManage.ProcessedQuery,
        InputCount:  inputCount,
        OutputCount: len(rerankResults),
        ModelID:     chatManage.RerankModelID,
        Duration:    time.Since(startTime).Milliseconds(),
        Results:     rerankResults,
    }).WithSessionID(chatManage.SessionID))
    
    chatManage.RerankResult = rerankResults
    return next()
}
```

#### 示例：在 chat_completion.go 中添加事件

```go
// internal/application/service/chat_pipline/chat_completion.go

func (p *PluginChatCompletion) OnEvent(
    ctx context.Context,
    eventType types.EventType,
    chatManage *types.ChatManage,
    next func() *PluginError,
) *PluginError {
    // 发送聊天开始事件
    startTime := time.Now()
    event.Emit(ctx, event.NewEvent(event.EventChatStart, event.ChatData{
        Query:    chatManage.Query,
        ModelID:  chatManage.ChatModelID,
        IsStream: false,
    }).WithSessionID(chatManage.SessionID))
    
    // 准备模型和消息
    chatModel, opt, err := prepareChatModel(ctx, p.modelService, chatManage)
    if err != nil {
        return ErrGetChatModel.WithError(err)
    }
    
    chatMessages := prepareMessagesWithHistory(chatManage)
    
    // 调用模型
    chatResponse, err := chatModel.Chat(ctx, chatMessages, opt)
    if err != nil {
        event.Emit(ctx, event.NewEvent(event.EventError, event.ErrorData{
            Error:     err.Error(),
            Stage:     "chat_completion",
            SessionID: chatManage.SessionID,
            Query:     chatManage.Query,
        }).WithSessionID(chatManage.SessionID))
        return ErrModelCall.WithError(err)
    }
    
    // 发送聊天完成事件
    event.Emit(ctx, event.NewEvent(event.EventChatComplete, event.ChatData{
        Query:      chatManage.Query,
        ModelID:    chatManage.ChatModelID,
        Response:   chatResponse.Content,
        TokenCount: chatResponse.TokenCount,
        Duration:   time.Since(startTime).Milliseconds(),
        IsStream:   false,
    }).WithSessionID(chatManage.SessionID))
    
    chatManage.ChatResponse = chatResponse
    return next()
}
```

### 3. 在 Handler 层发送请求接收事件

```go
// internal/handler/message.go

func (h *MessageHandler) SendMessage(c *gin.Context) {
    ctx := c.Request.Context()
    
    // 解析请求
    var req types.SendMessageRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }
    
    // 发送查询接收事件
    event.Emit(ctx, event.NewEvent(event.EventQueryReceived, event.QueryData{
        OriginalQuery: req.Content,
        SessionID:     req.SessionID,
        UserID:        c.GetString("user_id"),
    }).WithSessionID(req.SessionID).WithRequestID(c.GetString("request_id")))
    
    // 处理消息...
}
```

### 4. 自定义监控处理器

```go
// internal/monitoring/event_monitor.go

package monitoring

import (
    "context"
    "github.com/Tencent/WeKnora/internal/event"
    "github.com/prometheus/client_golang/prometheus"
)

var (
    retrievalDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "retrieval_duration_milliseconds",
            Help: "Duration of retrieval operations",
        },
        []string{"knowledge_base_id", "retrieval_type"},
    )
    
    rerankDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "rerank_duration_milliseconds",
            Help: "Duration of rerank operations",
        },
        []string{"model_id"},
    )
)

func init() {
    prometheus.MustRegister(retrievalDuration)
    prometheus.MustRegister(rerankDuration)
}

func SetupEventMonitoring() {
    bus := event.GetGlobalEventBus()
    
    // 监控检索性能
    bus.On(event.EventRetrievalComplete, func(ctx context.Context, e event.Event) error {
        data := e.Data.(event.RetrievalData)
        retrievalDuration.WithLabelValues(
            data.KnowledgeBaseID,
            data.RetrievalType,
        ).Observe(float64(data.Duration))
        return nil
    })
    
    // 监控排序性能
    bus.On(event.EventRerankComplete, func(ctx context.Context, e event.Event) error {
        data := e.Data.(event.RerankData)
        rerankDuration.WithLabelValues(data.ModelID).Observe(float64(data.Duration))
        return nil
    })
}
```

### 5. 日志记录处理器

```go
// internal/logging/event_logger.go

package logging

import (
    "context"
    "encoding/json"
    "github.com/Tencent/WeKnora/internal/event"
    "github.com/Tencent/WeKnora/internal/logger"
)

func SetupEventLogging() {
    bus := event.GetGlobalEventBus()
    
    // 对所有事件进行结构化日志记录
    logHandler := event.ApplyMiddleware(
        func(ctx context.Context, e event.Event) error {
            data, _ := json.Marshal(e.Data)
            logger.Infof(ctx, "Event: type=%s, session=%s, request=%s, data=%s",
                e.Type, e.SessionID, e.RequestID, string(data))
            return nil
        },
        event.WithTiming(),
    )
    
    // 注册到所有关键事件
    bus.On(event.EventQueryReceived, logHandler)
    bus.On(event.EventQueryRewritten, logHandler)
    bus.On(event.EventRetrievalComplete, logHandler)
    bus.On(event.EventRerankComplete, logHandler)
    bus.On(event.EventChatComplete, logHandler)
    bus.On(event.EventError, logHandler)
}
```

### 6. 完整的初始化流程

```go
// cmd/server/main.go 或 internal/container/container.go

func Initialize() {
    // 1. 初始化事件系统
    eventBus := event.GetGlobalEventBus()
    
    // 2. 设置监控
    event.NewMonitoringHandler(eventBus)
    
    // 3. 设置分析
    event.NewAnalyticsHandler(eventBus)
    
    // 4. 设置 Prometheus 监控（如果需要）
    // monitoring.SetupEventMonitoring()
    
    // 5. 设置结构化日志（如果需要）
    // logging.SetupEventLogging()
    
    // 6. 其他初始化...
}
```

## 测试事件系统

```go
// 在测试中使用独立的事件总线
func TestMyService(t *testing.T) {
    ctx := context.Background()
    
    // 创建测试专用的事件总线
    testBus := event.NewEventBus()
    
    // 注册测试监听器
    var receivedEvents []event.Event
    testBus.On(event.EventQueryReceived, func(ctx context.Context, e event.Event) error {
        receivedEvents = append(receivedEvents, e)
        return nil
    })
    
    // 执行测试...
    testBus.Emit(ctx, event.NewEvent(event.EventQueryReceived, event.QueryData{
        OriginalQuery: "test",
    }))
    
    // 验证事件
    if len(receivedEvents) != 1 {
        t.Errorf("Expected 1 event, got %d", len(receivedEvents))
    }
}
```

## 异步处理示例

```go
// 对于不影响主流程的事件，可以使用异步模式
func SetupAsyncAnalytics() {
    asyncBus := event.NewAsyncEventBus()
    
    asyncBus.On(event.EventQueryReceived, func(ctx context.Context, e event.Event) error {
        // 异步发送到分析平台，不阻塞主流程
        // sendToAnalyticsPlatform(e)
        return nil
    })
    
    // 使用异步总线发送事件
    // asyncBus.Emit(ctx, event)
}
```

## 性能优化建议

1. **避免在关键路径上使用同步事件总线**：对于不影响业务逻辑的监控、日志等，使用异步模式
2. **合理使用中间件**：只在需要的地方使用中间件，避免不必要的开销
3. **控制事件数据大小**：避免在事件中传递大量数据，特别是在异步模式下
4. **使用专用的监听器**：不要在一个监听器中做太多事情，保持单一职责

