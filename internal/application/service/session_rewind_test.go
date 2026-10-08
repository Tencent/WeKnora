package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

const (
	rewindSHA1 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rewindSHA2 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

type fakeRewindSandboxPort struct {
	boundID            string
	bound              bool
	activeTurn         bool
	activeTurnErr      error
	busyAfterCalls     int
	hasActiveTurnCalls int
	runner             *fakeShellRunner
	execBlock          func()
	rewindMu           sync.Mutex
	rewindHeld         bool
}

func newFakeRewindPort() *fakeRewindSandboxPort {
	return &fakeRewindSandboxPort{
		boundID: "sbx-1",
		bound:   true,
		runner:  &fakeShellRunner{result: &sandbox.ExecuteResult{ExitCode: 0}},
	}
}

var _ SessionRewindSandboxPort = (*fakeRewindSandboxPort)(nil)

func (f *fakeRewindSandboxPort) BoundSandboxID(context.Context, string) (string, bool) {
	return f.boundID, f.bound
}

func (f *fakeRewindSandboxPort) HasActiveTurn(context.Context, string) (bool, error) {
	f.hasActiveTurnCalls++
	if f.activeTurnErr != nil {
		return false, f.activeTurnErr
	}
	if f.busyAfterCalls > 0 && f.hasActiveTurnCalls > f.busyAfterCalls {
		return true, nil
	}
	return f.activeTurn, nil
}

func (f *fakeRewindSandboxPort) TryLockRewind(context.Context, string) (func(), error) {
	f.rewindMu.Lock()
	defer f.rewindMu.Unlock()
	if f.rewindHeld {
		return nil, ErrRewindSourceBusy
	}
	f.rewindHeld = true
	return func() {
		f.rewindMu.Lock()
		f.rewindHeld = false
		f.rewindMu.Unlock()
	}, nil
}

func (f *fakeRewindSandboxPort) ExecShellCommand(
	ctx context.Context, sessionID, command, workDir string,
	timeout time.Duration, env map[string]string,
) (*sandbox.ExecuteResult, error) {
	if f.execBlock != nil {
		f.execBlock()
	}
	if f.runner == nil {
		return &sandbox.ExecuteResult{ExitCode: 0}, nil
	}
	return f.runner.ExecShellCommand(ctx, sessionID, command, workDir, timeout, env)
}

type fakeLiveRunReader struct {
	assistantID string
}

func (f *fakeLiveRunReader) GetLiveRun(context.Context, string) (string, string, error) {
	if f == nil {
		return "", "", nil
	}
	return f.assistantID, "req", nil
}

type fakeRewindStream struct {
	fakeLiveRunReader
	dropped []string
}

func (f *fakeRewindStream) DropMessageStreams(_ context.Context, _ string, messageIDs []string) error {
	f.dropped = append(f.dropped, messageIDs...)
	return nil
}

func rewindCompletedTurn(userID, assistantID, sandboxID, sha string, offset time.Duration) []*types.Message {
	turn := checkpointedTurn(userID, assistantID, sandboxID, sha, offset)
	turn[1].IsCompleted = true
	return turn
}

func newRewindFixture(t *testing.T, port SessionRewindSandboxPort, messages []*types.Message) (
	*SessionRewindService, *fakeMessageStore,
) {
	t.Helper()
	sessions := newFakeSessionStore(&types.Session{
		ID: "src", TenantID: 1, UserID: "u1", Title: "原会话", SandboxConfigID: "cfg-1",
	})
	msgs := newFakeMessageStore(messages)
	return NewSessionRewindService(sessions, msgs, port, nil, nil), msgs
}

func TestRewindUserPointDeletesItselfAndResetsToPreviousAssistant(t *testing.T) {
	turn1 := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	later := rewindCompletedTurn("u-2", "a-2", "sbx-1", rewindSHA2, 10*time.Second)
	port := newFakeRewindPort()
	svc, msgs := newRewindFixture(t, port, append(turn1, later...))

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-2")

	require.NoError(t, err)
	require.Equal(t, 2, got.DeletedMessages)
	require.True(t, got.WorkspaceReset)
	require.Empty(t, got.Reason)
	require.NotNil(t, msgs.lastDeleteInclusive)
	require.True(t, *msgs.lastDeleteInclusive)
	require.Equal(t, []string{"u-1", "a-1"}, messageIDs(msgs.messages))
	require.Len(t, port.runner.calls, 1)
	require.Contains(t, port.runner.calls[0], rewindSHA1)
	require.NotContains(t, port.runner.calls[0], rewindSHA2)
}

func TestRewindAssistantPointKeepsItselfAndResetsToItsCheckpoint(t *testing.T) {
	turn1 := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	later := rewindCompletedTurn("u-2", "a-2", "sbx-1", rewindSHA2, 10*time.Second)
	port := newFakeRewindPort()
	svc, msgs := newRewindFixture(t, port, append(turn1, later...))

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "a-1")

	require.NoError(t, err)
	require.Equal(t, 2, got.DeletedMessages)
	require.True(t, got.WorkspaceReset)
	require.Empty(t, got.Reason)
	require.NotNil(t, msgs.lastDeleteInclusive)
	require.False(t, *msgs.lastDeleteInclusive)
	require.Equal(t, []string{"u-1", "a-1"}, messageIDs(msgs.messages))
	require.Len(t, port.runner.calls, 1)
	require.Contains(t, port.runner.calls[0], rewindSHA1)
}

func TestRewindIncompleteAssistantPointIsBusy(t *testing.T) {
	turn := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	turn[1].IsCompleted = false
	port := newFakeRewindPort()
	svc, msgs := newRewindFixture(t, port, turn)

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "a-1")

	require.ErrorIs(t, err, ErrRewindSourceBusy)
	require.Nil(t, got)
	require.Equal(t, 0, msgs.deleteFromCalls)
	require.Empty(t, port.runner.calls)
}

func TestRewindActiveTurnIsBusy(t *testing.T) {
	turn := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	laterUser := &types.Message{
		ID: "u-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := newFakeRewindPort()
	port.activeTurn = true
	svc, msgs := newRewindFixture(t, port, append(turn, laterUser))

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-2")

	require.ErrorIs(t, err, ErrRewindSourceBusy)
	require.Nil(t, got)
	require.Equal(t, 0, msgs.deleteFromCalls)
	require.Empty(t, port.runner.calls)
}

func TestRewindSystemRoleIsRejected(t *testing.T) {
	msg := &types.Message{
		ID: "sys-1", SessionID: "src", Role: "system", CreatedAt: forkBase,
	}
	port := newFakeRewindPort()
	svc, msgs := newRewindFixture(t, port, []*types.Message{msg})

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "sys-1")

	require.ErrorIs(t, err, ErrRewindMessageRole)
	require.Nil(t, got)
	require.Equal(t, 0, msgs.deleteFromCalls)
}

func TestRewindForeignUserIsNotFound(t *testing.T) {
	turn := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	port := newFakeRewindPort()
	svc, msgs := newRewindFixture(t, port, turn)

	got, err := svc.Rewind(context.Background(), 1, "other", "src", "u-1")

	require.ErrorIs(t, err, ErrRewindSessionNotFound)
	require.Nil(t, got)
	require.Equal(t, 0, msgs.deleteFromCalls)
}

func TestRewindNoSandboxDeletesMessagesWithoutShell(t *testing.T) {
	turn := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	laterUser := &types.Message{
		ID: "u-2", SessionID: "src", Role: "user", Content: "retry",
		CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := newFakeRewindPort()
	port.bound = false
	port.boundID = ""
	svc, msgs := newRewindFixture(t, port, append(turn, laterUser))

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-2")

	require.NoError(t, err)
	require.Equal(t, 1, got.DeletedMessages)
	require.False(t, got.WorkspaceReset)
	require.Equal(t, RewindSkipNoSandbox, got.Reason)
	require.Empty(t, port.runner.calls)
	require.Equal(t, []string{"u-1", "a-1"}, messageIDs(msgs.messages))
}

func TestRewindNoCheckpointOnLiveSandboxDoesNotDeleteMessages(t *testing.T) {
	history := []*types.Message{
		{ID: "u-1", SessionID: "src", Role: "user", CreatedAt: forkBase},
		{
			ID: "a-1", SessionID: "src", Role: "assistant", IsCompleted: true,
			CreatedAt: forkBase.Add(time.Second),
		},
		{ID: "u-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second)},
	}
	port := newFakeRewindPort()
	svc, msgs := newRewindFixture(t, port, history)

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-2")

	require.ErrorIs(t, err, ErrRewindNoCheckpoint)
	require.Nil(t, got)
	require.Equal(t, 0, msgs.deleteFromCalls)
	require.Empty(t, port.runner.calls)
	require.Equal(t, []string{"u-1", "a-1", "u-2"}, messageIDs(msgs.messages))
}

func TestRewindReplacedSandboxDoesNotDeleteMessages(t *testing.T) {
	turn := rewindCompletedTurn("u-1", "a-1", "sbx-old", rewindSHA1, 0)
	laterUser := &types.Message{
		ID: "u-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := newFakeRewindPort()
	port.boundID = "sbx-new"
	svc, msgs := newRewindFixture(t, port, append(turn, laterUser))

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-2")

	require.ErrorIs(t, err, ErrRewindSandboxReplaced)
	require.Nil(t, got)
	require.Equal(t, 0, msgs.deleteFromCalls)
	require.Equal(t, []string{"u-1", "a-1", "u-2"}, messageIDs(msgs.messages))
	require.Empty(t, port.runner.calls)
}

func TestRewindResetFailureDoesNotDeleteMessages(t *testing.T) {
	turn := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	laterUser := &types.Message{
		ID: "u-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := newFakeRewindPort()
	port.runner.err = errors.New("sandbox unreachable")
	svc, msgs := newRewindFixture(t, port, append(turn, laterUser))

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-2")

	require.Error(t, err)
	require.Nil(t, got)
	require.Equal(t, 0, msgs.deleteFromCalls)
	require.Equal(t, []string{"u-1", "a-1", "u-2"}, messageIDs(msgs.messages))
	require.True(t, strings.Contains(err.Error(), "sandbox unreachable") ||
		strings.Contains(err.Error(), "workspace reset"))
}

func TestRewindCanceledRequestStillDeletesAfterReset(t *testing.T) {
	turn := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	laterUser := &types.Message{
		ID: "u-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := newFakeRewindPort()
	svc, msgs := newRewindFixture(t, port, append(turn, laterUser))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := svc.Rewind(ctx, 1, "u1", "src", "u-2")

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, 1, got.DeletedMessages)
	require.True(t, got.WorkspaceReset)
	require.Len(t, port.runner.calls, 1)
	require.Equal(t, 1, msgs.deleteFromCalls)
	require.Equal(t, []string{"u-1", "a-1"}, messageIDs(msgs.messages))
}

func TestRewindTurnStartingBeforeResetIsBusy(t *testing.T) {
	turn := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	laterUser := &types.Message{
		ID: "u-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := newFakeRewindPort()
	port.busyAfterCalls = 1
	svc, msgs := newRewindFixture(t, port, append(turn, laterUser))

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-2")

	require.ErrorIs(t, err, ErrRewindSourceBusy)
	require.Nil(t, got)
	require.GreaterOrEqual(t, port.hasActiveTurnCalls, 2)
	require.Empty(t, port.runner.calls)
	require.Equal(t, 0, msgs.deleteFromCalls)
	require.Equal(t, []string{"u-1", "a-1", "u-2"}, messageIDs(msgs.messages))
}

func TestRewindTurnStartingBeforeDeleteWhenSkippingWorkspaceIsBusy(t *testing.T) {
	turn := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	laterUser := &types.Message{
		ID: "u-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := newFakeRewindPort()
	port.bound = false
	port.boundID = ""
	port.busyAfterCalls = 1
	svc, msgs := newRewindFixture(t, port, append(turn, laterUser))

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-2")

	require.ErrorIs(t, err, ErrRewindSourceBusy)
	require.Nil(t, got)
	require.GreaterOrEqual(t, port.hasActiveTurnCalls, 2)
	require.Empty(t, port.runner.calls)
	require.Equal(t, 0, msgs.deleteFromCalls)
	require.Equal(t, []string{"u-1", "a-1", "u-2"}, messageIDs(msgs.messages))
}

func TestRewindRejectsEmptyOwnerSession(t *testing.T) {
	turn := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	port := newFakeRewindPort()
	sessions := newFakeSessionStore(&types.Session{
		ID: "src", TenantID: 1, UserID: "", Title: "租户会话",
	})
	msgs := newFakeMessageStore(turn)
	svc := NewSessionRewindService(sessions, msgs, port, nil, nil)

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-1")

	require.ErrorIs(t, err, ErrRewindSessionNotFound)
	require.Nil(t, got)
	require.Equal(t, 0, msgs.deleteFromCalls)
	require.Empty(t, port.runner.calls)
}

func TestRewindRejectsEmptyCaller(t *testing.T) {
	turn := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	port := newFakeRewindPort()
	svc, msgs := newRewindFixture(t, port, turn)

	got, err := svc.Rewind(context.Background(), 1, "", "src", "u-1")

	require.ErrorIs(t, err, ErrRewindSessionNotFound)
	require.Nil(t, got)
	require.Equal(t, 0, msgs.deleteFromCalls)
	require.Empty(t, port.runner.calls)
}

func TestRewindMessageFromOtherSessionIsNotFound(t *testing.T) {
	turn := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	foreign := &types.Message{
		ID: "foreign", SessionID: "other", Role: "user", CreatedAt: forkBase,
	}
	port := newFakeRewindPort()
	svc, msgs := newRewindFixture(t, port, append(turn, foreign))

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "foreign")

	require.ErrorIs(t, err, ErrRewindMessageNotFound)
	require.Nil(t, got)
	require.Equal(t, 0, msgs.deleteFromCalls)
	require.Empty(t, port.runner.calls)
}

func TestRewindFirstUserMessageClearsConversationWithoutCheckpoint(t *testing.T) {
	history := []*types.Message{
		{ID: "u-1", SessionID: "src", Role: "user", Content: "hello", CreatedAt: forkBase},
		{
			ID: "a-1", SessionID: "src", Role: "assistant", IsCompleted: true,
			CreatedAt: forkBase.Add(time.Second),
			SandboxCheckpoint: &types.SandboxCheckpoint{
				SandboxID: "sbx-1", CommitSHA: rewindSHA1, CommittedAt: forkBase.Add(time.Second),
			},
		},
	}
	port := newFakeRewindPort()
	svc, msgs := newRewindFixture(t, port, history)

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-1")

	require.NoError(t, err)
	require.Equal(t, 2, got.DeletedMessages)
	require.True(t, got.WorkspaceReset)
	require.Empty(t, got.Reason)
	require.Len(t, port.runner.calls, 1)
	require.Contains(t, port.runner.calls[0], "commit-tree")
	require.Contains(t, port.runner.calls[0], "reset --hard")
	require.Contains(t, port.runner.calls[0], "clean -fdx")
	require.NotContains(t, port.runner.calls[0], `find "$WORK_TREE"`)
	require.Empty(t, msgs.messages)
}

func TestRewindWalksToEarlierCheckpointWhenLatestAssistantHasNone(t *testing.T) {
	turn1 := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	later := []*types.Message{
		{ID: "u-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second)},
		{
			ID: "a-2", SessionID: "src", Role: "assistant", IsCompleted: true,
			CreatedAt: forkBase.Add(11 * time.Second),
		},
		{ID: "u-3", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(20 * time.Second)},
	}
	port := newFakeRewindPort()
	svc, msgs := newRewindFixture(t, port, append(turn1, later...))

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-3")

	require.NoError(t, err)
	require.Equal(t, 1, got.DeletedMessages)
	require.True(t, got.WorkspaceReset)
	require.Len(t, port.runner.calls, 1)
	require.Contains(t, port.runner.calls[0], rewindSHA1)
	require.Equal(t, []string{"u-1", "a-1", "u-2", "a-2"}, messageIDs(msgs.messages))
}

func TestRewindLiveRunIsBusy(t *testing.T) {
	turn := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	laterUser := &types.Message{
		ID: "u-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := newFakeRewindPort()
	svc, msgs := newRewindFixture(t, port, append(turn, laterUser))
	svc.liveRuns = &fakeLiveRunReader{assistantID: "a-live"}

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-2")

	require.ErrorIs(t, err, ErrRewindSourceBusy)
	require.Nil(t, got)
	require.Equal(t, 0, msgs.deleteFromCalls)
	require.Empty(t, port.runner.calls)
}

func TestRewindIncompleteTurnPastOldestPageIsBusy(t *testing.T) {
	history := make([]*types.Message, 0, 1001)
	for i := 0; i < 1000; i++ {
		history = append(history, &types.Message{
			ID:        fmt.Sprintf("old-%04d", i),
			SessionID: "src",
			Role:      "user",
			CreatedAt: forkBase.Add(time.Duration(i) * time.Millisecond),
		})
	}
	history = append(history, &types.Message{
		ID: "late-a", SessionID: "src", Role: "assistant", IsCompleted: false,
		CreatedAt: forkBase.Add(time.Second),
	})
	port := newFakeRewindPort()
	svc, msgs := newRewindFixture(t, port, history)

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "old-0000")

	require.ErrorIs(t, err, ErrRewindSourceBusy)
	require.Nil(t, got)
	require.Equal(t, 0, msgs.deleteFromCalls)
	require.Empty(t, port.runner.calls)
}

func TestRewindRetriesDeleteAfterWorkspaceReset(t *testing.T) {
	turn := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	laterUser := &types.Message{
		ID: "u-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := newFakeRewindPort()
	svc, msgs := newRewindFixture(t, port, append(turn, laterUser))
	msgs.deleteFromFailTimes = 2

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-2")

	require.NoError(t, err)
	require.Equal(t, 1, got.DeletedMessages)
	require.True(t, got.WorkspaceReset)
	require.Equal(t, 3, msgs.deleteFromCalls)
	require.Equal(t, []string{"u-1", "a-1"}, messageIDs(msgs.messages))
}

func TestRewindCleansArtifactsAndStreamsForDeletedMessages(t *testing.T) {
	turn := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	later := rewindCompletedTurn("u-2", "a-2", "sbx-1", rewindSHA2, 10*time.Second)
	later[1].Artifacts = types.MessageArtifacts{{URL: "blob://a2", FileName: "out.txt"}}
	port := newFakeRewindPort()
	svc, msgs := newRewindFixture(t, port, append(turn, later...))
	streams := &fakeRewindStream{}
	svc.liveRuns = streams

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "a-1")

	require.NoError(t, err)
	require.Equal(t, 2, got.DeletedMessages)
	require.Equal(t, []types.ArtifactRef{{MessageID: "a-2", Position: 0}}, msgs.tombstoned)
	require.Equal(t, []string{"u-2", "a-2"}, streams.dropped)
}

func TestRewindIncompleteLaterTurnIsBusy(t *testing.T) {
	turn := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	later := []*types.Message{
		{ID: "u-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second)},
		{
			ID: "a-2", SessionID: "src", Role: "assistant", IsCompleted: false,
			CreatedAt: forkBase.Add(11 * time.Second),
		},
	}
	port := newFakeRewindPort()
	svc, msgs := newRewindFixture(t, port, append(turn, later...))

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-1")

	require.ErrorIs(t, err, ErrRewindSourceBusy)
	require.Nil(t, got)
	require.Equal(t, 0, msgs.deleteFromCalls)
	require.Empty(t, port.runner.calls)
}

func pendingForkBootstrap(sha string) *types.ForkBootstrap {
	return &types.ForkBootstrap{
		SnapshotID:      "snap-1",
		CommitSHA:       sha,
		SourceSandboxID: "sbx-1",
		CreatedAt:       forkBase,
	}
}

func newUnopenedForkRewindFixture(
	t *testing.T, messages []*types.Message, bootstrap *types.ForkBootstrap,
) (*SessionRewindService, *fakeSessionStore, *fakeMessageStore, *fakeRewindSandboxPort) {
	t.Helper()
	port := newFakeRewindPort()
	port.bound = false
	port.boundID = ""
	sessions := newFakeSessionStore(&types.Session{
		ID: "src", TenantID: 1, UserID: "u1", Title: "分支",
		SandboxConfigID: "cfg-1", ForkBootstrap: bootstrap,
	})
	msgs := newFakeMessageStore(messages)
	svc := NewSessionRewindService(sessions, msgs, port, nil, nil)
	return svc, sessions, msgs, port
}

func TestRewindUnopenedForkRetargetsBootstrapToKeptCheckpoint(t *testing.T) {
	turn1 := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	later := rewindCompletedTurn("u-2", "a-2", "sbx-1", rewindSHA2, 10*time.Second)
	svc, sessions, msgs, port := newUnopenedForkRewindFixture(
		t, append(turn1, later...), pendingForkBootstrap(rewindSHA2),
	)

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-2")

	require.NoError(t, err)
	require.Equal(t, 2, got.DeletedMessages)
	require.True(t, got.WorkspaceReset)
	require.Empty(t, got.Reason)
	require.Empty(t, port.runner.calls)
	require.Equal(t, []string{"u-1", "a-1"}, messageIDs(msgs.messages))
	require.NotNil(t, sessions.source.ForkBootstrap)
	require.Equal(t, rewindSHA1, sessions.source.ForkBootstrap.CommitSHA)
	require.Equal(t, "snap-1", sessions.source.ForkBootstrap.SnapshotID)
	require.Equal(t, "sbx-1", sessions.source.ForkBootstrap.SourceSandboxID)
	require.False(t, sessions.source.ForkBootstrap.Consumed())
	require.False(t, sessions.bootstrapCleared)
}

func TestRewindUnopenedForkWalksToEarlierCheckpoint(t *testing.T) {
	turn1 := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	later := []*types.Message{
		{ID: "u-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second)},
		{
			ID: "a-2", SessionID: "src", Role: "assistant", IsCompleted: true,
			CreatedAt: forkBase.Add(11 * time.Second),
		},
		{ID: "u-3", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(20 * time.Second)},
	}
	svc, sessions, msgs, port := newUnopenedForkRewindFixture(
		t, append(turn1, later...), pendingForkBootstrap(rewindSHA2),
	)

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-3")

	require.NoError(t, err)
	require.Equal(t, 1, got.DeletedMessages)
	require.True(t, got.WorkspaceReset)
	require.Empty(t, got.Reason)
	require.Empty(t, port.runner.calls)
	require.Equal(t, []string{"u-1", "a-1", "u-2", "a-2"}, messageIDs(msgs.messages))
	require.NotNil(t, sessions.source.ForkBootstrap)
	require.Equal(t, rewindSHA1, sessions.source.ForkBootstrap.CommitSHA)
}

func TestRewindUnopenedForkAbandonsBootstrapWhenNoCheckpointRemains(t *testing.T) {
	history := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	svc, sessions, msgs, port := newUnopenedForkRewindFixture(
		t, history, pendingForkBootstrap(rewindSHA1),
	)
	snapshots := &fakeSnapshotDeleter{}
	svc.snapshots = snapshots

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-1")

	require.NoError(t, err)
	require.Equal(t, 2, got.DeletedMessages)
	require.False(t, got.WorkspaceReset)
	require.Equal(t, RewindSkipNoCheckpoint, got.Reason)
	require.Empty(t, port.runner.calls)
	require.Empty(t, msgs.messages)
	require.True(t, sessions.bootstrapCleared)
	require.Nil(t, sessions.source.ForkBootstrap)
	require.Equal(t, []string{"snap-1"}, snapshots.deleted)
	require.Empty(t, sessions.leases)
}

func TestRewindUnopenedForkKeepsSharedSnapshotWhenAbandoningBootstrap(t *testing.T) {
	history := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	svc, sessions, _, _ := newUnopenedForkRewindFixture(
		t, history, pendingForkBootstrap(rewindSHA1),
	)
	sessions.unconsumed = []*types.Session{{
		ID: "sibling", TenantID: 1,
		ForkBootstrap: pendingForkBootstrap(rewindSHA1),
	}}
	snapshots := &fakeSnapshotDeleter{}
	svc.snapshots = snapshots

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-1")

	require.NoError(t, err)
	require.True(t, sessions.bootstrapCleared)
	require.Empty(t, snapshots.deleted, "nested unopened sibling still needs the snapshot")
	require.Empty(t, sessions.leases)
	require.Equal(t, 2, got.DeletedMessages)
}

func TestRewindConsumedBootstrapIsLeftAloneWhenSandboxGone(t *testing.T) {
	turn1 := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	later := rewindCompletedTurn("u-2", "a-2", "sbx-1", rewindSHA2, 10*time.Second)
	consumedAt := forkBase.Add(time.Minute)
	bootstrap := pendingForkBootstrap(rewindSHA2)
	bootstrap.ConsumedAt = &consumedAt
	svc, sessions, _, port := newUnopenedForkRewindFixture(t, append(turn1, later...), bootstrap)

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-2")

	require.NoError(t, err)
	require.Equal(t, RewindSkipNoSandbox, got.Reason)
	require.Empty(t, port.runner.calls)
	require.False(t, sessions.bootstrapCleared)
	require.NotNil(t, sessions.source.ForkBootstrap)
	require.Equal(t, rewindSHA2, sessions.source.ForkBootstrap.CommitSHA)
	require.True(t, sessions.source.ForkBootstrap.Consumed())
	require.Nil(t, sessions.updatedBootstrap)
}

func TestRewindLiveSandboxDoesNotRewritePendingBootstrap(t *testing.T) {
	turn1 := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	later := rewindCompletedTurn("u-2", "a-2", "sbx-1", rewindSHA2, 10*time.Second)
	port := newFakeRewindPort()
	sessions := newFakeSessionStore(&types.Session{
		ID: "src", TenantID: 1, UserID: "u1", Title: "分支",
		ForkBootstrap: pendingForkBootstrap(rewindSHA2),
	})
	msgs := newFakeMessageStore(append(turn1, later...))
	svc := NewSessionRewindService(sessions, msgs, port, nil, nil)

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-2")

	require.NoError(t, err)
	require.True(t, got.WorkspaceReset)
	require.Contains(t, port.runner.calls[0], rewindSHA1)
	require.Nil(t, sessions.updatedBootstrap)
	require.Equal(t, rewindSHA2, sessions.source.ForkBootstrap.CommitSHA)
}

func TestRewindUnopenedForkBootstrapUpdateFailureDoesNotDeleteMessages(t *testing.T) {
	turn1 := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	later := rewindCompletedTurn("u-2", "a-2", "sbx-1", rewindSHA2, 10*time.Second)
	svc, sessions, msgs, port := newUnopenedForkRewindFixture(
		t, append(turn1, later...), pendingForkBootstrap(rewindSHA2),
	)
	sessions.updateErr = errors.New("db down")

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-2")

	require.Error(t, err)
	require.Nil(t, got)
	require.Contains(t, err.Error(), "db down")
	require.Equal(t, 0, msgs.deleteFromCalls)
	require.Equal(t, []string{"u-1", "a-1", "u-2", "a-2"}, messageIDs(msgs.messages))
	require.Empty(t, port.runner.calls)
	require.Equal(t, rewindSHA2, sessions.source.ForkBootstrap.CommitSHA)
}

func TestRewindUnopenedForkRejectsInvalidCheckpointSHA(t *testing.T) {
	turn1 := rewindCompletedTurn("u-1", "a-1", "sbx-1", "not-a-git-object-name", 0)
	later := rewindCompletedTurn("u-2", "a-2", "sbx-1", rewindSHA2, 10*time.Second)
	svc, sessions, msgs, port := newUnopenedForkRewindFixture(
		t, append(turn1, later...), pendingForkBootstrap(rewindSHA2),
	)

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-2")

	require.Error(t, err)
	require.Nil(t, got)
	require.Contains(t, err.Error(), "invalid")
	require.Equal(t, 0, msgs.deleteFromCalls)
	require.Equal(t, []string{"u-1", "a-1", "u-2", "a-2"}, messageIDs(msgs.messages))
	require.Empty(t, port.runner.calls)
	require.Equal(t, rewindSHA2, sessions.source.ForkBootstrap.CommitSHA)
	require.Nil(t, sessions.updatedBootstrap)
}

func TestRewindBlocksLocalSendWithoutSandbox(t *testing.T) {
	turn := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	laterUser := &types.Message{
		ID: "u-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := newFakeRewindPort()
	port.bound = false
	port.boundID = ""
	svc, msgs := newRewindFixture(t, port, append(turn, laterUser))
	gate := NewSessionBusyGate()
	svc.busyGate = gate
	sessionSvc := &sessionService{busyGate: gate}

	release, err := sessionSvc.holdSandboxTurn(context.Background(), "src", "")
	require.NoError(t, err)

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-2")
	require.ErrorIs(t, err, ErrRewindSourceBusy)
	require.Nil(t, got)
	require.Equal(t, 0, msgs.deleteFromCalls)

	release()
	got, err = svc.Rewind(context.Background(), 1, "u1", "src", "u-2")
	require.NoError(t, err)
	require.Equal(t, 1, got.DeletedMessages)
}

func TestRewindConcurrentSecondIsBusy(t *testing.T) {
	turn1 := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	later := rewindCompletedTurn("u-2", "a-2", "sbx-1", rewindSHA2, 10*time.Second)
	port := newFakeRewindPort()
	started := make(chan struct{})
	unblock := make(chan struct{})
	var blocked atomic.Int32
	port.execBlock = func() {
		if blocked.Add(1) == 1 {
			close(started)
			<-unblock
		}
	}
	svc, msgs := newRewindFixture(t, port, append(turn1, later...))

	done := make(chan struct{})
	var firstErr error
	go func() {
		defer close(done)
		_, firstErr = svc.Rewind(context.Background(), 1, "u1", "src", "u-2")
	}()
	<-started

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "a-1")
	require.ErrorIs(t, err, ErrRewindSourceBusy)
	require.Nil(t, got)

	close(unblock)
	<-done
	require.NoError(t, firstErr)
	require.Equal(t, 1, msgs.deleteFromCalls)
}

func TestRewindLockIsSharedAcrossServiceInstances(t *testing.T) {
	turn1 := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	later := rewindCompletedTurn("u-2", "a-2", "sbx-1", rewindSHA2, 10*time.Second)
	history := append(turn1, later...)
	port := newFakeRewindPort()
	started := make(chan struct{})
	unblock := make(chan struct{})
	var blocked atomic.Int32
	port.execBlock = func() {
		if blocked.Add(1) == 1 {
			close(started)
			<-unblock
		}
	}
	svc1, _ := newRewindFixture(t, port, history)
	svc2, msgs2 := newRewindFixture(t, port, append([]*types.Message(nil), history...))

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = svc1.Rewind(context.Background(), 1, "u1", "src", "u-2")
	}()
	<-started

	got, err := svc2.Rewind(context.Background(), 1, "u1", "src", "a-1")
	require.ErrorIs(t, err, ErrRewindSourceBusy)
	require.Nil(t, got)
	require.Equal(t, 0, msgs2.deleteFromCalls)

	close(unblock)
	<-done
}

func TestRewindUnopenedForkBootstrapClearFailureDoesNotDeleteMessages(t *testing.T) {
	history := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	svc, sessions, msgs, _ := newUnopenedForkRewindFixture(
		t, history, pendingForkBootstrap(rewindSHA1),
	)
	sessions.clearErr = errors.New("db down")

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-1")

	require.Error(t, err)
	require.Nil(t, got)
	require.Contains(t, err.Error(), "db down")
	require.Equal(t, 0, msgs.deleteFromCalls)
	require.Equal(t, []string{"u-1", "a-1"}, messageIDs(msgs.messages))
	require.NotNil(t, sessions.source.ForkBootstrap)
	require.False(t, sessions.bootstrapCleared)
}

func messageIDs(messages []*types.Message) []string {
	ids := make([]string, 0, len(messages))
	for _, m := range messages {
		if m != nil {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

// Clearing the bootstrap column is redoable; deleting the snapshot behind it
// is not. A truncate that never lands must leave the fork something to boot
// from, otherwise the retry finds the whole transcript and no workspace.
func TestRewindKeepsForkSnapshotWhenDeleteFails(t *testing.T) {
	history := rewindCompletedTurn("u-1", "a-1", "sbx-1", rewindSHA1, 0)
	svc, sessions, msgs, _ := newUnopenedForkRewindFixture(
		t, history, pendingForkBootstrap(rewindSHA1),
	)
	snapshots := &fakeSnapshotDeleter{}
	svc.snapshots = snapshots
	msgs.deleteFromErr = errors.New("db down")

	got, err := svc.Rewind(context.Background(), 1, "u1", "src", "u-1")

	require.Error(t, err)
	require.Nil(t, got)
	require.Equal(t, []string{"u-1", "a-1"}, messageIDs(msgs.messages))
	require.Empty(t, snapshots.deleted,
		"the snapshot must survive a truncate that did not happen")
	require.True(t, sessions.bootstrapCleared,
		"the column write stays ahead of the delete so the fork cannot boot ahead of the transcript")
}
