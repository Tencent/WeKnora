package im

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type topicPolicyAdapter struct {
	Adapter
	policy func(context.Context) (bool, error)
}

func (a *topicPolicyAdapter) SetTopicReplyPolicy(policy func(context.Context) (bool, error)) {
	a.policy = policy
}

type topicPolicyAgents struct {
	interfaces.CustomAgentService
	agent *types.CustomAgent
	err   error
}

func (s *topicPolicyAgents) GetAgentByID(ctx context.Context, id string) (*types.CustomAgent, error) {
	tid, ok := types.TenantIDFromContext(ctx)
	if !ok || tid != 7 || id != "agent" {
		return nil, errors.New("wrong identity")
	}
	return s.agent, s.err
}

func TestChannelTopicReplyPolicyUsesCurrentAgent(t *testing.T) {
	agents := &topicPolicyAgents{agent: &types.CustomAgent{ID: "agent", TenantID: 7}}
	service := &Service{agentService: agents}
	adapter := &topicPolicyAdapter{}
	channel := &IMChannel{ID: "channel", TenantID: 7, AgentID: "agent"}
	service.configureTopicReplyPolicy(adapter, channel)
	channel.TenantID = 99
	enabled, err := adapter.policy(context.Background())
	require.NoError(t, err)
	require.False(t, enabled)
	agents.agent.Config.FeishuGroupReplyRule = true
	enabled, err = adapter.policy(context.Background())
	require.NoError(t, err)
	require.True(t, enabled)
	agents.agent.Config.FeishuGroupReplyRule = false
	enabled, err = adapter.policy(context.Background())
	require.NoError(t, err)
	require.False(t, enabled)
	agents.err = errors.New("unavailable")
	enabled, err = adapter.policy(context.Background())
	require.Error(t, err)
	require.False(t, enabled)
	agents.err = nil
	agents.agent.TenantID = 8
	enabled, err = adapter.policy(context.Background())
	require.Error(t, err)
	require.False(t, enabled)
	agents.agent = nil
	enabled, err = adapter.policy(context.Background())
	require.Error(t, err)
	require.False(t, enabled)
	service.configureTopicReplyPolicy(adapter, &IMChannel{TenantID: 7})
	enabled, err = adapter.policy(context.Background())
	require.NoError(t, err)
	require.False(t, enabled)
}
