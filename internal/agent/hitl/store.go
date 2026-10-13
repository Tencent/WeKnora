package hitl

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Store persists prompts. Production wiring passes the GORM repository; the
// in-memory store keeps single-process callers and tests on the same code path.
type Store = interfaces.AgentPromptRepository

// memoryStore is a process-local Store. It cannot coordinate replicas.
type memoryStore struct {
	mu   sync.Mutex
	rows map[string]types.AgentPrompt
}

// NewMemoryStore returns a process-local Store.
func NewMemoryStore() Store {
	return &memoryStore{rows: make(map[string]types.AgentPrompt)}
}

func (s *memoryStore) Create(_ context.Context, row *types.AgentPrompt) error {
	if row == nil || row.ID == "" {
		return errors.New("agent prompt row requires an id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.rows[row.ID]; exists {
		return errors.New("agent prompt already exists")
	}
	now := time.Now()
	stored := *row
	stored.CreatedAt, stored.UpdatedAt = now, now
	s.rows[row.ID] = stored
	return nil
}

func (s *memoryStore) Get(_ context.Context, id string) (*types.AgentPrompt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return nil, nil
	}
	return &row, nil
}

func (s *memoryStore) Transition(_ context.Context, id string, t types.AgentPromptTransition) (bool, error) {
	if len(t.From) == 0 || t.To == "" {
		return false, errors.New("transition requires from and to states")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok || !slices.Contains(t.From, row.Status) {
		return false, nil
	}
	row.Status = t.To
	row.UpdatedAt = time.Now()
	if len(t.Response) > 0 {
		row.Response = t.Response
	}
	if t.CloseReason != nil {
		row.CloseReason = *t.CloseReason
	}
	if t.ResolvedBy != nil {
		row.ResolvedBy = *t.ResolvedBy
	}
	if t.ResolvedAt != nil {
		at := *t.ResolvedAt
		row.ResolvedAt = &at
	}
	if t.ExpiresAt != nil {
		at := *t.ExpiresAt
		row.ExpiresAt = &at
	}
	s.rows[id] = row
	return true, nil
}

func (s *memoryStore) RenewLease(_ context.Context, id, owner string, until time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok || row.Status != types.AgentPromptPending || row.OwnerInstance != owner {
		return false, nil
	}
	row.LeaseUntil = &until
	row.UpdatedAt = time.Now()
	s.rows[id] = row
	return true, nil
}
