package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/stretchr/testify/require"
)

func TestShellCommandOutputIsBoundedAndFinishes(t *testing.T) {
	bus := event.NewEventBus()

	updates := make(chan event.CommandOutputData, 16)
	bus.On(event.EventAgentCommandOutput, func(_ context.Context, e event.Event) error {
		updates <- e.Data.(event.CommandOutputData)
		return nil
	})
	next := func() event.CommandOutputData {
		select {
		case update := <-updates:
			return update
		case <-time.After(3 * time.Second):
			t.Fatal("missing progress event")
			return event.CommandOutputData{}
		}
	}
	ctx := WithToolExecContext(context.Background(), &ToolExecContext{
		EventBus: bus, ToolCallID: "cmd-1", SessionID: "session",
	})
	output, finish := shellCommandOutput(ctx, "uv pip install -r requirements.lock")
	defer finish()
	start := next()
	require.Equal(t, "cmd-1", start.ToolCallID)
	require.False(t, start.Done)
	output("stdout", []byte(strings.Repeat("x", 16000)))
	first := next()
	require.LessOrEqual(t, len(first.Output), 8192)
	output("stderr", []byte("\ndownloading wheel"))
	// A short burst followed by a long silent download must still flush before completion.
	progress := next()
	require.Contains(t, progress.Output, "downloading wheel")
	require.False(t, progress.Done)
	finish()
	finish()
	require.True(t, next().Done)
	output("stdout", []byte("late callback"))
	require.Empty(t, updates)
}

func TestShellExecPublishesCommandProgress(t *testing.T) {
	bus := event.NewEventBus()
	var updates []event.CommandOutputData
	bus.On(event.EventAgentCommandOutput, func(_ context.Context, e event.Event) error {
		updates = append(updates, e.Data.(event.CommandOutputData))
		return nil
	})
	executor := &fakeShellExecutor{}
	tool := NewShellExecTool(executor, nil)
	ctx := WithToolExecContext(context.Background(), &ToolExecContext{
		EventBus: bus, SessionID: "session", ToolCallID: "call",
	})
	result, err := tool.Execute(ctx, json.RawMessage(`{"command":"cat","stdin":"private input"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Len(t, updates, 2)
	require.Equal(t, "call", updates[0].ToolCallID)
	require.Equal(t, "cat", updates[0].Command, "progress shows the requested command, not the encoded stdin wrapper")
	require.False(t, updates[0].Done)
	require.True(t, updates[1].Done)
}
