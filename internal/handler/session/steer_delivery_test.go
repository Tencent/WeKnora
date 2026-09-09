package session

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSteerDelivery(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		// Omitted delivery must queue, not interrupt: a client that does not
		// know about the field should never land inside a running turn.
		{"", steerDeliveryAfter, false},
		{"inject", steerDeliveryInject, false},
		{"INJECT", steerDeliveryInject, false},
		{"after", steerDeliveryAfter, false},
		{"After", steerDeliveryAfter, false},
		{"queue", "", true},
		{"now", "", true},
		{"bogus", "", true},
	}
	for _, tc := range cases {
		got, err := parseSteerDelivery(tc.in)
		if tc.wantErr {
			assert.Error(t, err, "in=%q", tc.in)
			continue
		}
		require.NoError(t, err, "in=%q", tc.in)
		assert.Equal(t, tc.want, got, "in=%q", tc.in)
	}
}

func TestSelectSteerBacklogKeepsAfterAndUndrainedInject(t *testing.T) {
	t.Parallel()
	events := []interfaces.StreamEvent{
		steerEvent("a", "inject-a", nil, "web"),
		steerEventWithDelivery("b", "after-b", steerDeliveryAfter),
		steerEvent("c", "inject-c", nil, "web"),
		steerEventWithDelivery("d", "after-d", steerDeliveryAfter),
		steerEvent("e", "inject-e", nil, "web"),
	}
	backlog := selectSteerBacklog(events, map[string]struct{}{"a": {}, "c": {}})
	require.Len(t, backlog, 3)
	assert.Equal(t, "b", backlog[0].ID)
	assert.Equal(t, "d", backlog[1].ID)
	assert.Equal(t, "e", backlog[2].ID)
}

func TestSelectSteerBacklogNothingWhenAllInjectDrainedAndNoAfter(t *testing.T) {
	t.Parallel()
	events := []interfaces.StreamEvent{
		steerEvent("a", "inject-a", nil, "web"),
		steerEvent("b", "inject-b", nil, "web"),
	}
	assert.Empty(t, selectSteerBacklog(events, map[string]struct{}{"a": {}, "b": {}}))
}

func TestSelectSteerBacklogAllWhenNeverDrained(t *testing.T) {
	t.Parallel()
	events := []interfaces.StreamEvent{
		steerEvent("a", "inject-a", nil, "web"),
		steerEventWithDelivery("b", "after-b", steerDeliveryAfter),
	}
	backlog := selectSteerBacklog(events, nil)
	require.Len(t, backlog, 2)
	assert.Equal(t, "a", backlog[0].ID)
	assert.Equal(t, "b", backlog[1].ID)
}

func TestSelectSteerBacklogKeepsPromotedAfterUntilInjected(t *testing.T) {
	t.Parallel()
	events := []interfaces.StreamEvent{
		steerEventWithDelivery("b", "after-b", steerDeliveryInject),
	}
	backlog := selectSteerBacklog(events, map[string]struct{}{})
	require.Len(t, backlog, 1)
	assert.Equal(t, "b", backlog[0].ID)
	assert.Empty(t, selectSteerBacklog(events, map[string]struct{}{"b": {}}))
}

func TestPollSteerSkipsAfterDeliveryAndAdvancesOffset(t *testing.T) {
	mgr := stream.NewMemoryStreamManager()
	ctx := context.Background()
	require.NoError(t, mgr.AppendSteerEvents(ctx, "sess", "assist", []interfaces.StreamEvent{
		steerEvent("a", "inject-a", nil, "web"),
		steerEventWithDelivery("b", "after-b", steerDeliveryAfter),
		steerEvent("c", "inject-c", nil, "web"),
	}))

	sink := newSteerSink(ctx, "sess", "req", &types.Message{ID: "assist"}, nil, mgr)
	events, next, err := sink.PollSteer(ctx, "sess", "assist", 0)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "a", events[0]["id"])
	assert.Equal(t, "c", events[1]["id"])
	assert.Equal(t, 3, next)
	assert.Equal(t, 3, sink.DrainedOffset())

	events, next, err = sink.PollSteer(ctx, "sess", "assist", next)
	require.NoError(t, err)
	assert.Empty(t, events)
	assert.Equal(t, 3, next)
}

func TestPollSteerPicksUpAfterPromotedToInject(t *testing.T) {
	mgr := stream.NewMemoryStreamManager()
	ctx := context.Background()
	require.NoError(t, mgr.AppendSteerEvents(ctx, "sess", "assist", []interfaces.StreamEvent{
		steerEventWithDelivery("b", "wait-then-nudge", steerDeliveryAfter),
	}))

	sink := newSteerSink(ctx, "sess", "req", &types.Message{ID: "assist"}, nil, mgr)
	events, _, err := sink.PollSteer(ctx, "sess", "assist", 0)
	require.NoError(t, err)
	assert.Empty(t, events)

	ok, err := promoteSteerForTest(ctx, mgr, "b")
	require.NoError(t, err)
	assert.True(t, ok)

	events, _, err = sink.PollSteer(ctx, "sess", "assist", sink.DrainedOffset())
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "b", events[0]["id"])
	assert.Equal(t, "wait-then-nudge", events[0]["content"])
}

func TestSetSteerDeliveryUpdatesExistingAfterEvent(t *testing.T) {
	mgr := stream.NewMemoryStreamManager()
	ctx := context.Background()
	require.NoError(t, mgr.AppendSteerEvents(ctx, "sess", "assist", []interfaces.StreamEvent{
		steerEventWithDelivery("keep", "later", steerDeliveryAfter),
		steerEventWithDelivery("flip", "now", steerDeliveryAfter),
	}))

	ok, err := promoteSteerForTest(ctx, mgr, "flip")
	require.NoError(t, err)
	assert.True(t, ok)

	events, _, err := mgr.GetSteerEvents(ctx, "sess", "assist", 0)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, steerDeliveryAfter, events[0].Data["delivery"])
	assert.Equal(t, steerDeliveryInject, events[1].Data["delivery"])

	ok, err = promoteSteerForTest(ctx, mgr, "missing")
	require.NoError(t, err)
	assert.False(t, ok)
}

// TestPollSteerMarksConsumedDurably pins the cross-replica contract: once the
// engine has taken a message, the flag lives on the event itself. Anything
// that reads the queue afterwards — the overlay, the depth guard, a follow-up
// handoff running in another process — must see it as gone.
func TestPollSteerMarksConsumedDurably(t *testing.T) {
	mgr := stream.NewMemoryStreamManager()
	ctx := context.Background()
	require.NoError(t, mgr.AppendSteerEvents(ctx, "sess", "assist", []interfaces.StreamEvent{
		steerEventWithDelivery("a", "do it now", steerDeliveryInject),
		steerEventWithDelivery("b", "and later this", steerDeliveryAfter),
	}))

	msgs := &steerPersistingMessageStub{}
	sink := newSteerSink(ctx, "sess", "req", &types.Message{ID: "assist"}, msgs, mgr)
	events, _, err := sink.PollSteer(ctx, "sess", "assist", 0)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.NotEmpty(t, sink.PersistSteerMessage(ctx, "sess", "assist", "a", "do it now", nil, "web"))

	stored, _, err := mgr.GetSteerEvents(ctx, "sess", "assist", 0)
	require.NoError(t, err)
	require.Len(t, stored, 2)
	assert.True(t, steerEventConsumed(stored[0]), "injected event must be flagged consumed")
	assert.False(t, steerEventConsumed(stored[1]), "queued after-event must stay pending")

	// A fresh sink stands in for another replica: it must not replay the
	// message the first one already handed to the model.
	other := newSteerSink(ctx, "sess", "req", &types.Message{ID: "assist"}, nil, mgr)
	replayed, _, err := other.PollSteer(ctx, "sess", "assist", 0)
	require.NoError(t, err)
	assert.Empty(t, replayed)

	// And it is no longer part of the backlog handed to a follow-up run.
	assert.Len(t, selectSteerBacklog(stored, nil), 1)
}

// TestPollSteerDoesNotMutateAlreadyReadEvents guards the copy-on-write in the
// memory manager: marking consumed must not retroactively edit event copies
// the teardown path already captured for carry-over.
func TestPollSteerDoesNotMutateAlreadyReadEvents(t *testing.T) {
	mgr := stream.NewMemoryStreamManager()
	ctx := context.Background()
	require.NoError(t, mgr.AppendSteerEvents(ctx, "sess", "assist", []interfaces.StreamEvent{
		steerEventWithDelivery("a", "carry me over", steerDeliveryAfter),
	}))

	captured, _, err := mgr.GetSteerEvents(ctx, "sess", "assist", 0)
	require.NoError(t, err)
	require.Len(t, captured, 1)

	ok, err := mgr.UpdateSteerEventData(ctx, "sess", "assist", "a",
		map[string]interface{}{steerDataConsumed: true})
	require.NoError(t, err)
	require.True(t, ok)

	assert.False(t, steerEventConsumed(captured[0]),
		"carry-over copy must stay pending so the follow-up run can inject it")
}

// promoteSteerForTest is the "立即发送" mutation the handler performs.
func promoteSteerForTest(
	ctx context.Context, mgr *stream.MemoryStreamManager, steerID string,
) (bool, error) {
	return mgr.UpdateSteerEventData(ctx, "sess", "assist", steerID,
		map[string]interface{}{"delivery": steerDeliveryInject})
}

func TestDeleteSteerEventRemovesPendingAfter(t *testing.T) {
	mgr := stream.NewMemoryStreamManager()
	ctx := context.Background()
	require.NoError(t, mgr.AppendSteerEvents(ctx, "sess", "assist", []interfaces.StreamEvent{
		steerEventWithDelivery("keep", "later", steerDeliveryAfter),
		steerEventWithDelivery("drop", "gone", steerDeliveryAfter),
	}))

	ok, err := mgr.DeleteSteerEvent(ctx, "sess", "assist", "drop")
	require.NoError(t, err)
	assert.True(t, ok)

	events, _, err := mgr.GetSteerEvents(ctx, "sess", "assist", 0)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "keep", events[0].ID)

	ok, err = mgr.DeleteSteerEvent(ctx, "sess", "assist", "drop")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestSteerEventCarriesDelivery(t *testing.T) {
	t.Parallel()
	evt := steerEventWithDelivery("id-1", "hello", steerDeliveryAfter)
	assert.Equal(t, types.ResponseTypeSteer, evt.Type)
	assert.Equal(t, steerDeliveryAfter, evt.Data["delivery"])
	assert.Equal(t, "hello", evt.Content)
}

func TestPendingSteerQueueItemsOmitsInjectedKeepsAfter(t *testing.T) {
	t.Parallel()
	events := []interfaces.StreamEvent{
		steerEvent("a", "inject-done", nil, "web"),
		steerEventWithDelivery("b", "first after", steerDeliveryAfter),
		steerEventWithDelivery("c", "second after", steerDeliveryAfter),
	}
	items := pendingSteerQueueItems(events, map[string]struct{}{"a": {}})
	require.Len(t, items, 2)
	assert.Equal(t, "b", items[0]["steer_id"])
	assert.Equal(t, "first after", items[0]["content"])
	assert.Equal(t, steerDeliveryAfter, items[0]["delivery"])
	assert.Equal(t, "c", items[1]["steer_id"])
	assert.Equal(t, steerDeliveryAfter, items[1]["delivery"])
}

func TestPendingSteerQueueItemsOmitsConsumedWithoutInMemoryState(t *testing.T) {
	t.Parallel()
	consumed := steerEventWithDelivery("a", "already injected", steerDeliveryInject)
	consumed.Data[steerDataConsumed] = true
	events := []interfaces.StreamEvent{
		consumed,
		steerEventWithDelivery("b", "still waiting", steerDeliveryAfter),
	}
	// nil injectedIDs stands in for a replica that never ran this turn: the
	// durable flag alone has to keep the overlay honest after a refresh.
	items := pendingSteerQueueItems(events, nil)
	require.Len(t, items, 1)
	assert.Equal(t, "b", items[0]["steer_id"])
}

func steerEventWithDelivery(id, query, delivery string) interfaces.StreamEvent {
	evt := steerEvent(id, query, nil, "web")
	evt.Data["delivery"] = delivery
	return evt
}

type steerUpdateFailingManager struct {
	interfaces.StreamManager
}

func (s *steerUpdateFailingManager) UpdateSteerEventData(
	context.Context, string, string, string, map[string]interface{},
) (bool, error) {
	return false, errors.New("cas exhausted")
}

func TestPersistSteerMessageRollsBackRowWhenConsumeFails(t *testing.T) {
	inner := stream.NewMemoryStreamManager()
	ctx := context.Background()
	require.NoError(t, inner.AppendSteerEvents(ctx, "sess", "assist", []interfaces.StreamEvent{
		steerEventWithDelivery("a", "do it now", steerDeliveryInject),
	}))
	msgs := &steerPersistingMessageStub{}
	sink := newSteerSink(ctx, "sess", "req", &types.Message{ID: "assist"}, msgs,
		&steerUpdateFailingManager{StreamManager: inner})
	assert.Empty(t, sink.PersistSteerMessage(ctx, "sess", "assist", "a", "do it now", nil, "web"))
	assert.Empty(t, msgs.byID, "failed consume must delete the user row so a retry cannot duplicate it")
}

func TestPersistSteerMessageIsIdempotentAfterConsume(t *testing.T) {
	mgr := stream.NewMemoryStreamManager()
	ctx := context.Background()
	require.NoError(t, mgr.AppendSteerEvents(ctx, "sess", "assist", []interfaces.StreamEvent{
		steerEventWithDelivery("a", "do it now", steerDeliveryInject),
	}))
	msgs := &steerPersistingMessageStub{}
	sink := newSteerSink(ctx, "sess", "req", &types.Message{ID: "assist"}, msgs, mgr)
	first := sink.PersistSteerMessage(ctx, "sess", "assist", "a", "do it now", nil, "api")
	require.NotEmpty(t, first)
	second := sink.PersistSteerMessage(ctx, "sess", "assist", "a", "do it now", nil, "api")
	assert.Equal(t, first, second)
	assert.Equal(t, 1, msgs.n)
	assert.Equal(t, "api", msgs.byID[first].Channel)
}

func TestDeleteConsumedSteerEventIsNoOp(t *testing.T) {
	mgr := stream.NewMemoryStreamManager()
	ctx := context.Background()
	evt := steerEventWithDelivery("a", "already in the model", steerDeliveryInject)
	evt.Data[steerDataConsumed] = true
	require.NoError(t, mgr.AppendSteerEvents(ctx, "sess", "assist", []interfaces.StreamEvent{evt}))

	ok, err := mgr.DeleteSteerEvent(ctx, "sess", "assist", "a")
	require.NoError(t, err)
	assert.False(t, ok)

	stored, _, err := mgr.GetSteerEvents(ctx, "sess", "assist", 0)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.Equal(t, "a", stored[0].ID)
}
