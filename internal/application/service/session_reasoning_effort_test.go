package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRequestReasoningEffortPrecedence(t *testing.T) {
	for _, tc := range []struct {
		override, want string
		enabled        bool
	}{
		{"", "high", true}, {"off", "off", false}, {"auto", "auto", true}, {"max", "max", true},
	} {
		t.Run("override="+tc.override, func(t *testing.T) {
			enabled := true
			agent := &types.CustomAgent{Config: types.CustomAgentConfig{
				Thinking: &enabled, ReasoningEffort: "high", WebSearchProviderID: "provider",
			}}
			svc := newTagTargetSessionService()
			req := &types.QARequest{
				Session:     &types.Session{ID: "session", TenantID: 100},
				CustomAgent: agent, ReasoningEffort: tc.override,
			}
			cfg, err := svc.buildAgentConfig(tagTargetContext(), req, &types.Tenant{ID: 100}, 100)
			require.NoError(t, err)
			require.Equal(t, tc.want, cfg.ReasoningEffort)
			require.NotNil(t, cfg.Thinking)
			require.Equal(t, tc.enabled, *cfg.Thinking)
			require.Equal(t, "high", agent.Config.ReasoningEffort)
			require.True(t, *agent.Config.Thinking, "runtime override must not mutate the agent")

			// The RAG pipeline starts with the agent defaults and applies the same
			// request override to its summary options.
			cm := &types.ChatManage{}
			svc.applyAgentOverridesToChatManage(tagTargetContext(), agent, cm)
			applyRequestReasoningEffort(
				req.ReasoningEffort, &cm.SummaryConfig.Thinking, &cm.SummaryConfig.ReasoningEffort,
			)
			require.Equal(t, tc.want, cm.SummaryConfig.ReasoningEffort)
			require.Equal(t, tc.enabled, *cm.SummaryConfig.Thinking)
		})
	}
}

func TestRequestReasoningEffortPreservesLegacyDefaults(t *testing.T) {
	for _, original := range []*bool{nil, new(bool)} {
		thinking, effort := original, ""
		applyRequestReasoningEffort("", &thinking, &effort)
		require.True(t, original == thinking)
		require.Empty(t, effort)
	}
}
