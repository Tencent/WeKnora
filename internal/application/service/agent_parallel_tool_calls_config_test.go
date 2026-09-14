package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// The engine already knows how to run a round's tool calls concurrently
// (AgentEngine.executeToolCallsParallel, gated on AgentConfig.ParallelToolCalls),
// but nothing ever set that flag: buildAgentConfig copies CustomAgentConfig into
// the runtime AgentConfig field by field and this one was absent, so the gate
// read the zero value and the parallel branch was unreachable for every custom
// agent. Failure was silent — tools just kept running one at a time.
func TestAgentConfigCarriesTheParallelToolCallsPreference(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{name: "opted in", want: true},
		{name: "opted out", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &sessionService{
				cfg:                   &config.Config{},
				webSearchProviderRepo: &sharedAgentWebSearchRepo{},
			}
			req := &types.QARequest{
				Session: &types.Session{ID: "session-1", TenantID: 1},
				CustomAgent: &types.CustomAgent{
					TenantID: 1,
					Config: types.CustomAgentConfig{
						MaxIterations:     5,
						ParallelToolCalls: tc.want,
					},
				},
			}

			agentConfig, err := svc.buildAgentConfig(t.Context(), req, &types.Tenant{ID: 1}, 1)
			require.NoError(t, err)
			require.Equal(t, tc.want, agentConfig.ParallelToolCalls)
		})
	}
}

// Agents saved before this option existed must keep running their tools
// sequentially: tools are not required to be side-effect free, so silently
// turning on concurrency for them would be a behavior change, not a fix.
func TestAgentConfigDefaultsParallelToolCallsOff(t *testing.T) {
	svc := &sessionService{
		cfg:                   &config.Config{},
		webSearchProviderRepo: &sharedAgentWebSearchRepo{},
	}
	req := &types.QARequest{
		Session: &types.Session{ID: "session-1", TenantID: 1},
		CustomAgent: &types.CustomAgent{
			TenantID: 1,
			Config:   types.CustomAgentConfig{MaxIterations: 5},
		},
	}

	agentConfig, err := svc.buildAgentConfig(t.Context(), req, &types.Tenant{ID: 1}, 1)
	require.NoError(t, err)
	require.False(t, agentConfig.ParallelToolCalls)
}
