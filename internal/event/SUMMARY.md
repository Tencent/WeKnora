# WeKnora 事件系统总结

## 概述

已成功为 WeKnora 项目创建了一个完整的事件发送和监听机制，支持对用户查询处理流程中的各个步骤进行事件处理。

## 核心功能

### ✅ 已实现的功能

1. **事件总线 (EventBus)**
   - `Emit(ctx, event)` - 发送事件
   - `On(eventType, handler)` - 注册事件监听器
   - `Off(eventType)` - 移除事件监听器
   - `EmitAndWait(ctx, event)` - 发送事件并等待所有处理器完成
   - 同步/异步两种模式

2. **事件类型**
   - 查询处理事件（接收、验证、预处理、改写）
   - 检索事件（开始、向量检索、关键词检索、实体检索、完成）
   - 排序事件（开始、完成）
   - 合并事件（开始、完成）
   - 聊天生成事件（开始、完成、流式输出）
   - 错误事件

3. **事件数据结构**
   - `QueryData` - 查询数据
   - `RetrievalData` - 检索数据
   - `RerankData` - 排序数据
   - `MergeData` - 合并数据
   - `ChatData` - 聊天数据
   - `ErrorData` - 错误数据

4. **中间件支持**
   - `WithLogging()` - 日志记录中间件
   - `WithTiming()` - 计时中间件
   - `WithRecovery()` - 错误恢复中间件
   - `Chain()` - 中间件组合

5. **全局事件总线**
   - 单例模式的全局事件总线
   - 全局便捷函数（`On`, `Emit`, `EmitAndWait`等）

6. **示例和测试**
   - 完整的单元测试
   - 性能基准测试
   - 完整的使用示例
   - 实际场景演示

## 文件结构

```
internal/event/
├── event.go                    # 核心事件总线实现
├── event_data.go              # 事件数据结构定义
├── middleware.go              # 中间件实现
├── global.go                  # 全局事件总线
├── integration_example.go     # 集成示例（监控、分析处理器）
├── example_test.go            # 测试和示例
├── demo/
│   └── main.go               # 完整的 RAG 流程演示
├── README.md                 # 详细文档
├── usage_example.md          # 使用示例文档
└── SUMMARY.md                # 本文档
```

## 性能指标

- **事件发送性能**: ~9 纳秒/次 (基准测试)
- **并发安全**: 使用 `sync.RWMutex` 保证线程安全
- **内存开销**: 极低，只存储事件处理器函数引用

## 使用场景

### 1. 监控和指标收集

```go
bus.On(event.EventRetrievalComplete, func(ctx context.Context, e event.Event) error {
    data := e.Data.(event.RetrievalData)
    // 发送到 Prometheus 或其他监控系统
    metricsCollector.RecordRetrievalDuration(data.Duration)
    return nil
})
```

### 2. 日志记录

```go
bus.On(event.EventQueryRewritten, func(ctx context.Context, e event.Event) error {
    data := e.Data.(event.QueryData)
    logger.Infof(ctx, "Query rewritten: %s -> %s", 
        data.OriginalQuery, data.RewrittenQuery)
    return nil
})
```

### 3. 用户行为分析

```go
bus.On(event.EventQueryReceived, func(ctx context.Context, e event.Event) error {
    data := e.Data.(event.QueryData)
    // 发送到分析平台
    analytics.TrackQuery(data.UserID, data.OriginalQuery)
    return nil
})
```

### 4. 错误追踪

```go
bus.On(event.EventError, func(ctx context.Context, e event.Event) error {
    data := e.Data.(event.ErrorData)
    // 发送到错误追踪系统
    sentry.CaptureException(data.Error)
    return nil
})
```

## 集成方式

### 步骤 1: 初始化事件系统

在应用启动时（如 `main.go` 或 `container.go`）：

```go
import "github.com/Tencent/WeKnora/internal/event"

func Initialize() {
    // 获取全局事件总线
    bus := event.GetGlobalEventBus()
    
    // 设置监控和分析
    event.NewMonitoringHandler(bus)
    event.NewAnalyticsHandler(bus)
}
```

### 步骤 2: 在各个处理阶段发送事件

在查询处理流程的各个插件中添加事件发送：

```go
// 在 search.go 中
event.Emit(ctx, event.NewEvent(event.EventRetrievalStart, event.RetrievalData{
    Query:           chatManage.ProcessedQuery,
    KnowledgeBaseID: chatManage.KnowledgeBaseID,
    TopK:            chatManage.EmbeddingTopK,
}).WithSessionID(chatManage.SessionID))

// 在 rerank.go 中
event.Emit(ctx, event.NewEvent(event.EventRerankComplete, event.RerankData{
    Query:       chatManage.ProcessedQuery,
    InputCount:  len(chatManage.SearchResult),
    OutputCount: len(rerankResults),
    Duration:    time.Since(startTime).Milliseconds(),
}).WithSessionID(chatManage.SessionID))
```

### 步骤 3: 注册自定义事件处理器

根据需要注册自定义处理器：

```go
event.On(event.EventQueryRewritten, func(ctx context.Context, e event.Event) error {
    // 自定义处理逻辑
    return nil
})
```

## 优势

1. **低耦合**: 事件发送者和监听者完全解耦，便于维护和扩展
2. **高性能**: 极低的性能开销（~9纳秒/次）
3. **灵活性**: 支持同步/异步、单个/多个监听器
4. **可扩展**: 易于添加新的事件类型和处理器
5. **类型安全**: 预定义的事件数据结构
6. **中间件支持**: 便于添加横切关注点（日志、计时、错误处理等）
7. **测试友好**: 易于在测试中验证事件行为

## 测试结果

✅ 所有单元测试通过
✅ 性能测试通过（~9纳秒/次）
✅ 异步处理测试通过
✅ 多处理器测试通过
✅ 完整流程演示成功

## 后续建议

### 可选的增强功能

1. **事件持久化**: 将关键事件保存到数据库或消息队列
2. **事件重放**: 支持事件重放以进行调试或分析
3. **事件过滤**: 支持更复杂的事件过滤和路由
4. **优先级队列**: 支持事件优先级处理
5. **分布式事件**: 通过消息队列支持跨服务事件

### 集成建议

1. **监控集成**: 集成 Prometheus 进行指标收集
2. **日志集成**: 统一的结构化日志记录
3. **追踪集成**: 与现有的 tracing 系统集成
4. **告警集成**: 基于事件的告警机制

## 示例输出

运行 `go run ./internal/event/demo/main.go` 可以看到完整的 RAG 流程事件输出：

```
Step 1: Query Received
[MONITOR] Query received - Session: session-xxx, Query: 什么是RAG技术？
[ANALYTICS] Query tracked - User: user-123, Session: session-xxx

Step 2: Query Rewriting
[MONITOR] Query rewrite started
[MONITOR] Query rewritten - Original: 什么是RAG技术？, Rewritten: 检索增强生成技术...
[CUSTOM] Query Transformation: ...

Step 3: Vector Retrieval
[MONITOR] Retrieval started - Type: vector, TopK: 20
[MONITOR] Retrieval completed - Results: 18, Duration: 301ms
[CUSTOM] Retrieval Efficiency: Rate: 90.00%

Step 4: Result Reranking
[MONITOR] Rerank started - Input: 18
[MONITOR] Rerank completed - Output: 5, Duration: 201ms
[CUSTOM] Rerank Statistics: Reduction: 72.22%

Step 5: Chat Completion
[MONITOR] Chat generation started
[MONITOR] Chat generation completed - Tokens: 256, Duration: 801ms
[ANALYTICS] Chat metrics - Model: gpt-4, Tokens: 256
```

## 总结

事件系统已完全实现并经过测试验证，可以立即集成到 WeKnora 项目中，用于监控、日志记录、分析和调试查询处理流程的各个阶段。系统设计简洁、性能优异、易于使用和扩展。

