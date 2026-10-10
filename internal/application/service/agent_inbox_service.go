package service

import (
	"context"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// inboxRetentionSweepInterval is how often the background sweeper runs.
const inboxRetentionSweepInterval = 1 * time.Hour

// agentInboxService is the production AgentInboxService: policy-gated
// ingestion plus the query surface behind the check_inbox tool. Ingest
// never fails the caller — an inbox problem must never break the flow
// that produced the event.
type agentInboxService struct {
	repo   interfaces.AgentInboxRepository
	policy *inboxPolicy

	sweepOnce sync.Once
}

// NewAgentInboxService constructs the inbox service. The event policy is
// loaded from config/agent_inbox_policy.yaml when present (code defaults
// otherwise). Subscribes to the parse lifecycle events on the global event
// bus (the same channel handler/session/qa.go emits on) and starts the
// retention sweeper.
func NewAgentInboxService(repo interfaces.AgentInboxRepository) interfaces.AgentInboxService {
	s := &agentInboxService{
		repo:   repo,
		policy: loadInboxPolicy(config.ConfigDir()),
	}
	event.On(event.EventKnowledgeParseCompleted, func(ctx context.Context, e event.Event) error {
		s.ingestParseEvent(ctx, e, types.InboxTypeParseCompleted)
		return nil
	})
	event.On(event.EventKnowledgeParseFailed, func(ctx context.Context, e event.Event) error {
		s.ingestParseEvent(ctx, e, types.InboxTypeParseFailed)
		return nil
	})
	s.StartRetentionSweeper(context.Background())
	return s
}

// ingestParseEvent converts a bus parse-lifecycle event into an inbox entry.
// Handler context has no execution tenant — the event payload carries it.
func (s *agentInboxService) ingestParseEvent(ctx context.Context, e event.Event, inboxType string) {
	data, ok := e.Data.(*event.KnowledgeParseEventData)
	if !ok || data == nil {
		// Tolerate value (non-pointer) publishes.
		if v, ok := e.Data.(event.KnowledgeParseEventData); ok {
			data = &v
		}
	}
	if data == nil || data.KnowledgeID == "" {
		return
	}
	s.Ingest(ctx, &types.AgentInboxEvent{
		TenantID: data.TenantID,
		KBID:     data.KnowledgeBaseID,
		Type:     inboxType,
		Payload: types.JSONMap{
			"knowledge_id": data.KnowledgeID,
			"title":        data.Title,
			"error":        data.Error,
		},
	})
}

// Ingest applies the event policy and persists. Best-effort by design:
// policy rejections and storage errors are logged, never propagated.
func (s *agentInboxService) Ingest(ctx context.Context, evt *types.AgentInboxEvent) {
	if evt == nil || evt.Type == "" {
		return
	}
	entry, ok := s.policy.lookup(evt.Type)
	if !ok {
		// Default deny — the type is not in the policy or disabled.
		return
	}
	now := time.Now()
	if evt.CreatedAt.IsZero() {
		evt.CreatedAt = now
	}
	if evt.Priority == "" {
		evt.Priority = entry.Priority
	}
	// Debounce collapses repeat view/tab events (same type + dedup key)
	// within the policy window so browsing does not flood the inbox.
	if dedup, _ := evt.Payload["dedup_key"].(string); dedup != "" {
		if !s.policy.acceptDebounced(evt.Type, dedup, now) {
			return
		}
	}
	if err := s.repo.Insert(ctx, evt); err != nil {
		logger.Warnf(ctx, "agent inbox: insert %s event: %v", evt.Type, err)
	}
}

// Check returns the digest of matching events and optionally marks them read.
func (s *agentInboxService) Check(
	ctx context.Context, f types.InboxQueryFilters, markRead bool,
) ([]types.InboxDigest, int64, error) {
	rows, err := s.repo.List(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	digests := make([]types.InboxDigest, 0, len(rows))
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		digests = append(digests, types.InboxDigest{
			ID:       row.ID,
			Type:     row.Type,
			Priority: row.Priority,
			Time:     row.CreatedAt.Format(time.RFC3339),
			Payload:  row.Payload,
		})
		ids = append(ids, row.ID)
	}
	if markRead && len(ids) > 0 {
		if err := s.repo.MarkRead(ctx, f.TenantID, ids, time.Now()); err != nil {
			logger.Warnf(ctx, "agent inbox: mark read %d events: %v", len(ids), err)
		}
	}
	remaining, err := s.repo.CountUnread(ctx, f)
	if err != nil {
		logger.Warnf(ctx, "agent inbox: count unread: %v", err)
		remaining = 0
	}
	return digests, remaining, nil
}

// LatestView returns the newest user.viewed_page event in scope — the
// "current page" pointer for wiki_get_current_page.
func (s *agentInboxService) LatestView(
	ctx context.Context, f types.InboxQueryFilters,
) (*types.AgentInboxEvent, error) {
	f.Types = []string{types.InboxTypeUserViewedPage}
	f.Limit = 1
	rows, err := s.repo.List(ctx, f)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// Trail returns the most recent view events, oldest last, so the agent can
// resolve references like "the page I was on before".
func (s *agentInboxService) Trail(
	ctx context.Context, f types.InboxQueryFilters, limit int,
) ([]*types.AgentInboxEvent, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	f.Types = []string{types.InboxTypeUserViewedPage}
	f.Limit = limit
	rows, err := s.repo.List(ctx, f)
	if err != nil {
		return nil, err
	}
	// Reverse to chronological order for the agent.
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows, nil
}

// StartRetentionSweeper launches the hourly cleanup loop (once).
func (s *agentInboxService) StartRetentionSweeper(ctx context.Context) {
	s.sweepOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(inboxRetentionSweepInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.sweepOncePerType(ctx)
				}
			}
		}()
	})
}

// sweepOncePerType deletes expired rows per event-type retention.
func (s *agentInboxService) sweepOncePerType(ctx context.Context) {
	now := time.Now()
	seen := map[time.Duration]bool{}
	s.policy.mu.RLock()
	cutoffs := make([]time.Time, 0, len(s.policy.entries))
	for _, entry := range s.policy.entries {
		if !seen[entry.Retention] {
			seen[entry.Retention] = true
			cutoffs = append(cutoffs, now.Add(-entry.Retention))
		}
	}
	s.policy.mu.RUnlock()

	for _, cutoff := range cutoffs {
		deleted, err := s.repo.DeleteOlderThan(ctx, cutoff)
		if err != nil {
			logger.Warnf(ctx, "agent inbox: retention sweep (cutoff %s): %v", cutoff.Format(time.RFC3339), err)
			continue
		}
		if deleted > 0 {
			logger.Debugf(ctx, "agent inbox: retention sweep removed %d events", deleted)
		}
	}
}
