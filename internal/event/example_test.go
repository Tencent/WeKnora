package event

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Example: Basic usage of event system
func ExampleEventBus_basic() {
	ctx := context.Background()
	bus := NewEventBus()

	// Register a handler
	bus.On(EventQueryReceived, func(ctx context.Context, event Event) error {
		fmt.Printf("Query received: %v\n", event.Data)
		return nil
	})

	// Emit an event
	event := NewEvent(EventQueryReceived, QueryData{
		OriginalQuery: "What is RAG?",
		SessionID:     "session-123",
	})

	_ = bus.Emit(ctx, event)
	// Output: Query received: {What is RAG?  session-123  map[]}
}

// Example: Using middleware
func ExampleEventBus_middleware() {
	ctx := context.Background()
	bus := NewEventBus()

	// Create a handler with middleware
	handler := func(ctx context.Context, event Event) error {
		data := event.Data.(QueryData)
		fmt.Printf("Processing query: %s\n", data.OriginalQuery)
		return nil
	}

	// Apply middleware
	handlerWithMiddleware := ApplyMiddleware(
		handler,
		WithTiming(),
		WithRecovery(),
	)

	bus.On(EventQueryReceived, handlerWithMiddleware)

	event := NewEvent(EventQueryReceived, QueryData{
		OriginalQuery: "What is RAG?",
	})

	_ = bus.Emit(ctx, event)
	// Output: Processing query: What is RAG?
}

// Example: Query processing pipeline with events
func ExampleEventBus_pipeline() {
	ctx := context.Background()
	bus := NewEventBus()

	// Step 1: Query received
	bus.On(EventQueryReceived, func(ctx context.Context, event Event) error {
		data := event.Data.(QueryData)
		fmt.Printf("1. Query received: %s\n", data.OriginalQuery)
		return nil
	})

	// Step 2: Query rewrite
	bus.On(EventQueryRewrite, func(ctx context.Context, event Event) error {
		data := event.Data.(QueryData)
		fmt.Printf("2. Rewriting query: %s\n", data.OriginalQuery)
		return nil
	})

	// Step 3: Retrieval
	bus.On(EventRetrievalStart, func(ctx context.Context, event Event) error {
		data := event.Data.(RetrievalData)
		fmt.Printf("3. Starting retrieval for: %s\n", data.Query)
		return nil
	})

	// Step 4: Rerank
	bus.On(EventRerankStart, func(ctx context.Context, event Event) error {
		data := event.Data.(RerankData)
		fmt.Printf("4. Starting rerank for: %s\n", data.Query)
		return nil
	})

	// Simulate pipeline
	sessionID := "session-123"

	_ = bus.Emit(ctx, NewEvent(EventQueryReceived, QueryData{
		OriginalQuery: "What is RAG?",
		SessionID:     sessionID,
	}))

	_ = bus.Emit(ctx, NewEvent(EventQueryRewrite, QueryData{
		OriginalQuery: "What is RAG?",
		SessionID:     sessionID,
	}))

	_ = bus.Emit(ctx, NewEvent(EventRetrievalStart, RetrievalData{
		Query:           "What is Retrieval Augmented Generation?",
		KnowledgeBaseID: "kb-1",
		TopK:            10,
	}))

	_ = bus.Emit(ctx, NewEvent(EventRerankStart, RerankData{
		Query:       "What is Retrieval Augmented Generation?",
		InputCount:  10,
		OutputCount: 5,
		ModelID:     "rerank-model-1",
	}))

	// Output:
	// 1. Query received: What is RAG?
	// 2. Rewriting query: What is RAG?
	// 3. Starting retrieval for: What is Retrieval Augmented Generation?
	// 4. Starting rerank for: What is Retrieval Augmented Generation?
}

// Test: Multiple handlers for same event
func TestEventBus_MultipleHandlers(t *testing.T) {
	ctx := context.Background()
	bus := NewEventBus()

	counter := 0

	// Register multiple handlers
	bus.On(EventQueryReceived, func(ctx context.Context, event Event) error {
		counter++
		return nil
	})

	bus.On(EventQueryReceived, func(ctx context.Context, event Event) error {
		counter++
		return nil
	})

	bus.On(EventQueryReceived, func(ctx context.Context, event Event) error {
		counter++
		return nil
	})

	// Emit event
	event := NewEvent(EventQueryReceived, QueryData{
		OriginalQuery: "test",
	})

	_ = bus.Emit(ctx, event)

	if counter != 3 {
		t.Errorf("Expected 3 handlers to be called, got %d", counter)
	}
}

// Test: Async event bus
func TestEventBus_Async(t *testing.T) {
	ctx := context.Background()
	bus := NewAsyncEventBus()

	done := make(chan bool, 3)

	// Register handlers
	for i := 0; i < 3; i++ {
		bus.On(EventQueryReceived, func(ctx context.Context, event Event) error {
			time.Sleep(100 * time.Millisecond)
			done <- true
			return nil
		})
	}

	// Emit event
	event := NewEvent(EventQueryReceived, QueryData{
		OriginalQuery: "test",
	})

	_ = bus.Emit(ctx, event)

	// Wait for all handlers
	timeout := time.After(2 * time.Second)
	count := 0

	for count < 3 {
		select {
		case <-done:
			count++
		case <-timeout:
			t.Error("Timeout waiting for async handlers")
			return
		}
	}
}

// Test: EmitAndWait
func TestEventBus_EmitAndWait(t *testing.T) {
	ctx := context.Background()
	bus := NewAsyncEventBus()

	counter := 0

	// Register handlers
	for i := 0; i < 3; i++ {
		bus.On(EventQueryReceived, func(ctx context.Context, event Event) error {
			time.Sleep(50 * time.Millisecond)
			counter++
			return nil
		})
	}

	// Emit and wait
	event := NewEvent(EventQueryReceived, QueryData{
		OriginalQuery: "test",
	})

	err := bus.EmitAndWait(ctx, event)
	if err != nil {
		t.Errorf("EmitAndWait failed: %v", err)
	}

	if counter != 3 {
		t.Errorf("Expected 3 handlers to complete, got %d", counter)
	}
}

// Benchmark: Event emission
func BenchmarkEventBus_Emit(b *testing.B) {
	ctx := context.Background()
	bus := NewEventBus()

	bus.On(EventQueryReceived, func(ctx context.Context, event Event) error {
		return nil
	})

	event := NewEvent(EventQueryReceived, QueryData{
		OriginalQuery: "test",
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bus.Emit(ctx, event)
	}
}
