package im

import (
	"context"
	"fmt"
)

// TopicReplyPolicySetter allows an adapter to check the agent's current opt-in
// before admitting an unmentioned topic starter. The adapter defaults to off.
type TopicReplyPolicySetter interface {
	SetTopicReplyPolicy(func(context.Context) (bool, error))
}

func (s *Service) configureTopicReplyPolicy(adapter Adapter, channel *IMChannel) {
	setter, ok := adapter.(TopicReplyPolicySetter)
	if !ok {
		return
	}
	// Capture identity rather than sharing the mutable channel configuration.
	identity := *channel
	setter.SetTopicReplyPolicy(func(ctx context.Context) (bool, error) {
		if identity.AgentID == "" {
			return false, nil
		}
		if s.agentService == nil {
			return false, fmt.Errorf("agent service unavailable")
		}
		agent, err := s.agentService.GetAgentByID(withIMIdentity(ctx, &identity, nil), identity.AgentID)
		if err != nil {
			return false, err
		}
		if agent == nil || agent.TenantID != identity.TenantID {
			return false, fmt.Errorf("channel agent unavailable")
		}
		return agent.Config.FeishuGroupReplyRule, nil
	})
}
