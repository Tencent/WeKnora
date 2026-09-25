package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeReaperClient struct {
	summaries []RemoteSandboxSummary
	states    []RemoteSandboxState
	deleted   []string
}

func (f *fakeReaperClient) List(
	_ context.Context, filter RemoteListFilter,
) ([]RemoteSandboxSummary, error) {
	f.states = filter.States
	return f.summaries, nil
}

func (f *fakeReaperClient) Delete(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

// Paused sandboxes are the whole point: onTimeout=pause means the provider TTL
// never reclaims them, so a sweep that only looked at running ones would miss
// every leaked instance.
func TestReapOrphanSandboxesDeletesUnboundIncludingPaused(t *testing.T) {
	now := time.Now()
	client := &fakeReaperClient{summaries: []RemoteSandboxSummary{
		{ID: "sbx-bound", State: RemoteStateRunning, StartedAt: now.Add(-2 * time.Hour)},
		{ID: "sbx-orphan-running", State: RemoteStateRunning, StartedAt: now.Add(-2 * time.Hour)},
		{ID: "sbx-orphan-paused", State: RemoteStatePaused, StartedAt: now.Add(-2 * time.Hour)},
		// Inside the grace window: may still be mid-creation and unbound.
		{ID: "sbx-fresh", State: RemoteStateRunning, StartedAt: now.Add(-1 * time.Minute)},
	}}

	deleted, err := ReapOrphanSandboxes(context.Background(), OrphanReaperDeps{
		Client:   client,
		BoundIDs: map[string]struct{}{"sbx-bound": {}},
		TenantID: 7,
		Grace:    30 * time.Minute,
		Now:      func() time.Time { return now },
	})

	require.NoError(t, err)
	require.Equal(t, 2, deleted)
	require.ElementsMatch(t,
		[]string{"sbx-orphan-running", "sbx-orphan-paused"}, client.deleted)
	require.Contains(t, client.states, RemoteStatePaused,
		"the listing filter must include paused sandboxes")
}

func TestReapOrphanSandboxesKeepsBoundSandboxes(t *testing.T) {
	now := time.Now()
	client := &fakeReaperClient{summaries: []RemoteSandboxSummary{
		{ID: "sbx-a", State: RemoteStatePaused, StartedAt: now.Add(-5 * time.Hour)},
	}}

	deleted, err := ReapOrphanSandboxes(context.Background(), OrphanReaperDeps{
		Client:   client,
		BoundIDs: map[string]struct{}{"sbx-a": {}},
		TenantID: 7,
		Grace:    time.Minute,
		Now:      func() time.Time { return now },
	})

	require.NoError(t, err)
	require.Zero(t, deleted)
	require.Empty(t, client.deleted)
}

// Grace=0 is the backend-switch case: everything on the old backend is known to
// be abandoned, so even a just-created sandbox must be reaped.
func TestReapOrphanSandboxesZeroGraceIgnoresAge(t *testing.T) {
	now := time.Now()
	client := &fakeReaperClient{summaries: []RemoteSandboxSummary{
		{ID: "sbx-fresh", State: RemoteStateRunning, StartedAt: now},
	}}

	deleted, err := ReapOrphanSandboxes(context.Background(), OrphanReaperDeps{
		Client:   client,
		TenantID: 7,
		Grace:    0,
		Now:      func() time.Time { return now },
	})

	require.NoError(t, err)
	require.Equal(t, 1, deleted)
}

// The reaper must not reach across configs, even when both live on the same
// provider account.
func TestReapOrphanSandboxesScopesToConfig(t *testing.T) {
	client := &fakeConfigSandboxClient{inventory: []RemoteSandboxSummary{
		summary("sb-a", "7", "cfg-a"),
		summary("sb-b", "7", "cfg-b"),
	}}

	deleted, err := ReapOrphanSandboxes(context.Background(), OrphanReaperDeps{
		Client:   client,
		TenantID: 7,
		ConfigID: "cfg-a",
		BoundIDs: map[string]struct{}{},
	})
	require.NoError(t, err)
	require.Equal(t, 1, deleted)
	require.Equal(t, []string{"sb-a"}, client.deleted)
}
