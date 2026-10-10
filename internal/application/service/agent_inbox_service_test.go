package service

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// stubInboxRepo records inserts for policy assertions.
type stubInboxRepo struct {
	inserted []*types.AgentInboxEvent
	rows     []*types.AgentInboxEvent
}

func (r *stubInboxRepo) Insert(_ context.Context, evt *types.AgentInboxEvent) error {
	r.inserted = append(r.inserted, evt)
	r.rows = append(r.rows, evt)
	return nil
}

func (r *stubInboxRepo) List(_ context.Context, f types.InboxQueryFilters) ([]*types.AgentInboxEvent, error) {
	out := make([]*types.AgentInboxEvent, 0, len(r.rows))
	for _, row := range r.rows {
		if f.UnreadOnly && row.ReadAt != nil {
			continue
		}
		out = append(out, row)
	}
	// Mirror the production contract: newest first.
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > f.Limit && f.Limit > 0 {
		out = out[:f.Limit]
	}
	return out, nil
}

func (r *stubInboxRepo) MarkRead(_ context.Context, _ uint64, ids []string, at time.Time) error {
	for _, row := range r.rows {
		for _, id := range ids {
			if row.ID == id {
				t := at
				row.ReadAt = &t
			}
		}
	}
	return nil
}

func (r *stubInboxRepo) CountUnread(_ context.Context, _ types.InboxQueryFilters) (int64, error) {
	var n int64
	for _, row := range r.rows {
		if row.ReadAt == nil {
			n++
		}
	}
	return n, nil
}

func (r *stubInboxRepo) DeleteOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

func TestInboxPolicyDefaultDeny(t *testing.T) {
	repo := &stubInboxRepo{}
	svc := NewAgentInboxService(repo).(interface {
		Ingest(ctx context.Context, evt *types.AgentInboxEvent)
	}).(*agentInboxService)

	// Configured type passes.
	svc.Ingest(context.Background(), &types.AgentInboxEvent{
		TenantID: 1, KBID: "kb", Type: types.InboxTypeUserUploadedDoc,
		Payload: types.JSONMap{"knowledge_id": "k1"},
	})
	require.Len(t, repo.inserted, 1)
	require.Equal(t, types.InboxPriorityP1, repo.inserted[0].Priority)

	// Unknown type is dropped (default deny).
	svc.Ingest(context.Background(), &types.AgentInboxEvent{
		TenantID: 1, KBID: "kb", Type: "something.unlisted",
	})
	require.Len(t, repo.inserted, 1)
}

func TestInboxViewDebounceCollapsesRepeats(t *testing.T) {
	repo := &stubInboxRepo{}
	svc := NewAgentInboxService(repo).(*agentInboxService)

	mk := func() *types.AgentInboxEvent {
		return &types.AgentInboxEvent{
			TenantID: 1, KBID: "kb", UserID: "u1",
			Type:    types.InboxTypeUserViewedPage,
			Payload: types.JSONMap{"slug": "entity/x", "dedup_key": "entity/x"},
		}
	}
	svc.Ingest(context.Background(), mk())
	svc.Ingest(context.Background(), mk()) // within the 2m window → collapsed
	require.Len(t, repo.inserted, 1)

	// A different slug is a different dedup key → kept.
	other := mk()
	other.Payload = types.JSONMap{"slug": "entity/y", "dedup_key": "entity/y"}
	svc.Ingest(context.Background(), other)
	require.Len(t, repo.inserted, 2)
}

func TestInboxCheckMarksReadAndCountsRemaining(t *testing.T) {
	repo := &stubInboxRepo{}
	svc := NewAgentInboxService(repo).(*agentInboxService)

	svc.Ingest(context.Background(), &types.AgentInboxEvent{
		ID: "e1", TenantID: 1, KBID: "kb", UserID: "u1",
		Type: types.InboxTypeUserUploadedDoc,
	})
	svc.Ingest(context.Background(), &types.AgentInboxEvent{
		ID: "e2", TenantID: 1, KBID: "kb", UserID: "u1",
		Type: types.InboxTypeParseFailed,
	})

	digests, remaining, err := svc.Check(context.Background(), types.InboxQueryFilters{
		TenantID: 1, KBIDs: []string{"kb"}, UserID: "u1",
	}, true)
	require.NoError(t, err)
	require.Len(t, digests, 2)
	require.Equal(t, int64(0), remaining)

	// Marked read → a second check with unread_only sees nothing.
	empty, remaining2, err := svc.Check(context.Background(), types.InboxQueryFilters{
		TenantID: 1, KBIDs: []string{"kb"}, UserID: "u1", UnreadOnly: true,
	}, false)
	require.NoError(t, err)
	require.Len(t, empty, 0)
	require.Equal(t, int64(0), remaining2)
}

func TestInboxLatestViewAndTrail(t *testing.T) {
	repo := &stubInboxRepo{}
	svc := NewAgentInboxService(repo).(*agentInboxService)

	mkView := func(id, slug string, at time.Time) *types.AgentInboxEvent {
		return &types.AgentInboxEvent{
			ID: id, TenantID: 1, KBID: "kb", UserID: "u1",
			Type:      types.InboxTypeUserViewedPage,
			Payload:   types.JSONMap{"slug": slug, "title": slug},
			CreatedAt: at,
		}
	}
	base := time.Now()
	// Direct inserts (bypass debounce) to build a trail.
	for _, e := range []*types.AgentInboxEvent{
		mkView("v1", "a", base.Add(-3*time.Minute)),
		mkView("v2", "b", base.Add(-2*time.Minute)),
		mkView("v3", "c", base.Add(-1*time.Minute)),
	} {
		require.NoError(t, repo.Insert(context.Background(), e))
	}

	view, err := svc.LatestView(context.Background(), types.InboxQueryFilters{TenantID: 1, UserID: "u1"})
	require.NoError(t, err)
	require.NotNil(t, view)
	require.Equal(t, "c", view.Payload["slug"])

	trail, err := svc.Trail(context.Background(), types.InboxQueryFilters{TenantID: 1, UserID: "u1"}, 10)
	require.NoError(t, err)
	require.Len(t, trail, 3)
	// Trail is chronological: oldest first.
	require.Equal(t, "a", trail[0].Payload["slug"])
	require.Equal(t, "c", trail[2].Payload["slug"])
}
