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
	lastUpdated time.Time
	mu          sync.RWMutex
}

// MemoryStreamManager implements StreamManager using in-memory storage
type MemoryStreamManager struct {
	// Map: sessionID -> messageID -> stream data
	streams map[string]map[string]*memoryStreamData
	mu      sync.RWMutex
}

// NewMemoryStreamManager creates a new in-memory stream manager
func NewMemoryStreamManager() *MemoryStreamManager {
	return &MemoryStreamManager{
		streams: make(map[string]map[string]*memoryStreamData),
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
	_ context.Context,
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
	_ context.Context,
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

// Ensure MemoryStreamManager implements StreamManager interface
var _ interfaces.StreamManager = (*MemoryStreamManager)(nil)
