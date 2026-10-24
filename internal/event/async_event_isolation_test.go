package event

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Async delivery runs one goroutine per handler against the same Event value.
// Handlers are free to annotate the event they receive — the package's own
// WithTiming middleware writes event.Metadata["duration_ms"] — so two
// annotated handlers on one async Emit raced on the shared metadata map. A
// concurrent map write is a fatal, unrecoverable runtime panic; the
// per-handler recover() cannot catch it.
func TestAsyncEmitIsolatesHandlerMetadataMutations(t *testing.T) {
	bus := NewAsyncEventBus()

	original := Event{
		Type:     EventQueryReceived,
		Metadata: map[string]interface{}{"origin": "test"},
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	saw := make(map[string]bool)

	record := func(tag string) EventHandler {
		return func(ctx context.Context, e Event) error {
			defer wg.Done()
			e.Metadata["handler"] = tag
			mu.Lock()
			saw[tag] = true
			mu.Unlock()
			return nil
		}
	}

	wg.Add(3)
	bus.On(EventQueryReceived, WithTiming()(record("timed-a")))
	bus.On(EventQueryReceived, WithTiming()(record("timed-b")))
	bus.On(EventQueryReceived, func(ctx context.Context, e Event) error {
		defer wg.Done()
		mu.Lock()
		defer mu.Unlock()
		// The original metadata must reach every handler untouched.
		assert.Equal(t, "test", e.Metadata["origin"])
		return nil
	})

	err := bus.Emit(context.Background(), original)
	require.NoError(t, err)
	wg.Wait()
	assert.Len(t, saw, 2, "both annotated handlers must run")
	// Handler-side annotations must not reach the caller's event: async
	// delivery hands each handler its own copy.
	assert.NotContains(t, original.Metadata, "handler")
	assert.NotContains(t, original.Metadata, "duration_ms")
}

// EmitAndWait dispatches handlers on goroutines too, with the same shared
// Event value.
func TestEmitAndWaitIsolatesHandlerMetadataMutations(t *testing.T) {
	bus := NewEventBus()

	original := Event{
		Type:     EventQueryReceived,
		Metadata: map[string]interface{}{"origin": "test"},
	}

	var wg sync.WaitGroup
	wg.Add(2)
	bus.On(EventQueryReceived, WithTiming()(func(ctx context.Context, e Event) error {
		defer wg.Done()
		e.Metadata["handler"] = "a"
		return nil
	}))
	bus.On(EventQueryReceived, WithTiming()(func(ctx context.Context, e Event) error {
		defer wg.Done()
		e.Metadata["handler"] = "b"
		return nil
	}))

	err := bus.EmitAndWait(context.Background(), original)
	require.NoError(t, err)
	wg.Wait()
	assert.NotContains(t, original.Metadata, "handler")
	assert.NotContains(t, original.Metadata, "duration_ms")
}
