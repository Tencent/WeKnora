package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type failingTool struct{ agenttools.BaseTool }

func (*failingTool) Execute(context.Context, json.RawMessage) (*types.ToolResult, error) {
	return &types.ToolResult{Success: false, Error: "not found"}, nil
}

func pageToolResponse(content, tool string) mockResponse {
	return mockResponse{chunks: []types.StreamResponse{{
		Content: content, Done: true, FinishReason: "tool_calls",
		ToolCalls: []types.LLMToolCall{{
			ID: "page", Type: "function", Function: types.FunctionCall{Name: tool, Arguments: `{"page":"agents"}`},
		}},
	}}}
}

func answerResponse(content string) mockResponse {
	return mockResponse{chunks: []types.StreamResponse{{Content: content, Done: true}}}
}

type pageButtonRun struct {
	state    *types.AgentState
	model    *mockChat
	tool     *countingTool
	answers  *terminalAnswerRecorder
	progress []string
}

func runPageButtonTurn(
	t *testing.T, binding *collaboration.Binding, tool types.Tool, responses ...mockResponse,
) *pageButtonRun {
	t.Helper()
	run := &pageButtonRun{model: &mockChat{responses: responses}, answers: &terminalAnswerRecorder{}}
	engine := newTestEngine(t, run.model)
	engine.eventBus = event.NewEventBus()
	engine.toolRegistry = agenttools.NewToolRegistry()
	if tool == nil {
		run.tool = newCountingTool(agenttools.ToolOpenPage)
		tool = run.tool
	}
	engine.toolRegistry.RegisterTool(tool)
	engine.toolRegistry.RegisterTool(newCountingTool(agenttools.ToolViewPage))
	if binding != nil {
		engine.SetCollaboration(binding)
	}
	run.answers.attach(engine.eventBus)
	var mu sync.Mutex
	engine.eventBus.On(event.EventAgentProgress, func(_ context.Context, evt event.Event) error {
		mu.Lock()
		defer mu.Unlock()
		run.progress = append(run.progress, evt.ID)
		return nil
	})
	run.state = &types.AgentState{RoundSteps: []types.AgentStep{}}
	_, err := engine.executeLoop(context.Background(), run.state, "list my agents",
		[]chat.Message{{Role: "user", Content: "list my agents"}}, emptyTools(), "s", "m")
	require.NoError(t, err)
	require.True(t, run.state.IsComplete)
	return run
}

func requireAnsweredWithButton(t *testing.T, run *pageButtonRun) {
	t.Helper()
	require.Len(t, run.model.calls, 1, "the answer is already written; no round restates it")
	require.Equal(t, 1, run.tool.calls, "the button is offered once")
	require.Equal(t, "| agent | model |\n|---|---|\n| a | m |", run.state.FinalAnswer)
	require.Len(t, run.state.RoundSteps, 1)
	step := run.state.RoundSteps[0]
	require.Empty(t, step.Thought, "the answer must not replay again as the step's thought")
	require.Empty(t, step.Progress, "the answer is not moved into the steps")
	require.Len(t, step.ToolCalls, 1)
	done, _ := run.answers.snapshot()
	require.Equal(t, 1, done)
	require.Empty(t, run.progress, "the answer is not retracted")
}

func TestAnswerWithPageButtonEndsTheTurn(t *testing.T) {
	requireAnsweredWithButton(t, runPageButtonTurn(t, nil, nil,
		pageToolResponse("| agent | model |\n|---|---|\n| a | m |", agenttools.ToolOpenPage),
		answerResponse("unused"),
	))
}

func TestAnswerWithPageButtonEndsACoordinatorTurn(t *testing.T) {
	_, binding := engineCollaboration(t)
	requireAnsweredWithButton(t, runPageButtonTurn(t, binding, nil,
		pageToolResponse("| agent | model |\n|---|---|\n| a | m |", agenttools.ToolOpenPage),
		answerResponse("unused"),
	))
}

func TestPageButtonWithoutAnswerContinues(t *testing.T) {
	run := runPageButtonTurn(t, nil, nil,
		pageToolResponse("", agenttools.ToolOpenPage),
		answerResponse("the report"),
	)
	require.Len(t, run.model.calls, 2)
	require.Equal(t, "the report", run.state.FinalAnswer)
}

func TestViewPageIsAStep(t *testing.T) {
	run := runPageButtonTurn(t, nil, nil,
		pageToolResponse("Let me look at the page.", agenttools.ToolViewPage),
		answerResponse("the setting is on"),
	)
	require.Len(t, run.model.calls, 2, "view_page returns what the page shows, so the model reads it")
	require.Equal(t, "the setting is on", run.state.FinalAnswer)
}

func TestFailedPageButtonRetractsTheAnswer(t *testing.T) {
	failing := &failingTool{
		agenttools.NewBaseTool(agenttools.ToolOpenPage, "test", json.RawMessage(`{"type":"object"}`)),
	}
	run := runPageButtonTurn(t, nil, failing,
		pageToolResponse("the report", agenttools.ToolOpenPage),
		answerResponse("the report again"),
	)
	require.Len(t, run.model.calls, 2, "the model gets to fix the button")
	require.Equal(t, "the report again", run.state.FinalAnswer)
	require.Len(t, run.state.RoundSteps[0].ToolCalls, 1, "the failed button ran once")
	require.Equal(t, "the report", run.state.RoundSteps[0].Progress)
	require.Len(t, run.progress, 1, "the streamed text moves into the steps")
}
