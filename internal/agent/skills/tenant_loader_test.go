package skills

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/skillpkg"
	"github.com/Tencent/WeKnora/internal/skillrunner"
	"github.com/Tencent/WeKnora/internal/types"
)

type tenantLoaderRepoStub struct {
	skill   *types.TenantSkill
	version *types.TenantSkillVersion
	audits  int
}

func (repo *tenantLoaderRepoStub) GetByID(context.Context, uint64, string) (*types.TenantSkill, error) {
	return repo.skill, nil
}
func (repo *tenantLoaderRepoStub) GetCurrentVersion(context.Context, uint64, string) (*types.TenantSkillVersion, error) {
	return repo.version, nil
}
func (repo *tenantLoaderRepoStub) CreateExecutionAudit(context.Context, *types.SkillExecutionAudit) error {
	repo.audits++
	return nil
}

type tenantRunnerStub struct{ request skillrunner.ExecuteRequest }

func (runner *tenantRunnerStub) Execute(_ context.Context, request skillrunner.ExecuteRequest) (skillrunner.ExecuteResponse, error) {
	runner.request = request
	return skillrunner.ExecuteResponse{ExitCode: 0, Stdout: "ok"}, nil
}
func (*tenantLoaderRepoStub) FinishExecutionAudit(context.Context, uint64, string, types.ExecutionAuditFinish) error {
	return nil
}

type tenantLoaderStorageStub struct{}

func (tenantLoaderStorageStub) Stage(context.Context, uint64, string, io.Reader, int64) (*skillpkg.ValidatedPackage, error) {
	return nil, nil
}
func (tenantLoaderStorageStub) Materialize(context.Context, uint64, string, string, *skillpkg.ValidatedPackage) (string, string, error) {
	return "", "", nil
}
func (tenantLoaderStorageStub) VerifyVersion(context.Context, uint64, *types.TenantSkillVersion) error {
	return nil
}
func (tenantLoaderStorageStub) RemoveVersion(context.Context, uint64, *types.TenantSkillVersion) error {
	return nil
}
func (tenantLoaderStorageStub) Reconcile(context.Context, time.Time) error { return nil }

func TestTenantLoaderRejectsDisabledSkillDespitePriorEnabledState(t *testing.T) {
	repo := &tenantLoaderRepoStub{
		skill:   &types.TenantSkill{ID: "skill", TenantID: 7, Status: types.TenantSkillEnabled},
		version: &types.TenantSkillVersion{ID: "version", SkillID: "skill", ContentHash: "hash"},
	}
	loader := NewTenantLoader(repo, tenantLoaderStorageStub{}, t.TempDir(), NewLoader(nil))
	ref := types.SkillReference{Source: types.SkillSourceTenant, SkillID: "skill"}
	repo.skill.Status = types.TenantSkillDisabled
	if _, err := loader.LoadInstructions(context.Background(), RuntimeScope{TenantID: 7, Allowed: []types.SkillReference{ref}}, ref); err != ErrSkillDisabled {
		t.Fatalf("expected ErrSkillDisabled after runtime reauthorization, got %v", err)
	}
}

func TestLegacySelectedSkillsMigrateOnlyToPreloaded(t *testing.T) {
	refs, invalid := MigrateLegacySkillRefs([]string{"citation-generator", "unknown"}, []*SkillMetadata{{Name: "citation-generator"}})
	if len(refs) != 1 || refs[0].Source != types.SkillSourcePreloaded || refs[0].SkillID != "citation-generator" {
		t.Fatalf("unexpected refs: %+v", refs)
	}
	if len(invalid) != 1 || invalid[0] != "unknown" {
		t.Fatalf("unexpected invalid names: %v", invalid)
	}
}

func TestTenantResolverReauthorizesAndAuditsExecution(t *testing.T) {
	repo := &tenantLoaderRepoStub{
		skill:   &types.TenantSkill{ID: "skill", TenantID: 7, Status: types.TenantSkillEnabled},
		version: &types.TenantSkillVersion{ID: "version", SkillID: "skill", ContentHash: "hash", ManifestJSON: []byte(`{"scripts":["scripts/run.py"]}`)},
	}
	runner := &tenantRunnerStub{}
	resolver := NewTenantResolver(NewTenantLoader(repo, tenantLoaderStorageStub{}, t.TempDir(), NewLoader(nil)), repo, runner)
	ref := types.SkillReference{Source: types.SkillSourceTenant, SkillID: "skill"}
	result, err := resolver.Execute(context.Background(), RuntimeScope{TenantID: 7, UserID: "user", Allowed: []types.SkillReference{ref}}, ref, "scripts/run.py", []string{"--safe"}, "input")
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "ok" || repo.audits != 1 || runner.request.TenantID != "7" || runner.request.SkillID != "skill" {
		t.Fatalf("unexpected execution: result=%+v audits=%d request=%+v", result, repo.audits, runner.request)
	}
}
