package agent

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestResumeReplaysBackgroundNoticesAtTheirOriginalBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name               string
		completed, pending bool
	}{
		{name: "completed", completed: true},
		{name: "pending", pending: true},
		{name: "both", completed: true, pending: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSuspendFixture(t, "first_tool", false)
			f.engine.EnableSuspension(nil)
			model := f.engine.chatModel.(*mockChat)
			model.responses = append([]mockResponse{{chunks: []types.StreamResponse{{
				ResponseType: types.ResponseTypeAnswer,
				Content:      "previous round",
				ToolCalls: []types.LLMToolCall{{
					ID: "call-before", Type: "function",
					Function: types.FunctionCall{Name: "first_tool", Arguments: `{}`},
				}},
				Done: true, FinishReason: "tool_calls",
			}}}}, model.responses...)
			var deliveries [2]background.Delivery
			if tc.completed {
				deliveries[0] = background.Delivery{Text: "build (sj_1) succeeded.", AckIDs: []string{"sj_1"}}
			}
			if tc.pending {
				deliveries[1] = background.Delivery{Text: "tests (sj_2) failed.", AckIDs: []string{"sj_2"}}
			}
			f.engine.SetBackgroundRun(&fakeBackgroundRun{roundStart: deliveries[:]})
			state := f.run(t)
			require.NotNil(t, state.Suspended)
			require.NoError(t, f.blocked.err)
			snap, err := DecodeRunSnapshot(f.blocked.snapshot)
			require.NoError(t, err)
			require.Len(t, snap.CompletedSteps, 1)
			first := types.BackgroundInjection{
				Content: backgroundNoticesContent("build (sj_1) succeeded."), AckIDs: []string{"sj_1"},
			}
			second := types.BackgroundInjection{
				Content: backgroundNoticesContent("tests (sj_2) failed."), AckIDs: []string{"sj_2"},
			}
			var completed, pending []types.BackgroundInjection
			if tc.completed {
				completed = []types.BackgroundInjection{first}
			}
			if tc.pending {
				pending = []types.BackgroundInjection{second}
			}
			require.Equal(t, completed, snap.CompletedSteps[0].BackgroundInjections)
			require.Equal(t, pending, snap.PendingRound.Step.BackgroundInjections)

			// A fresh replica has no unread delivery: the saved notices must be replayed.
			resumed := newSuspendFixture(t, "first_tool", false)
			resumedModel := resumed.engine.chatModel.(*mockChat)
			resumedModel.responses = []mockResponse{answer("done")}
			state, err = resumed.engine.Resume(t.Context(), "sess-1", "msg-1", nil,
				f.blocked.snapshot, answeredPrompt("call-b"))
			require.NoError(t, err)
			require.True(t, state.IsComplete)
			require.Zero(t, resumed.first.calls, "completed calls must not run again")
			require.NotNil(t, resumed.blocked.answered)
			require.Equal(t, 1, resumed.after.calls)
			require.Len(t, resumedModel.calls, 1)
			var contents []string
			for _, m := range resumedModel.calls[0] {
				contents = append(contents, m.Content)
				for _, part := range m.MultiContent {
					if part.Type == "text" {
						contents = append(contents, part.Text)
					}
				}
			}
			transcript := strings.Join(contents, "\n")
			previous := -1
			var ordered []string
			if tc.completed {
				ordered = append(ordered, first.Content)
			}
			ordered = append(ordered, "previous round")
			if tc.pending {
				ordered = append(ordered, second.Content)
			}
			ordered = append(ordered, "checking first")
			for _, content := range ordered {
				require.Equal(t, 1, strings.Count(transcript, content), "content must appear once: %s", content)
				position := strings.Index(transcript, content)
				require.Greater(t, position, previous, "content is out of order: %s", content)
				previous = position
			}
			require.Equal(t, completed, state.RoundSteps[0].BackgroundInjections)
			require.Equal(t, pending, state.RoundSteps[1].BackgroundInjections)
		})
	}
}
