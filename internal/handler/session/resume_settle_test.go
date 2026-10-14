package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type settleMessages struct {
	interfaces.MessageService
	mu  sync.Mutex
	msg types.Message
}

func (s *settleMessages) GetMessage(context.Context, string, string) (*types.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.msg
	return &m, nil
}

func (s *settleMessages) set(f func(*types.Message)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.msg)
}

func shortResumeSettle(t *testing.T, timeout time.Duration) {
	prevTimeout, prevInterval := resumeSettleTimeout, resumeSettleInterval
	resumeSettleTimeout, resumeSettleInterval = timeout, 10*time.Millisecond
	t.Cleanup(func() { resumeSettleTimeout, resumeSettleInterval = prevTimeout, prevInterval })
}

var settlePrompt = &types.AgentPrompt{ID: "p1", SessionID: "s", AssistantMessageID: "m"}

// An answer that lands while the suspending run is still tearing down waits
// for it to persist the message and release the session.
func TestResumeWaitsForSuspendingRunToFinish(t *testing.T) {
	shortResumeSettle(t, 5*time.Second)
	mgr := stream.NewMemoryStreamManager()
	require.NoError(t, mgr.SetLiveRun(t.Context(), "s", "m", "req"))
	messages := &settleMessages{msg: types.Message{ID: "m", SessionID: "s"}}
	h := &Handler{messageService: messages, streamManager: mgr}

	go func() {
		time.Sleep(50 * time.Millisecond)
		messages.set(func(m *types.Message) { m.RunState = types.MessageRunStateAwaitingInput })
		time.Sleep(50 * time.Millisecond)
		_ = mgr.ClearLiveRun(context.Background(), "s", "m")
	}()

	start := time.Now()
	require.NoError(t, h.awaitSuspendedRunSettled(t.Context(), settlePrompt, false, nil))
	require.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond)
}

func TestResumeClearsMarkerLeftByVanishedOwner(t *testing.T) {
	shortResumeSettle(t, 5*time.Second)
	mgr := stream.NewMemoryStreamManager()
	require.NoError(t, mgr.SetLiveRun(t.Context(), "s", "m", "req"))
	messages := &settleMessages{msg: types.Message{
		ID: "m", SessionID: "s", RunState: types.MessageRunStateAwaitingInput,
	}}
	h := &Handler{messageService: messages, streamManager: mgr}

	require.NoError(t, h.awaitSuspendedRunSettled(t.Context(), settlePrompt, true, nil))
	live, _, err := mgr.GetLiveRun(t.Context(), "s")
	require.NoError(t, err)
	require.Empty(t, live)
}

func TestResumeGivesUpOnMessageThatNeverSettles(t *testing.T) {
	shortResumeSettle(t, 60*time.Millisecond)
	messages := &settleMessages{msg: types.Message{ID: "m", SessionID: "s"}}
	h := &Handler{messageService: messages, streamManager: stream.NewMemoryStreamManager()}
	require.Error(t, h.awaitSuspendedRunSettled(t.Context(), settlePrompt, false, nil))

	messages.set(func(m *types.Message) { m.IsCompleted = true })
	require.Error(t, h.awaitSuspendedRunSettled(t.Context(), settlePrompt, true, nil))
}
