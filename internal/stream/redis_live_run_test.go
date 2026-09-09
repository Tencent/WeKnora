package stream

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
)

func newTestRedisStreamManager(t *testing.T, ttl time.Duration) (*RedisStreamManager, *miniredis.Miniredis) {
	t.Helper()
	mini := miniredis.RunT(t)
	mgr, err := NewRedisStreamManager(mini.Addr(), "", "", 0, "test", ttl)
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr, mini
}

// A corrupt live-run marker used to decode as "no run is live". Steer then
// answered new_run and the client started a second AgentQA on top of the
// still-generating turn. Lookup failures must surface as errors instead.
func TestGetLiveRunCorruptJSONIsError(t *testing.T) {
	mgr, mini := newTestRedisStreamManager(t, time.Hour)
	ctx := context.Background()

	require.NoError(t, mini.Set(mgr.buildLiveRunKey("sess-1"), "not-json"))

	id, req, err := mgr.GetLiveRun(ctx, "sess-1")
	require.Error(t, err)
	require.Empty(t, id)
	require.Empty(t, req)
}

func TestGetLiveRunMissingKeyIsEmpty(t *testing.T) {
	mgr, _ := newTestRedisStreamManager(t, time.Hour)

	id, req, err := mgr.GetLiveRun(context.Background(), "sess-missing")
	require.NoError(t, err)
	require.Empty(t, id)
	require.Empty(t, req)
}

// The live-run key is written once with the StreamManager TTL. A turn that
// runs longer than that TTL used to look idle, and /steer answered new_run.
// Streaming (AppendEvent / GetEvents) must refresh the marker.
func TestAppendEventRefreshesLiveRunTTL(t *testing.T) {
	ttl := 2 * time.Second
	mgr, mini := newTestRedisStreamManager(t, ttl)
	ctx := context.Background()

	require.NoError(t, mgr.SetLiveRun(ctx, "sess-1", "assist-1", "req-1"))
	mini.FastForward(ttl - 200*time.Millisecond)

	require.NoError(t, mgr.AppendEvent(ctx, "sess-1", "assist-1", interfaces.StreamEvent{
		ID:      "e1",
		Type:    types.ResponseTypeAnswer,
		Content: "chunk",
	}))
	mini.FastForward(ttl - 200*time.Millisecond)

	id, _, err := mgr.GetLiveRun(ctx, "sess-1")
	require.NoError(t, err)
	require.Equal(t, "assist-1", id)
}

func TestGetEventsRefreshesLiveRunTTLWhileWaiting(t *testing.T) {
	ttl := 2 * time.Second
	mgr, mini := newTestRedisStreamManager(t, ttl)
	ctx := context.Background()

	require.NoError(t, mgr.SetLiveRun(ctx, "sess-1", "assist-1", "req-1"))
	mini.FastForward(ttl - 200*time.Millisecond)

	_, _, err := mgr.GetEvents(ctx, "sess-1", "assist-1", 0)
	require.NoError(t, err)
	mini.FastForward(ttl - 200*time.Millisecond)

	id, _, err := mgr.GetLiveRun(ctx, "sess-1")
	require.NoError(t, err)
	require.Equal(t, "assist-1", id)
}

func TestAppendSteerEventsRefreshesLiveRunTTL(t *testing.T) {
	ttl := 2 * time.Second
	mgr, mini := newTestRedisStreamManager(t, ttl)
	ctx := context.Background()

	require.NoError(t, mgr.SetLiveRun(ctx, "sess-1", "assist-1", "req-1"))
	mini.FastForward(ttl - 200*time.Millisecond)

	require.NoError(t, mgr.AppendSteerEvents(ctx, "sess-1", "assist-1", []interfaces.StreamEvent{{
		ID:      "s1",
		Type:    types.ResponseTypeSteer,
		Content: "queued",
		Done:    true,
	}}))
	mini.FastForward(ttl - 200*time.Millisecond)

	id, _, err := mgr.GetLiveRun(ctx, "sess-1")
	require.NoError(t, err)
	require.Equal(t, "assist-1", id)
}

func TestSetLiveRunRejectsADifferentAssistant(t *testing.T) {
	mgr, _ := newTestRedisStreamManager(t, time.Hour)
	ctx := context.Background()

	require.NoError(t, mgr.SetLiveRun(ctx, "sess-1", "assist-1", "req-1"))
	require.ErrorIs(t, mgr.SetLiveRun(ctx, "sess-1", "assist-2", "req-2"), ErrLiveRunExists)

	id, req, err := mgr.GetLiveRun(ctx, "sess-1")
	require.NoError(t, err)
	require.Equal(t, "assist-1", id)
	require.Equal(t, "req-1", req)
}

func TestSetLiveRunIsIdempotentForTheSameAssistant(t *testing.T) {
	mgr, _ := newTestRedisStreamManager(t, time.Hour)
	ctx := context.Background()

	require.NoError(t, mgr.SetLiveRun(ctx, "sess-1", "assist-1", "req-1"))
	require.NoError(t, mgr.SetLiveRun(ctx, "sess-1", "assist-1", "req-1"))
}

func TestClaimLiveRunOverwritesTheMarker(t *testing.T) {
	mgr, _ := newTestRedisStreamManager(t, time.Hour)
	ctx := context.Background()

	require.NoError(t, mgr.SetLiveRun(ctx, "sess-1", "assist-1", "req-1"))
	require.NoError(t, mgr.ClaimLiveRun(ctx, "sess-1", "assist-2", "req-2"))

	id, req, err := mgr.GetLiveRun(ctx, "sess-1")
	require.NoError(t, err)
	require.Equal(t, "assist-2", id)
	require.Equal(t, "req-2", req)
}

func TestMemorySetLiveRunRejectsADifferentAssistant(t *testing.T) {
	mgr := NewMemoryStreamManager()
	ctx := context.Background()

	require.NoError(t, mgr.SetLiveRun(ctx, "sess-1", "assist-1", "req-1"))
	require.ErrorIs(t, mgr.SetLiveRun(ctx, "sess-1", "assist-2", "req-2"), ErrLiveRunExists)
	require.NoError(t, mgr.ClaimLiveRun(ctx, "sess-1", "assist-2", "req-2"))
	id, _, err := mgr.GetLiveRun(ctx, "sess-1")
	require.NoError(t, err)
	require.Equal(t, "assist-2", id)
}
