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
	if cfg.DisplayTitle != "Support" {
		t.Fatalf("display_title = %q, want channel name", cfg.DisplayTitle)
	}
}

func TestPublicConfigDisplayTitlePrefersPageTitle(t *testing.T) {
	svc := &embedChannelService{
		agentService: &stubAgentForEmbed{
			agent: &types.CustomAgent{
				Name: "Agent Name",
			},
		},
	}
	cfg := svc.PublicConfig(context.Background(), &types.EmbedChannel{
		ID:        "ch-3",
		AgentID:   "agent-3",
		Name:      "Internal Channel",
		PageTitle: "Visitor Chat",
	})
	if cfg.DisplayTitle != "Visitor Chat" {
		t.Fatalf("display_title = %q, want page title", cfg.DisplayTitle)
	}
}

func TestPublicConfigIncludesCapabilityFlags(t *testing.T) {
	svc := &embedChannelService{
		agentService: &stubAgentForEmbed{
			agent: &types.CustomAgent{
				Config: types.CustomAgentConfig{
					KBSelectionMode:  "all",
					WebSearchEnabled: true,
				},
			},
		},
	}
	cfg := svc.PublicConfig(context.Background(), &types.EmbedChannel{
		ID:             "ch-cap",
		AgentID:        "agent-cap",
		AllowWebSearch: true,
		WidgetPosition: "top-left",
	})
	if !cfg.AllowWebSearch {
		t.Fatalf("allow_web_search = false, want true")
	}
	if !cfg.AgentWebSearchEnabled {
		t.Fatalf("agent_web_search_enabled = false, want true")
	}
	if cfg.WidgetPosition != "top-left" {
		t.Fatalf("widget_position = %q, want top-left", cfg.WidgetPosition)
	}
}

func TestPublicConfigDisplayTitleFallsBackToAgentName(t *testing.T) {
	svc := &embedChannelService{
		agentService: &stubAgentForEmbed{
			agent: &types.CustomAgent{
				Name: "客服助手",
				Config: types.CustomAgentConfig{
					KBSelectionMode: "all",
				},
			},
		},
	}
	cfg := svc.PublicConfig(context.Background(), &types.EmbedChannel{
		ID:      "ch-2",
		AgentID: "agent-2",
	})
	if cfg.DisplayTitle != "客服助手" {
		t.Fatalf("display_title = %q, want agent name", cfg.DisplayTitle)
	}
	if cfg.AgentName != "客服助手" {
		t.Fatalf("agent_name = %q", cfg.AgentName)
	}
}
