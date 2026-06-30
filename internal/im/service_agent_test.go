package im

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubIMAgentService struct {
	interfaces.CustomAgentService
	agent *types.CustomAgent
}

func (s *stubIMAgentService) GetAgentByID(_ context.Context, id string) (*types.CustomAgent, error) {
	if s.agent != nil && s.agent.ID == id {
		return s.agent, nil
	}
	return nil, nil
}

func TestSetChannelAgentID(t *testing.T) {
	svc := &Service{
		agentService: &stubIMAgentService{
			agent: &types.CustomAgent{ID: "agent-new", TenantID: 42},
		},
	}
	channel := &IMChannel{
		ID:       "im-1",
		TenantID: 42,
		AgentID:  "agent-old",
	}

	if err := svc.SetChannelAgentID(context.Background(), channel, "agent-new"); err != nil {
		t.Fatalf("SetChannelAgentID() error = %v", err)
	}
	if channel.AgentID != "agent-new" {
		t.Fatalf("channel.AgentID = %q, want agent-new", channel.AgentID)
	}
}

func TestSetChannelAgentIDRejectsForeignTenant(t *testing.T) {
	svc := &Service{
		agentService: &stubIMAgentService{
			agent: &types.CustomAgent{ID: "agent-new", TenantID: 99},
		},
	}
	channel := &IMChannel{
		ID:       "im-1",
		TenantID: 42,
		AgentID:  "agent-old",
	}

	if err := svc.SetChannelAgentID(context.Background(), channel, "agent-new"); err == nil {
		t.Fatal("SetChannelAgentID() expected error for foreign tenant agent")
	}
	if channel.AgentID != "agent-old" {
		t.Fatalf("channel.AgentID = %q, want unchanged agent-old", channel.AgentID)
	}
}

func TestSetChannelAgentIDRequiresAgentID(t *testing.T) {
	svc := &Service{agentService: &stubIMAgentService{}}
	channel := &IMChannel{ID: "im-1", TenantID: 42, AgentID: "agent-old"}

	if err := svc.SetChannelAgentID(context.Background(), channel, "  "); err == nil {
		t.Fatal("SetChannelAgentID() expected error for empty agent_id")
	}
}
