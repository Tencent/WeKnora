package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type fakeStreamManager struct {
	interfaces.StreamManager
	appended []struct {
		session, message string
		ev               interfaces.StreamEvent
	}
	live map[string]string // session -> assistant message
}

func (f *fakeStreamManager) AppendEvent(_ context.Context, session, message string, ev interfaces.StreamEvent) error {
	f.appended = append(f.appended, struct {
		session string
		message string
		ev      interfaces.StreamEvent
	}{session, message, ev})
	return nil
}

func (f *fakeStreamManager) GetLiveRun(_ context.Context, session string) (string, string, error) {
	if id, ok := f.live[session]; ok {
		return id, "r", nil
	}
	return "", "", nil
}

func TestCommandOutputOnlyWhenParentLive(t *testing.T) {
	streams := &fakeStreamManager{live: map[string]string{"s": "parent"}}
	emitter := NewBackgroundEventEmitter(streams)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	require.NoError(t, emitter.EmitCommandOutput(context.Background(), ref, "parent", "sj_0123",
		event.CommandOutputData{ToolCallID: "tc1", Output: "half"}))
	require.Len(t, streams.appended, 1)
	require.Equal(t, types.ResponseTypeWorkEvent, streams.appended[0].ev.Type)
	require.Equal(t, "sj_0123", streams.appended[0].ev.Data["work_id"])
	inner := streams.appended[0].ev.Data["inner"].(map[string]any)
	require.Equal(t, "command_output", inner["response_type"])

	delete(streams.live, "s")
	require.NoError(t, emitter.EmitCommandOutput(context.Background(), ref, "parent", "sj_0123",
		event.CommandOutputData{Output: "more"}))
	require.Len(t, streams.appended, 1, "父 run 不 live 时丢弃 work_event")
}

func TestWorkStartedFallsBackToCurrentLiveRun(t *testing.T) {
	streams := &fakeStreamManager{live: map[string]string{"s": "other"}}
	emitter := NewBackgroundEventEmitter(streams)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	activity := interfaces.BackgroundActivity{
		ID: "sj_0123", Kind: "shell",
		Title: "build", StartedAt: time.Unix(100, 0),
	}
	require.NoError(t, emitter.EmitWorkStarted(context.Background(), ref, "parent", activity, "tc1"))
	require.Len(t, streams.appended, 1)
	require.Equal(t, "other", streams.appended[0].message, "路由到当前 live run")
	require.Equal(t, types.ResponseTypeWorkStarted, streams.appended[0].ev.Type)
	require.Equal(t, "tc1", streams.appended[0].ev.Data["parent_tool_call_id"])

	streams.live = map[string]string{}
	require.NoError(t, emitter.EmitWorkStarted(context.Background(), ref, "parent", activity, "tc1"))
	require.Len(t, streams.appended, 1, "没有任何 live run 时不写事件")
}

func TestWorkFinishedCarriesStateAndPayload(t *testing.T) {
	streams := &fakeStreamManager{live: map[string]string{"s": "parent"}}
	emitter := NewBackgroundEventEmitter(streams)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	result := interfaces.WorkResult{
		ID: "sj_0123", State: interfaces.WorkStateSucceeded,
		Terminal: true, Payload: map[string]any{"exit_code": 0},
	}
	require.NoError(t, emitter.EmitWorkFinished(context.Background(), ref, "parent", result))
	require.Len(t, streams.appended, 1)
	require.Equal(t, types.ResponseTypeWorkFinished, streams.appended[0].ev.Type)
	require.Equal(t, interfaces.WorkStateSucceeded, streams.appended[0].ev.Data["state"])

	live, err := emitter.ParentLive(context.Background(), ref, "parent")
	require.NoError(t, err)
	require.True(t, live)
}
