package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// transcriptStreams records what the installer projects onto the replayable
// stream, in order, so a test can state the shape the console will read.
type transcriptStreams struct {
	events []interfaces.StreamEvent
	err    error
}

func (s *transcriptStreams) AppendEvent(
	_ context.Context, _, _ string, evt interfaces.StreamEvent,
) error {
	s.events = append(s.events, evt)
	return s.err
}

func (s *transcriptStreams) GetEvents(
	_ context.Context, _, _ string, from int,
) ([]interfaces.StreamEvent, int, error) {
	return s.events[from:], len(s.events), nil
}

func (s *transcriptStreams) types() []types.ResponseType {
	out := make([]types.ResponseType, 0, len(s.events))
	for _, evt := range s.events {
		out = append(out, evt.Type)
	}
	return out
}

// transcriptMessages is the durable half. Only the two calls the transcript
// makes are implemented; the rest panic so an accidental new dependency on the
// repository surfaces as a test failure rather than a silent nil write.
type transcriptMessages struct {
	interfaces.MessageRepository
	created []*types.Message
	updated []*types.Message
	err     error
}

func (m *transcriptMessages) CreateMessage(
	_ context.Context, msg *types.Message,
) (*types.Message, error) {
	m.created = append(m.created, msg)
	return msg, m.err
}

func (m *transcriptMessages) UpdateMessage(_ context.Context, msg *types.Message) error {
	m.updated = append(m.updated, msg)
	return m.err
}

func newTranscriptForTest(t *testing.T) (
	*installTranscript, *event.EventBus, *transcriptStreams, *transcriptMessages,
) {
	t.Helper()
	bus := event.NewEventBus()
	streams := &transcriptStreams{}
	messages := &transcriptMessages{}
	tr := newInstallTranscript(context.Background(), bus, streams, messages, "sess-1", "msg-1")
	tr.Subscribe()
	return tr, bus, streams, messages
}

func TestInstallTranscriptProjectsTheAgentsWork(t *testing.T) {
	_, bus, streams, _ := newTranscriptForTest(t)
	ctx := context.Background()

	require.NoError(t, bus.Emit(ctx, event.Event{
		ID: "t-1", Type: event.EventAgentThought,
		Data: event.AgentThoughtData{Content: "check for uv", Done: false},
	}))
	require.NoError(t, bus.Emit(ctx, event.Event{
		ID: "c-1", Type: event.EventAgentToolCall,
		Data: event.AgentToolCallData{
			ToolName: "shell_exec", ToolCallID: "call-1",
			Arguments: map[string]any{"command": "uv --version"},
		},
	}))
	require.NoError(t, bus.Emit(ctx, event.Event{
		ID: "r-1", Type: event.EventAgentToolResult,
		Data: event.AgentToolResultData{
			ToolName: "shell_exec", ToolCallID: "call-1", Success: true, Output: "uv 0.4.0",
		},
	}))

	require.Equal(t, []types.ResponseType{
		types.ResponseTypeThinking,
		types.ResponseTypeToolCall,
		types.ResponseTypeToolResult,
	}, streams.types())

	call := streams.events[1]
	require.Equal(t, "shell_exec", call.Data["tool_name"])
	require.Equal(t, "call-1", call.Data["tool_call_id"])
}

// A failed command is the single most useful thing in an install transcript,
// so it must reach the stream as an error the console already knows how to
// render, not be dropped for having Success=false.
func TestInstallTranscriptReportsAFailedCommandAsAnError(t *testing.T) {
	_, bus, streams, _ := newTranscriptForTest(t)

	require.NoError(t, bus.Emit(context.Background(), event.Event{
		ID: "r-1", Type: event.EventAgentToolResult,
		Data: event.AgentToolResultData{
			ToolName: "shell_exec", ToolCallID: "call-1",
			Success: false, Error: "exit status 1",
		},
	}))

	require.Equal(t, []types.ResponseType{types.ResponseTypeError}, streams.types())
	require.Equal(t, "exit status 1", streams.events[0].Content)
	require.Equal(t, false, streams.events[0].Data["success"])
}

func TestInstallTranscriptPersistsTheAnswerAndStepsOnComplete(t *testing.T) {
	tr, bus, streams, messages := newTranscriptForTest(t)
	ctx := context.Background()

	require.NoError(t, bus.Emit(ctx, event.Event{
		ID: "a-1", Type: event.EventAgentFinalAnswer,
		Data: event.AgentFinalAnswerData{Content: "installed ", Done: false},
	}))
	require.NoError(t, bus.Emit(ctx, event.Event{
		ID: "a-1", Type: event.EventAgentFinalAnswer,
		Data: event.AgentFinalAnswerData{Content: "pdf-tools", Done: true},
	}))
	require.NoError(t, bus.Emit(ctx, event.Event{
		ID: "done", Type: event.EventAgentComplete,
		Data: event.AgentCompleteData{
			MessageID: "msg-1", TotalDurationMs: 4200, TotalSteps: 3,
		},
	}))
	tr.Finish(ctx, nil)

	require.Equal(t, types.ResponseTypeComplete, streams.types()[len(streams.types())-1])
	require.Len(t, messages.updated, 1)
	saved := messages.updated[0]
	require.Equal(t, "installed pdf-tools", saved.Content)
	require.True(t, saved.IsCompleted)
	require.EqualValues(t, 4200, saved.AgentDurationMs)
}

// The engine routes a round's plain assistant prose through
// EventAgentFinalAnswer, so every "now I'll check X" preamble arrives as an
// answer chunk. Only the prose of the round that ends the run is the answer.
// Without retracting the preambles the persisted record is every round's
// commentary glued end to end, which is unreadable — and is what the console
// renders as the install's result.
func TestInstallTranscriptRetractsPreamblesSupersededByAToolCall(t *testing.T) {
	tr, bus, _, messages := newTranscriptForTest(t)
	ctx := context.Background()

	emitAnswer := func(id, content string, done bool) {
		require.NoError(t, bus.Emit(ctx, event.Event{
			ID: id, Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: content, Done: done},
		}))
	}

	emitAnswer("a-1", "The script needs duckduckgo-search, so I'll create the venv:", true)
	require.NoError(t, bus.Emit(ctx, event.Event{
		ID: "c-1", Type: event.EventAgentToolCall,
		Data: event.AgentToolCallData{
			ToolName: "shell_exec", ToolCallID: "call-1",
			Arguments: map[string]any{"command": "uv venv .venv"},
		},
	}))
	emitAnswer("a-2", "Installed successfully. Now verifying:", true)
	require.NoError(t, bus.Emit(ctx, event.Event{
		ID: "c-2", Type: event.EventAgentToolCall,
		Data: event.AgentToolCallData{
			ToolName: "shell_exec", ToolCallID: "call-2",
			Arguments: map[string]any{"command": "python scripts/search.py --help"},
		},
	}))
	emitAnswer("a-3", "Installed duckduckgo-search into the skill venv.", true)

	require.NoError(t, bus.Emit(ctx, event.Event{
		ID: "done", Type: event.EventAgentComplete,
		Data: event.AgentCompleteData{MessageID: "msg-1", TotalDurationMs: 900},
	}))
	tr.Finish(ctx, nil)

	require.Len(t, messages.updated, 1)
	require.Equal(t,
		"Installed duckduckgo-search into the skill venv.",
		messages.updated[0].Content,
		"only the final round's prose is the answer; earlier preambles were retracted by their tool calls",
	)
}

// An install that dies before the engine emits complete is exactly the run
// someone will come looking for, so Finish must close the record itself.
func TestInstallTranscriptRecordsAFailedRun(t *testing.T) {
	tr, _, streams, messages := newTranscriptForTest(t)

	tr.Finish(context.Background(), errors.New("installer agent failed: boom"))

	require.Equal(t, []types.ResponseType{
		types.ResponseTypeError,
		types.ResponseTypeComplete,
	}, streams.types())
	require.Len(t, messages.updated, 1)
	require.Contains(t, messages.updated[0].Content, "boom")
	require.True(t, messages.updated[0].IsCompleted)
}

// The transcript is a troubleshooting aid. A broken Redis or a failing write
// must not be able to fail an otherwise good install.
func TestInstallTranscriptSwallowsItsOwnFailures(t *testing.T) {
	bus := event.NewEventBus()
	streams := &transcriptStreams{err: errors.New("redis down")}
	messages := &transcriptMessages{err: errors.New("db down")}
	tr := newInstallTranscript(context.Background(), bus, streams, messages, "sess-1", "msg-1")
	tr.Subscribe()

	require.NoError(t, bus.Emit(context.Background(), event.Event{
		ID: "t-1", Type: event.EventAgentThought,
		Data: event.AgentThoughtData{Content: "hi", Done: true},
	}))
	require.NotPanics(t, func() { tr.Finish(context.Background(), nil) })
}

func TestInstallTranscriptCreateSeedsTheAssistantRow(t *testing.T) {
	streams := &transcriptStreams{}
	messages := &transcriptMessages{}
	tr := newInstallTranscript(
		context.Background(), event.NewEventBus(), streams, messages, "sess-1", "msg-1",
	)

	require.NoError(t, tr.Create(context.Background(), "install pdf-tools"))
	require.Len(t, messages.created, 2)
	require.Equal(t, "user", messages.created[0].Role)
	require.Equal(t, "install pdf-tools", messages.created[0].Content)
	require.Equal(t, "assistant", messages.created[1].Role)
	require.Equal(t, "msg-1", messages.created[1].ID)
	require.Equal(t, "sess-1", messages.created[1].SessionID)
}

// A console that attaches to a running install reads the event log and nothing
// else, so the prompt has to be in the log or the run shows up as an agent
// answering a question nobody can see.
func TestInstallTranscriptCreateOpensTheLogWithThePrompt(t *testing.T) {
	streams := &transcriptStreams{}
	tr := newInstallTranscript(
		context.Background(), event.NewEventBus(), streams, &transcriptMessages{}, "sess-1", "msg-1",
	)

	require.NoError(t, tr.Create(context.Background(), "install pdf-tools"))

	require.Equal(t, []types.ResponseType{types.ResponseTypeInstallPrompt}, streams.types())
	require.Equal(t, "install pdf-tools", streams.events[0].Content)
}

// An install may run a second installer round, because verification happens
// after the agent stops and a dependency it names is something the agent can
// still fix. The engine reports "complete" at the end of every turn, so the
// transcript must not treat the first one as the end of the install — a console
// that saw it would stop following before the repair round began.
func TestInstallTranscriptStaysOpenAcrossInstallerRounds(t *testing.T) {
	tr, bus, streams, messages := newTranscriptForTest(t)
	ctx := context.Background()

	finishRound := func(steps int, ms int64) {
		require.NoError(t, bus.Emit(ctx, event.Event{
			ID: "done", Type: event.EventAgentComplete,
			Data: event.AgentCompleteData{
				MessageID: "msg-1", TotalSteps: steps, TotalDurationMs: ms,
			},
		}))
	}

	finishRound(3, 1000)
	tr.RecordPrompt("Verification failed. Install defusedxml into the venv.")
	finishRound(2, 500)
	tr.Finish(ctx, nil)

	kinds := streams.types()
	require.Equal(t, []types.ResponseType{
		types.ResponseTypeInstallPrompt,
		types.ResponseTypeComplete,
	}, kinds, "the repair instruction has to be in the log, and the install ends once")
	require.Contains(t, streams.events[0].Content, "Install defusedxml")

	terminal := streams.events[len(streams.events)-1]
	require.EqualValues(t, 5, terminal.Data["total_steps"],
		"an install that needed a repair round cost both turns")
	require.EqualValues(t, 1500, terminal.Data["total_duration_ms"])
	require.Len(t, messages.updated, 1)
}

// Finish is deferred by the install path and also called on the error path that
// gives up before that defer is armed. Saying "this install is over" twice would
// duplicate the verdict in the record people read.
func TestInstallTranscriptFinishIsIdempotent(t *testing.T) {
	tr, _, streams, messages := newTranscriptForTest(t)

	tr.Finish(context.Background(), errors.New("python verification failed: boom"))
	tr.Finish(context.Background(), errors.New("python verification failed: boom"))

	require.Equal(t, []types.ResponseType{
		types.ResponseTypeError,
		types.ResponseTypeComplete,
	}, streams.types())
	require.Len(t, messages.updated, 1)
}
