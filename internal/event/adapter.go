// Package event provides related functionality.
package event

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// BusAdapter adapts *Bus to types.BusInterface
// This allows Bus to be used through the interface without circular dependencies
type BusAdapter struct {
	bus *Bus
}

// NewBusAdapter creates a new adapter for Bus
func NewBusAdapter(bus *Bus) types.BusInterface {
	return &BusAdapter{bus: bus}
}

// On registers an event handler for a specific event type
func (a *BusAdapter) On(eventType types.Type, handler types.Handler) {
	// Convert types.Type to event.Type
	evtType := Type(eventType)

	// Convert types.Handler to event.Handler
	evtHandler := func(ctx context.Context, evt Event) error {
		// Convert event.Event to types.Event
		typesEvt := types.Event{
			ID:        evt.ID,
			Type:      types.Type(evt.Type),
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
func (a *BusAdapter) Emit(ctx context.Context, evt types.Event) error {
	// Convert types.Event to event.Event
	eventEvt := Event{
		ID:        evt.ID,
		Type:      Type(evt.Type),
		SessionID: evt.SessionID,
		Data:      evt.Data,
		Metadata:  evt.Metadata,
		RequestID: evt.RequestID,
	}
	return a.bus.Emit(ctx, eventEvt)
}

// AsBusInterface converts *Bus to types.BusInterface
func (eb *Bus) AsBusInterface() types.BusInterface {
	return NewBusAdapter(eb)
}
