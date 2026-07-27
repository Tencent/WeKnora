package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

type tenantToolResolverStub struct { loaded types.SkillReference }
func (resolver *tenantToolResolverStub) LoadInstructions(_ context.Context, _ skills.RuntimeScope, ref types.SkillReference) (*skills.Skill, error) {
	resolver.loaded = ref
	return &skills.Skill{Name: "tenant-skill", Description: "tenant", Instructions: "instructions"}, nil
}
func (resolver *tenantToolResolverStub) ReadFile(_ context.Context, _ skills.RuntimeScope, ref types.SkillReference, _ string) (string, error) {
	resolver.loaded = ref; return "content", nil
}
func (resolver *tenantToolResolverStub) Execute(_ context.Context, _ skills.RuntimeScope, ref types.SkillReference, _ string, _ []string, _ string) (*sandbox.ExecuteResult, error) {
	resolver.loaded = ref; return &sandbox.ExecuteResult{}, nil
}

func TestReadSkillUsesCanonicalTenantReference(t *testing.T) {
	ref := types.SkillReference{Source: types.SkillSourceTenant, SkillID: "skill-id"}
	resolver := &tenantToolResolverStub{}
	manager := skills.NewManager(&skills.ManagerConfig{Enabled: true, AllowedSkillRefs: []types.SkillReference{ref}, Resolver: resolver, RuntimeScope: skills.RuntimeScope{TenantID: 7}}, nil)
	tool := NewReadSkillTool(manager)
	args, _ := json.Marshal(ReadSkillInput{SkillRef: &SkillRefInput{Source: ref.Source, SkillID: ref.SkillID}})
	result, err := tool.Execute(context.Background(), args)
	if err != nil || !result.Success || resolver.loaded != ref { t.Fatalf("canonical tenant ref was not used: result=%+v ref=%+v err=%v", result, resolver.loaded, err) }
}

func TestLegacySkillNameNeverBindsTenantSkillWithSameName(t *testing.T) {
	ref := types.SkillReference{Source: types.SkillSourceTenant, SkillID: "same-name"}
	resolver := &tenantToolResolverStub{}
	manager := skills.NewManager(&skills.ManagerConfig{Enabled: true, SkillDirs: []string{t.TempDir()}, AllowedSkillRefs: []types.SkillReference{ref}, Resolver: resolver}, nil)
	tool := NewReadSkillTool(manager)
	args, _ := json.Marshal(ReadSkillInput{SkillName: "same-name"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil { t.Fatal(err) }
	if result.Success || resolver.loaded == ref { t.Fatalf("legacy name must resolve preloaded-only: %+v", result) }
}
