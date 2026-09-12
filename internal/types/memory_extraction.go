package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrMemoryConflict = errors.New("memory changed; reload before applying this proposal")
var ErrMemoryExtractionLeaseLost = errors.New("memory extraction lease lost")

// MemoryMessageCursor breaks timestamp ties using the message primary key.
type MemoryMessageCursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

func (c MemoryMessageCursor) After(other MemoryMessageCursor) bool {
	return c.At.After(other.At) || (c.At.Equal(other.At) && c.ID > other.ID)
}

type MemoryExtractionSession struct {
	SessionID string              `json:"-"`
	Revision  uint64              `json:"revision"`
	Cursor    MemoryMessageCursor `json:"cursor"`
}

// Progress survives claiming, truncation, task retries and worker restarts.
// A subject-wide timestamp is retained only as a legacy diagnostic field;
// it must never skip messages from another, slower conversation.
type MemoryExtractionState struct {
	Sessions   map[string]MemoryExtractionSession `json:"sessions"`
	LeaseID    string                             `json:"lease_id,omitempty"`
	LeaseUntil time.Time                          `json:"lease_until,omitempty"`
}

func (s MemoryExtractionState) Value() (driver.Value, error) { return json.Marshal(s) }

func (s *MemoryExtractionState) Scan(value interface{}) error {
	*s = MemoryExtractionState{}
	var raw []byte
	switch v := value.(type) {
	case nil:
		return nil
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("memory extraction state: unsupported value %T", value)
	}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, s)
}

type MemoryExtractionBatch struct {
	// RetryAt keeps a redelivered task alive while a crashed worker's lease expires.
	RetryAt  time.Time
	Sessions []MemoryExtractionSession
}
