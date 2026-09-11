package outline

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

type version struct {
	CollectionID string `json:"collection_id"`
	Fingerprint  string `json:"fingerprint"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

type run struct {
	ID        string            `json:"id"`
	ForceFull bool              `json:"force_full"`
	Completed map[string]bool   `json:"completed"`
	Seen      map[string]bool   `json:"seen"`
	Applied   map[string]string `json:"applied"`
}

type state struct {
	TaskRunID        string             `json:"task_run_id,omitempty"`
	Metrics          map[string]int64   `json:"metrics,omitempty"`
	Version          int                `json:"version"`
	Instance         identity           `json:"instance"`
	Selected         []string           `json:"selected_collection_ids"`
	Documents        map[string]version `json:"documents"`
	PendingUpserts   map[string]string  `json:"pending_upserts"`
	PendingDeletions map[string]string  `json:"pending_deletions"`
	Run              *run               `json:"run,omitempty"`
	LastComplete     time.Time          `json:"last_complete_scan"`
}

func newRun(full bool) *run {
	return &run{ID: uuid.NewString(), ForceFull: full, Completed: map[string]bool{}, Seen: map[string]bool{}, Applied: map[string]string{}}
}

func parseCursor(cursor *types.SyncCursor) (*state, error) {
	s := &state{Version: 1, Documents: map[string]version{}, PendingUpserts: map[string]string{}, PendingDeletions: map[string]string{}}
	if cursor == nil {
		return s, nil
	}
	raw, err := json.Marshal(cursor.ConnectorCursor)
	if err != nil || json.Unmarshal(raw, s) != nil || s.Version != 1 ||
		s.Documents == nil || s.PendingUpserts == nil || s.PendingDeletions == nil {
		return nil, errors.New("outline_cursor_invalid")
	}
	// Reject a foreign cursor instead of silently interpreting it as an empty baseline.
	for _, field := range []string{"version", "documents", "pending_upserts", "pending_deletions", "instance"} {
		if _, ok := cursor.ConnectorCursor[field]; !ok {
			return nil, errors.New("outline_cursor_invalid")
		}
	}
	if s.Run != nil && (s.Run.Completed == nil || s.Run.Seen == nil || s.Run.Applied == nil) {
		return nil, errors.New("outline_cursor_invalid")
	}
	return s, nil
}

func (s *state) cursor() *types.SyncCursor {
	raw, _ := json.Marshal(s)
	var values map[string]interface{}
	_ = json.Unmarshal(raw, &values)
	return &types.SyncCursor{LastSyncTime: s.LastComplete, ConnectorCursor: values}
}

func (c *Connector) PrepareFullSyncCursor(previous *types.SyncCursor) (*types.SyncCursor, error) {
	s, err := parseCursor(previous)
	if err != nil {
		return nil, err
	}
	s.Run = newRun(true)
	return s.cursor(), nil
}

func (c *Connector) PrepareSyncRunCursor(previous *types.SyncCursor, runID string, full bool) (*types.SyncCursor, error) {
	s, err := parseCursor(previous)
	if err != nil {
		return nil, err
	}
	if s.TaskRunID != runID && full {
		s.Run = newRun(true)
	}
	s.TaskRunID = runID
	return s.cursor(), nil
}
