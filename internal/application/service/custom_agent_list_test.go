package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

type listAgentsRepoStub struct {
	byOrg     []*types.CustomAgent
	byCreator []*types.CustomAgent
	all       []*types.CustomAgent
}

func (s *listAgentsRepoStub) CreateAgent(context.Context, *types.CustomAgent) error {
	return nil
}
func (s *listAgentsRepoStub) GetAgentByID(context.Context, string, uint64) (*types.CustomAgent, error) {
	return nil, nil
}
func (s *listAgentsRepoStub) ListAgentsByTenantID(context.Context, uint64) ([]*types.CustomAgent, error) {
	return s.all, nil
}
func (s *listAgentsRepoStub) ListCustomAgentsByOrgUnit(
	_ context.Context, _ uint64, _ string,
) ([]*types.CustomAgent, error) {
	return s.byOrg, nil
}
func (s *listAgentsRepoStub) ListCustomAgentsByCreator(
	_ context.Context, _ uint64, _ string,
) ([]*types.CustomAgent, error) {
	return s.byCreator, nil
}
func (s *listAgentsRepoStub) UpdateAgent(context.Context, *types.CustomAgent) error {
	return nil
}
func (s *listAgentsRepoStub) DeleteAgent(context.Context, string, uint64) error {
	return nil
}
func (s *listAgentsRepoStub) CountByModelID(context.Context, uint64, string) (int64, error) {
	return 0, nil
}
func (s *listAgentsRepoStub) CountBySandboxConfigID(context.Context, uint64, string) (int64, error) {
	return 0, nil
}
func (s *listAgentsRepoStub) ListNamesBySandboxConfigID(
	context.Context, uint64, string,
) ([]string, error) {
	return nil, nil
}
func (s *listAgentsRepoStub) ListModelUsages(
	context.Context, uint64, string,
) ([]types.ModelUsageResource, error) {
	return nil, nil
}

func TestListAgentsPurposeChatAndManage(t *testing.T) {
	repo := &listAgentsRepoStub{
		byOrg: []*types.CustomAgent{
			{ID: "a1", Name: "dept", OrgUnitID: "ou-1"},
		},
		byCreator: []*types.CustomAgent{
			{ID: "a2", Name: "mine", CreatedBy: "u1"},
		},
		all: []*types.CustomAgent{
			{ID: "a2", Name: "mine", CreatedBy: "u1"},
			{ID: "a3", Name: "peer", CreatedBy: "u2"},
			{ID: "builtin-quick-answer", Name: "builtin", IsBuiltin: true},
		},
	}
	svc := &customAgentService{repo: repo}

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.OrgUnitIDContextKey, "ou-1")
	ctx = context.WithValue(ctx, types.UserIDContextKey, "u1")

	chatAgents, err := svc.ListAgents(ctx, "chat")
	if err != nil {
		t.Fatalf("chat list: %v", err)
	}
	if len(chatAgents) != 1 || chatAgents[0].ID != "a1" {
		t.Fatalf("chat agents = %#v, want a1", chatAgents)
	}

	// manage + active OrgUnit → org-scoped list (所在组织), not creator-only
	manageAgents, err := svc.ListAgents(ctx, "manage")
	if err != nil {
		t.Fatalf("manage list: %v", err)
	}
	if len(manageAgents) != 1 || manageAgents[0].ID != "a1" {
		t.Fatalf("manage agents = %#v, want a1 (org-scoped)", manageAgents)
	}

	sysCtx := context.WithValue(ctx, types.SystemAdminContextKey, true)
	// System admin with an explicit OrgUnit still stays org-scoped when set.
	sysScoped, err := svc.ListAgents(sysCtx, "manage")
	if err != nil {
		t.Fatalf("sys manage scoped: %v", err)
	}
	if len(sysScoped) != 1 || sysScoped[0].ID != "a1" {
		t.Fatalf("sys admin with org unit = %#v, want a1", sysScoped)
	}

	sysAllCtx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	sysAllCtx = context.WithValue(sysAllCtx, types.SystemAdminContextKey, true)
	sysAllCtx = context.WithValue(sysAllCtx, types.UserIDContextKey, "u1")
	sysAgents, err := svc.ListAgents(sysAllCtx, "manage")
	if err != nil {
		t.Fatalf("sys manage list: %v", err)
	}
	if len(sysAgents) != 2 {
		t.Fatalf("sys admin unscoped manage agents = %#v, want 2 custom", sysAgents)
	}
	for _, agent := range sysAgents {
		if agent.IsBuiltin || types.IsBuiltinAgentID(agent.ID) {
			t.Fatalf("builtin leaked into manage list: %#v", agent)
		}
	}

	adminAllCtx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	adminAllCtx = context.WithValue(adminAllCtx, types.TenantRoleContextKey, types.TenantRoleAdmin)
	adminAllCtx = context.WithValue(adminAllCtx, types.UserIDContextKey, "u1")
	adminAgents, err := svc.ListAgents(adminAllCtx, "manage")
	if err != nil {
		t.Fatalf("admin unscoped manage: %v", err)
	}
	if len(adminAgents) != 2 {
		t.Fatalf("admin unscoped manage = %#v, want 2", adminAgents)
	}
}
