package event

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// EventBusAdapter adapts *EventBus to types.EventBusInterface
// This allows EventBus to be used through the interface without circular dependencies
type EventBusAdapter struct {
	bus *EventBus
}

// NewEventBusAdapter creates a new adapter for EventBus
func NewEventBusAdapter(bus *EventBus) types.EventBusInterface {
	return &EventBusAdapter{bus: bus}
}

// On registers an event handler for a specific event type
func (a *EventBusAdapter) On(eventType types.EventType, handler types.EventHandler) {
	// Convert types.EventType to event.EventType
	evtType := EventType(eventType)

	// Convert types.EventHandler to event.EventHandler
	evtHandler := func(ctx context.Context, evt Event) error {
		// Convert event.Event to types.Event
		typesEvt := types.Event{
			ID:        evt.ID,
			Type:      types.EventType(evt.Type),
			SessionID: evt.SessionID,
			Data:      evt.Data,
			Metadata:  evt.Metadata,
			RequestID: evt.RequestID,
		}
		return handler(ctx, typesEvt)
	}

	a.bus.On(evtType, evtHandler)
}

// Emit publishes an event to all registered handlers
func (a *EventBusAdapter) Emit(ctx context.Context, evt types.Event) error {
	// Convert types.Event to event.Event
	eventEvt := Event{
		ID:        evt.ID,
		Type:      EventType(evt.Type),
		SessionID: evt.SessionID,
		Data:      evt.Data,
		Metadata:  evt.Metadata,
		RequestID: evt.RequestID,
	}
	return a.bus.Emit(ctx, eventEvt)
}

// AsEventBusInterface converts *EventBus to types.EventBusInterface
func (eb *EventBus) AsEventBusInterface() types.EventBusInterface {
	return NewEventBusAdapter(eb)
}
