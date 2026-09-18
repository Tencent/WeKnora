package types

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// ContextCheckpoint is an agent compaction summary persisted on the assistant
// message of the last turn it covers. The next turn's history starts from the
// newest checkpoint instead of from the raw turns it replaces, so a compaction
// is paid for once rather than again on every later turn.
//
// Storing it on the covered turn rather than on the turn that ran the
// compaction keeps the boundary implicit: a fork that copies the turn copies
// the checkpoint with it, and deleting the turn drops the checkpoint too.
type ContextCheckpoint struct {
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
}

// Value implements the driver.Valuer interface for database serialization.
func (c ContextCheckpoint) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface for database deserialization.
func (c *ContextCheckpoint) Scan(value any) error {
	if value == nil {
		*c = ContextCheckpoint{}
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return errors.New("types: cannot scan context checkpoint from unsupported type")
	}
	if len(b) == 0 {
		*c = ContextCheckpoint{}
		return nil
	}
	return json.Unmarshal(b, c)
}

// ContextCheckpointSink persists compaction checkpoints for the agent engine.
// Declared in types for the same reason as SteerSink: the engine and the
// service both need the shape and neither may import the other.
type ContextCheckpointSink interface {
	// SaveContextCheckpoint stores checkpoint on the assistant message
	// turnMessageID, replacing any earlier checkpoint there.
	SaveContextCheckpoint(ctx context.Context, turnMessageID string, checkpoint *ContextCheckpoint) error
}
