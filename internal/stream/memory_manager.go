package stream

import (
	"context"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// memoryStreamData holds stream events in memory
type memoryStreamData struct {
	events      []interfaces.StreamEvent
	steerEvents []interfaces.StreamEvent // control-plane events, never surfaced on SSE
	lastUpdated time.Time
	mu          sync.RWMutex
}

// liveRunMarker names the run that is currently generating for a session.
type liveRunMarker struct {
	assistantMessageID string
	requestID          string
}

// MemoryStreamManager implements StreamManager using in-memory storage
type MemoryStreamManager struct {
	// Map: sessionID -> messageID -> stream data
	streams map[string]map[string]*memoryStreamData
	// Map: sessionID -> the run currently generating. Single-process only,
	// which is exactly why the memory backend is not suitable for a
	// multi-replica deployment (see stream.NewStreamManager).
	liveRuns map[string]liveRunMarker
	mu       sync.RWMutex
}

// NewMemoryStreamManager creates a new in-memory stream manager
func NewMemoryStreamManager() *MemoryStreamManager {
	return &MemoryStreamManager{
		streams:  make(map[string]map[string]*memoryStreamData),
		liveRuns: make(map[string]liveRunMarker),
	}
}

// getOrCreateStream gets or creates stream data
func (m *MemoryStreamManager) getOrCreateStream(sessionID, messageID string) *memoryStreamData {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.streams[sessionID]; !exists {
		m.streams[sessionID] = make(map[string]*memoryStreamData)
	}

	if _, exists := m.streams[sessionID][messageID]; !exists {
		m.streams[sessionID][messageID] = &memoryStreamData{
			events:      make([]interfaces.StreamEvent, 0),
			lastUpdated: time.Now(),
		}
	}

	return m.streams[sessionID][messageID]
}

// getStream gets existing stream data (returns nil if not found)
func (m *MemoryStreamManager) getStream(sessionID, messageID string) *memoryStreamData {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if sessionMap, exists := m.streams[sessionID]; exists {
		return sessionMap[messageID]
	}
	return nil
}

// AppendEvent appends a single event to the stream
func (m *MemoryStreamManager) AppendEvent(
	ctx context.Context,
	sessionID, messageID string,
	event interfaces.StreamEvent,
) error {
	stream := m.getOrCreateStream(sessionID, messageID)

	stream.mu.Lock()
	defer stream.mu.Unlock()

	// Set timestamp if not already set
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Append event
	stream.events = append(stream.events, event)
	stream.lastUpdated = time.Now()

	return nil
}

// GetEvents gets events starting from offset
// Returns: events slice, next offset, error
func (m *MemoryStreamManager) GetEvents(
	ctx context.Context,
	sessionID, messageID string,
	fromOffset int,
) ([]interfaces.StreamEvent, int, error) {
	stream := m.getStream(sessionID, messageID)
	if stream == nil {
		// Stream doesn't exist yet
		return []interfaces.StreamEvent{}, fromOffset, nil
	}

	stream.mu.RLock()
	defer stream.mu.RUnlock()

	// Check if offset is beyond current events
	if fromOffset >= len(stream.events) {
		return []interfaces.StreamEvent{}, fromOffset, nil
	}

	// Get events from offset to end
	events := stream.events[fromOffset:]
	nextOffset := len(stream.events)

	// Return copy of events to avoid race conditions
	eventsCopy := make([]interfaces.StreamEvent, len(events))
	copy(eventsCopy, events)

	return eventsCopy, nextOffset, nil
}

// AppendSteerEvents appends control events to the dedicated steer sub-list.
func (m *MemoryStreamManager) AppendSteerEvents(
	_ context.Context,
	sessionID, messageID string,
	events []interfaces.StreamEvent,
) error {
	stream := m.getOrCreateStream(sessionID, messageID)

	stream.mu.Lock()
	defer stream.mu.Unlock()

	for i := range events {
		if events[i].Timestamp.IsZero() {
			events[i].Timestamp = time.Now()
		}
		stream.steerEvents = append(stream.steerEvents, events[i])
	}
	stream.lastUpdated = time.Now()
	return nil
}

// GetSteerEvents drains the steer sub-list starting fromOffset.
func (m *MemoryStreamManager) GetSteerEvents(
	_ context.Context,
	sessionID, messageID string,
	fromOffset int,
) ([]interfaces.StreamEvent, int, error) {
	stream := m.getStream(sessionID, messageID)
	if stream == nil {
		return []interfaces.StreamEvent{}, fromOffset, nil
	}

	stream.mu.RLock()
	defer stream.mu.RUnlock()

	if fromOffset >= len(stream.steerEvents) {
		return []interfaces.StreamEvent{}, fromOffset, nil
	}

	events := stream.steerEvents[fromOffset:]
	nextOffset := len(stream.steerEvents)
	eventsCopy := make([]interfaces.StreamEvent, len(events))
	copy(eventsCopy, events)
	return eventsCopy, nextOffset, nil
}

// UpdateSteerEventData merges keys into a queued steer event's Data map.
func (m *MemoryStreamManager) UpdateSteerEventData(
	_ context.Context,
	sessionID, messageID, eventID string,
	data map[string]interface{},
) (bool, error) {
	stream := m.getStream(sessionID, messageID)
	if stream == nil {
		return false, nil
	}

	stream.mu.Lock()
	defer stream.mu.Unlock()
	for i := range stream.steerEvents {
		if stream.steerEvents[i].ID != eventID {
			continue
		}
		// Copy before mutating: GetSteerEvents hands out a shallow copy of the
		// slice, so callers may still hold the old Data map.
		merged := make(map[string]interface{}, len(stream.steerEvents[i].Data)+len(data))
		for k, v := range stream.steerEvents[i].Data {
			merged[k] = v
		}
		for k, v := range data {
			merged[k] = v
		}
		stream.steerEvents[i].Data = merged
		stream.lastUpdated = time.Now()
		return true, nil
	}
	return false, nil
}

// DeleteSteerEvent removes a queued steer event by ID.
func (m *MemoryStreamManager) DeleteSteerEvent(
	_ context.Context,
	sessionID, messageID, eventID string,
) (bool, error) {
	stream := m.getStream(sessionID, messageID)
	if stream == nil {
		return false, nil
	}

	stream.mu.Lock()
	defer stream.mu.Unlock()
	for i := range stream.steerEvents {
		if stream.steerEvents[i].ID != eventID {
			continue
		}
		if stream.steerEvents[i].Data != nil {
			if consumed, _ := stream.steerEvents[i].Data["consumed"].(bool); consumed {
				return false, nil
			}
		}
		stream.steerEvents = append(stream.steerEvents[:i], stream.steerEvents[i+1:]...)
		stream.lastUpdated = time.Now()
		return true, nil
	}
	return false, nil
}

// SetLiveRun records the session's currently generating assistant message.
func (m *MemoryStreamManager) SetLiveRun(
	_ context.Context,
	sessionID, assistantMessageID, requestID string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.liveRuns[sessionID]; ok && existing.assistantMessageID != assistantMessageID {
		return ErrLiveRunExists
	}
	m.liveRuns[sessionID] = liveRunMarker{
		assistantMessageID: assistantMessageID,
		requestID:          requestID,
	}
	return nil
}

// ClaimLiveRun overwrites the live-run marker for follow-up handoff.
func (m *MemoryStreamManager) ClaimLiveRun(
	_ context.Context,
	sessionID, assistantMessageID, requestID string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.liveRuns[sessionID] = liveRunMarker{
		assistantMessageID: assistantMessageID,
		requestID:          requestID,
	}
	return nil
}

// GetLiveRun returns the session's generating assistant message, if any.
func (m *MemoryStreamManager) GetLiveRun(
	_ context.Context,
	sessionID string,
) (string, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	marker, ok := m.liveRuns[sessionID]
	if !ok {
		return "", "", nil
	}
	return marker.assistantMessageID, marker.requestID, nil
}

// ClearLiveRun drops the marker only when it still points at assistantMessageID.
func (m *MemoryStreamManager) ClearLiveRun(
	_ context.Context,
	sessionID, assistantMessageID string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if marker, ok := m.liveRuns[sessionID]; ok && marker.assistantMessageID == assistantMessageID {
		delete(m.liveRuns, sessionID)
	}
	return nil
}

// Ensure MemoryStreamManager implements StreamManager interface
var _ interfaces.StreamManager = (*MemoryStreamManager)(nil)
