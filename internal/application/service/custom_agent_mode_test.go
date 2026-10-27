package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type captureAgentRepo struct {
	interfaces.CustomAgentRepository
	saved *types.CustomAgent
}

func (r *captureAgentRepo) CreateAgent(_ context.Context, agent *types.CustomAgent) error {
	r.saved = agent
	return nil
}

func TestCreateAgentDefaultsOmittedModeToSmartReasoning(t *testing.T) {
	repo := &captureAgentRepo{}
	svc := &customAgentService{repo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	created, err := svc.CreateAgent(ctx, &types.CustomAgent{Name: "天气查询助手"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Config.AgentMode != types.AgentModeSmartReasoning {
		t.Fatalf("agent_mode = %q, want smart-reasoning", created.Config.AgentMode)
	}
	if !created.Config.MultiTurnEnabled {
		t.Fatal("smart-reasoning agent should keep earlier turns")
	}
	if created.Config.MaxIterations != types.UnlimitedMaxIterations {
		t.Fatalf("new agent max_iterations = %d, want unlimited", created.Config.MaxIterations)
	}
	if got := types.AgentRoundMaxCompletionTokens(created.Config.MaxCompletionTokens); got != 24576 {
		t.Fatalf("new agent completion budget = %d, want 24576", got)
	}
}

func TestCreateAgentKeepsExplicitQuickAnswer(t *testing.T) {
	repo := &captureAgentRepo{}
	svc := &customAgentService{repo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	created, err := svc.CreateAgent(ctx, &types.CustomAgent{
		Name:   "知识库问答",
		Config: types.CustomAgentConfig{AgentMode: types.AgentModeQuickAnswer},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Config.AgentMode != types.AgentModeQuickAnswer {
		t.Fatalf("agent_mode = %q, want quick-answer", created.Config.AgentMode)
	}
}
