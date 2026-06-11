package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubAgentForEmbed struct {
	interfaces.CustomAgentService
	agent *types.CustomAgent
	err   error
}

func (s *stubAgentForEmbed) GetAgentByID(_ context.Context, _ string) (*types.CustomAgent, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.agent, nil
}

func TestResolveKnowledgeBaseIDsFromAgent(t *testing.T) {
	svc := &embedChannelService{
		agentService: &stubAgentForEmbed{
			agent: &types.CustomAgent{
				Config: types.CustomAgentConfig{
					KBSelectionMode: "selected",
					KnowledgeBases:  []string{"kb-a", "kb-b"},
				},
			},
		},
	}
	ids := svc.resolveKnowledgeBaseIDs(context.Background(), &types.EmbedChannel{
		AgentID: "agent-1",
	})
	if len(ids) != 2 || ids[0] != "kb-a" || ids[1] != "kb-b" {
		t.Fatalf("unexpected kb ids: %#v", ids)
	}
}

func TestPublicConfigUsesAgentKBs(t *testing.T) {
	svc := &embedChannelService{
		agentService: &stubAgentForEmbed{
			agent: &types.CustomAgent{
				Config: types.CustomAgentConfig{
					KBSelectionMode: "selected",
					KnowledgeBases:  []string{"kb-primary"},
				},
			},
		},
	}
	cfg := svc.PublicConfig(context.Background(), &types.EmbedChannel{
		ID:      "ch-1",
		AgentID: "agent-1",
		Name:    "Support",
	})
	if len(cfg.KnowledgeBaseIDs) != 1 || cfg.KnowledgeBaseIDs[0] != "kb-primary" {
		t.Fatalf("unexpected public config: %#v", cfg)
	}
	if cfg.AgentID != "agent-1" || cfg.ChannelID != "ch-1" {
		t.Fatalf("unexpected ids: %#v", cfg)
	}
}
