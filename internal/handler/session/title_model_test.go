package session

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestTitleModelFollowsValidatedConversationSelection(t *testing.T) {
	agent := &types.CustomAgent{Config: types.CustomAgentConfig{ModelID: "agent-model"}}
	for _, tc := range []struct {
		name     string
		selected string
		agent    *types.CustomAgent
		want     string
	}{
		{"input overrides agent", "input-model", agent, "input-model"},
		{"input without agent", "input-model", nil, "input-model"},
		{"absent or cleared override uses agent", "", agent, "agent-model"},
		{"unconfigured agent keeps default fallback", "", &types.CustomAgent{}, ""},
		{"no selection keeps default fallback", "", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc := &qaRequestContext{summaryModelID: tc.selected, customAgent: tc.agent}
			require.Equal(t, tc.want, rc.titleModelID())
			require.Equal(t, "agent-model", agent.Config.ModelID, "selection must not rewrite agent settings")
		})
	}
}
