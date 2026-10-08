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

// MemoryExtractionSession is a small indexed row, not an entry in a growing
// subject JSON document. Completed cursors prevent historical re-extraction.
type MemoryExtractionSession struct {
	TenantID     uint64              `json:"-" gorm:"primaryKey;autoIncrement:false"`
	SubjectID    string              `json:"-" gorm:"primaryKey;type:varchar(512)"`
	SessionID    string              `json:"-" gorm:"primaryKey;type:varchar(36)"`
	Revision     uint64              `json:"revision" gorm:"not null;default:0"`
	Cursor       MemoryMessageCursor `json:"cursor" gorm:"embedded;embeddedPrefix:cursor_"`
	Pending      bool                `json:"-" gorm:"not null;default:false"`
	FailureCount int                 `json:"-" gorm:"not null;default:0"`
	FailureCode  string              `json:"-" gorm:"type:varchar(64);not null;default:''"`
	FailedFrom   MemoryMessageCursor `json:"-" gorm:"embedded;embeddedPrefix:failed_from_"`
	FailedTo     MemoryMessageCursor `json:"-" gorm:"embedded;embeddedPrefix:failed_to_"`
	FailedAt     *time.Time          `json:"-"`
	UpdatedAt    time.Time           `json:"-"`
}

// TableName selects the indexed session progress table.
func (MemoryExtractionSession) TableName() string { return "memory_extraction_sessions" }

// MemoryExtractionState contains only the subject-wide worker lease.
// Cursors and queued work live in MemoryExtractionSession.
type MemoryExtractionState struct {
	LeaseID    string    `json:"lease_id,omitempty"`
	LeaseUntil time.Time `json:"lease_until,omitempty"`
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
