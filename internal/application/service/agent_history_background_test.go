package service

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

const replayNotice = "<background_notices>\nbuild (sj_1) succeeded.\n</background_notices>"

func backgroundStepAssistant() *types.Message {
	return &types.Message{
		ID: "a-1", SessionID: "s", Role: "assistant", Content: "all green",
		AgentSteps: []types.AgentStep{
			{
				Iteration: 0, Thought: "checking the build", IntermediateAnswer: true,
				Timestamp:            time.Date(2026, 10, 15, 10, 0, 1, 0, time.UTC),
				BackgroundInjections: []types.BackgroundInjection{{Content: replayNotice, AckIDs: []string{"sj_1"}}},
			},
		},
	}
}

func rolesAndContents(msgs []chat.Message) [][2]string {
	out := make([][2]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, [2]string{m.Role, m.Content})
	}
	return out
}

func TestAssistantHistoryReplaysBackgroundInjectionBeforeItsStep(t *testing.T) {
	got := rolesAndContents(buildAssistantHistoryMessages(backgroundStepAssistant()))

	require.Equal(t, [][2]string{
		{"user", replayNotice},
		{"assistant", "checking the build"},
		{"assistant", "all green"},
	}, got)
}

func TestTurnBodyReplaysSteerThenBackgroundThenStep(t *testing.T) {
	assistant := backgroundStepAssistant()
	assistant.AgentSteps[0].UserMessagesBefore = []string{"u-steer"}
	steer := &types.Message{
		ID: "u-steer", SessionID: "s", Role: "user", Content: "also run lint",
		CreatedAt: time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC),
	}

	got := rolesAndContents(buildTurnBodyMessages(assistant, []*types.Message{steer}))

	require.Len(t, got, 4)
	require.Equal(t, "user", got[0][0])
	require.Contains(t, got[0][1], "also run lint")
	require.Equal(t, [2]string{"user", replayNotice}, got[1])
	require.Equal(t, [2]string{"assistant", "checking the build"}, got[2])
	require.Equal(t, [2]string{"assistant", "all green"}, got[3])
}

func TestHistoryIgnoresBlankBackgroundInjections(t *testing.T) {
	assistant := backgroundStepAssistant()
	assistant.AgentSteps[0].BackgroundInjections = []types.BackgroundInjection{{Content: "  "}}

	got := rolesAndContents(buildAssistantHistoryMessages(assistant))

	require.Equal(t, "assistant", got[0][0])
}
