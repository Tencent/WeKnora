package event

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/logger"
)

// Type represents the type of event in the system
type Type string

// EventQueryValidated and related constants.
// EventQueryReceived is an exported constant.
const (
	// Query processing events
	EventQueryReceived   Type = "query.received"   // 用户查询到达
	EventQueryValidated  Type = "query.validated"  // 查询验证完成
	EventQueryPreprocess Type = "query.preprocess" // 查询预处理
	EventQueryRewrite    Type = "query.rewrite"    // 查询改写
	EventQueryRewritten  Type = "query.rewritten"  // 查询改写完成

	// EventRetrievalStart is exported.
	// Retrieval events
	EventRetrievalStart    Type = "retrieval.start"    // 检索开始
	EventRetrievalVector   Type = "retrieval.vector"   // 向量检索
	EventRetrievalKeyword  Type = "retrieval.keyword"  // 关键词检索
	EventRetrievalEntity   Type = "retrieval.entity"   // 实体检索
	EventRetrievalComplete Type = "retrieval.complete" // 检索完成

	// EventRerankStart is exported.
	// Rerank events
	EventRerankStart    Type = "rerank.start"    // 排序开始
	EventRerankComplete Type = "rerank.complete" // 排序完成

	// EventMergeStart is exported.
	// Merge events
	EventMergeStart    Type = "merge.start"    // 合并开始
	EventMergeComplete Type = "merge.complete" // 合并完成

	// EventChatStart is exported.
	// Chat completion events
	EventChatStart    Type = "chat.start"    // 聊天生成开始
	EventChatComplete Type = "chat.complete" // 聊天生成完成
	EventChatStream   Type = "chat.stream"   // 聊天流式输出

	// EventAgentQuery is exported.
	// Agent events
	EventAgentQuery    Type = "agent.query"    // Agent 查询开始
	EventAgentPlan     Type = "agent.plan"     // Agent 计划生成
	EventAgentStep     Type = "agent.step"     // Agent 步骤执行
	EventAgentTool     Type = "agent.tool"     // Agent 工具调用
	EventAgentComplete Type = "agent.complete" // Agent 完成

	// EventAgentThought is exported.
	// Agent streaming events (for real-time feedback)
	EventAgentThought     Type = "thought"      // Agent 思考过程
	EventAgentToolCall    Type = "tool_call"    // 工具调用通知
	EventAgentToolResult  Type = "tool_result"  // 工具结果
	EventAgentReflection  Type = "reflection"   // Agent 反思
	EventAgentReferences  Type = "references"   // 知识引用
	EventAgentFinalAnswer Type = "final_answer" // 最终答案

	// EventToolApprovalRequired is exported.
	// MCP tool human approval (issue #1173)
	EventToolApprovalRequired Type = "tool_approval_required"
	EventToolApprovalResolved Type = "tool_approval_resolved"

	// EventMCPOAuthRequired is exported.
	// MCP OAuth in-conversation authorization prompt: emitted when an
	// OAuth-enabled MCP service is invoked but the current user has not
	// authorized it yet. The agent pauses until the user authorizes (or the
	// wait times out / is canceled).
	EventMCPOAuthRequired Type = "mcp_oauth_required"
	EventMCPOAuthResolved Type = "mcp_oauth_resolved"

	// EventError is exported.
	// Error events
	EventError Type = "error" // 错误事件

	// EventSessionTitle is exported.
	// Session events
	EventSessionTitle Type = "session_title" // 会话标题更新

	// EventStop is exported.
	// Control events
	EventStop Type = "stop" // 停止对话生成
)

// Event represents an event in the system
type Event struct {
	ID        string                 // 事件ID (自动生成UUID，用于流式更新追踪)
	Type      Type                   // 事件类型
	SessionID string                 // 会话ID
	Data      interface{}            // 事件数据
	Metadata  map[string]interface{} // 事件元数据
	RequestID string                 // 请求ID
}

// Handler is a function that handles events
type Handler func(ctx context.Context, event Event) error

// Bus manages event publishing and subscription
type Bus struct {
	mu        sync.RWMutex
	handlers  map[Type][]Handler
	asyncMode bool // 是否异步处理事件
}

// NewBus creates a new Bus instance
func NewBus() *Bus {
	return &Bus{
		handlers:  make(map[Type][]Handler),
		asyncMode: false,
	}
}

// NewAsyncBus creates a new Bus with async mode enabled
func NewAsyncBus() *Bus {
	return &Bus{
		handlers:  make(map[Type][]Handler),
		asyncMode: true,
	}
}

// On registers an event handler for a specific event type
// Multiple handlers can be registered for the same event type
func (eb *Bus) On(eventType Type, handler Handler) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	eb.handlers[eventType] = append(eb.handlers[eventType], handler)
}

// Off removes all handlers for a specific event type
func (eb *Bus) Off(eventType Type) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	delete(eb.handlers, eventType)
}

// Emit publishes an event to all registered handlers
// Returns error if any handler fails (in sync mode)
// Automatically generates an ID for the event if not provided (from source)
func (eb *Bus) Emit(ctx context.Context, event Event) error {
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
				defer func() {
					if r := recover(); r != nil {
						logger.Errorf(ctx, "event handler panic recovered (type=%s): %v", event.Type, r)
					}
				}()
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
func (eb *Bus) EmitAndWait(ctx context.Context, event Event) error {
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
			defer func() {
				if r := recover(); r != nil {
					errChan <- fmt.Errorf("event handler panic (type=%s): %v", event.Type, r)
				}
			}()
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
func (eb *Bus) HasHandlers(eventType Type) bool {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	handlers, exists := eb.handlers[eventType]
	return exists && len(handlers) > 0
}

// GetHandlerCount returns the number of handlers for a specific event type
func (eb *Bus) GetHandlerCount(eventType Type) int {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	if handlers, exists := eb.handlers[eventType]; exists {
		return len(handlers)
	}
	return 0
}

// Clear removes all event handlers
func (eb *Bus) Clear() {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	eb.handlers = make(map[Type][]Handler)
}
