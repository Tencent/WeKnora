package event

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// EventType represents the type of event in the system
type EventType string

const (
	// Query processing events
	EventQueryReceived   EventType = "query.received"   // 用户查询到达
	EventQueryValidated  EventType = "query.validated"  // 查询验证完成
	EventQueryPreprocess EventType = "query.preprocess" // 查询预处理
	EventQueryRewrite    EventType = "query.rewrite"    // 查询改写
	EventQueryRewritten  EventType = "query.rewritten"  // 查询改写完成

	// Retrieval events
	EventRetrievalStart    EventType = "retrieval.start"    // 检索开始
	EventRetrievalVector   EventType = "retrieval.vector"   // 向量检索
	EventRetrievalKeyword  EventType = "retrieval.keyword"  // 关键词检索
	EventRetrievalEntity   EventType = "retrieval.entity"   // 实体检索
	EventRetrievalComplete EventType = "retrieval.complete" // 检索完成

	// Rerank events
	EventRerankStart    EventType = "rerank.start"    // 排序开始
	EventRerankComplete EventType = "rerank.complete" // 排序完成

	// Merge events
	EventMergeStart    EventType = "merge.start"    // 合并开始
	EventMergeComplete EventType = "merge.complete" // 合并完成

	// Chat completion events
	EventChatStart    EventType = "chat.start"    // 聊天生成开始
	EventChatComplete EventType = "chat.complete" // 聊天生成完成
	EventChatStream   EventType = "chat.stream"   // 聊天流式输出

	// Agent events
	EventAgentQuery    EventType = "agent.query"    // Agent 查询开始
	EventAgentPlan     EventType = "agent.plan"     // Agent 计划生成
	EventAgentStep     EventType = "agent.step"     // Agent 步骤执行
	EventAgentTool     EventType = "agent.tool"     // Agent 工具调用
	EventAgentComplete EventType = "agent.complete" // Agent 完成

	// Agent streaming events (for real-time feedback)
	EventAgentThought     EventType = "thought"      // Agent 思考过程
	EventAgentToolCall    EventType = "tool_call"    // 工具调用通知
	EventAgentToolResult  EventType = "tool_result"  // 工具结果
	EventAgentReflection  EventType = "reflection"   // Agent 反思
	EventAgentReferences  EventType = "references"   // 知识引用
	EventAgentFinalAnswer EventType = "final_answer" // 最终答案
	EventAgentAskUser     EventType = "ask_user"     // Agent 向用户提问

	// Error events
	EventError EventType = "error" // 错误事件

	// Session events
	EventSessionTitle EventType = "session_title" // 会话标题更新

	// Control events
	EventStop EventType = "stop" // 停止对话生成
)

// Event represents an event in the system
type Event struct {
	ID        string                 // 事件ID (自动生成UUID，用于流式更新追踪)
	Type      EventType              // 事件类型
	SessionID string                 // 会话ID
	Data      interface{}            // 事件数据
	Metadata  map[string]interface{} // 事件元数据
	RequestID string                 // 请求ID
}

// EventHandler is a function that handles events
type EventHandler func(ctx context.Context, event Event) error

// EventBus manages event publishing and subscription
type EventBus struct {
	mu        sync.RWMutex
	handlers  map[EventType][]EventHandler
	asyncMode bool // 是否异步处理事件
}

// NewEventBus creates a new EventBus instance
func NewEventBus() *EventBus {
	return &EventBus{
		handlers:  make(map[EventType][]EventHandler),
		asyncMode: false,
	}
}

// NewAsyncEventBus creates a new EventBus with async mode enabled
func NewAsyncEventBus() *EventBus {
	return &EventBus{
		handlers:  make(map[EventType][]EventHandler),
		asyncMode: true,
	}
}

// On registers an event handler for a specific event type
// Multiple handlers can be registered for the same event type
func (eb *EventBus) On(eventType EventType, handler EventHandler) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	eb.handlers[eventType] = append(eb.handlers[eventType], handler)
}

// Off removes all handlers for a specific event type
func (eb *EventBus) Off(eventType EventType) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	delete(eb.handlers, eventType)
}

// Emit publishes an event to all registered handlers
// Returns error if any handler fails (in sync mode)
// Automatically generates an ID for the event if not provided (from source)
func (eb *EventBus) Emit(ctx context.Context, event Event) error {
	// Auto-generate ID if not provided (from source)
	if event.ID == "" {
		event.ID = uuid.New().String()
	}

	eb.mu.RLock()
	handlers, exists := eb.handlers[event.Type]
	eb.mu.RUnlock()

	if !exists || len(handlers) == 0 {
		// No handlers registered for this event type
		return nil
	}

	if eb.asyncMode {
		// Async mode: fire and forget
		for _, handler := range handlers {
			h := handler // capture loop variable
			go func() {
				_ = h(ctx, event)
			}()
		}
		return nil
	}

	// Sync mode: execute handlers sequentially
	for _, handler := range handlers {
		if err := handler(ctx, event); err != nil {
			return fmt.Errorf("event handler failed for %s: %w", event.Type, err)
		}
	}

	return nil
}

// EmitAndWait publishes an event and waits for all handlers to complete
// This method works in both sync and async mode
// Automatically generates an ID for the event if not provided (from source)
func (eb *EventBus) EmitAndWait(ctx context.Context, event Event) error {
	// Auto-generate ID if not provided (from source)
	if event.ID == "" {
		event.ID = uuid.New().String()
	}

	eb.mu.RLock()
	handlers, exists := eb.handlers[event.Type]
	eb.mu.RUnlock()

	if !exists || len(handlers) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	errChan := make(chan error, len(handlers))

	for _, handler := range handlers {
		wg.Add(1)
		h := handler // capture loop variable

		go func() {
			defer wg.Done()
			if err := h(ctx, event); err != nil {
				errChan <- err
			}
		}()
	}

	wg.Wait()
	close(errChan)

	// Collect errors
	for err := range errChan {
		if err != nil {
			return fmt.Errorf("event handler failed for %s: %w", event.Type, err)
		}
	}

	return nil
}

// HasHandlers checks if there are any handlers registered for an event type
func (eb *EventBus) HasHandlers(eventType EventType) bool {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	handlers, exists := eb.handlers[eventType]
	return exists && len(handlers) > 0
}

// GetHandlerCount returns the number of handlers for a specific event type
func (eb *EventBus) GetHandlerCount(eventType EventType) int {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	if handlers, exists := eb.handlers[eventType]; exists {
		return len(handlers)
	}
	return 0
}

// Clear removes all event handlers
func (eb *EventBus) Clear() {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	eb.handlers = make(map[EventType][]EventHandler)
}
