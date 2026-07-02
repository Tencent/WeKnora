package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubEmbedChannelRepo struct {
	interfaces.EmbedChannelRepository
	ch *types.EmbedChannel
}

func (r *stubEmbedChannelRepo) GetByID(_ context.Context, id string) (*types.EmbedChannel, error) {
	if r.ch == nil || r.ch.ID != id {
		return nil, nil
	}
	cp := *r.ch
	return &cp, nil
}

func (r *stubEmbedChannelRepo) Update(_ context.Context, ch *types.EmbedChannel) error {
	cp := *ch
	r.ch = &cp
	return nil
}

func TestEmbedChannelUpdateAgentID(t *testing.T) {
	repo := &stubEmbedChannelRepo{
		ch: &types.EmbedChannel{
			ID:       "ch-1",
			TenantID: 42,
			AgentID:  "agent-old",
			Name:     "Support",
		},
	}
	svc := &embedChannelService{
		repo: repo,
		agentService: &stubAgentForEmbed{
			agent: &types.CustomAgent{ID: "agent-new", TenantID: 42},
		},
	}
	enabled := true
	updated, err := svc.Update(
		context.Background(),
		42,
		"ch-1",
		&types.EmbedChannel{AgentID: "agent-new"},
		&enabled, nil, nil, nil, nil, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.AgentID != "agent-new" {
		t.Fatalf("updated.AgentID = %q, want agent-new", updated.AgentID)
	}
	if repo.ch.AgentID != "agent-new" {
		t.Fatalf("persisted AgentID = %q, want agent-new", repo.ch.AgentID)
	}
}
