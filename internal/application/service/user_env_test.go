package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

const userEnvTenantID = uint64(7)

// userEnvCtx builds the only context these calls trust: the workspace and the
// identity both come from it, and nothing in a request can influence either.
func userEnvCtx(tenantID uint64, principalID string) context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)
	return types.WithPrincipal(ctx, types.Principal{
		Type: types.PrincipalWebUser, ID: principalID,
	})
}

// userEnvConfigRepo is the sandbox-config slice UserEnvService needs: names for
// the listing, and a workspace-scoped lookup that refuses a foreign config.
type userEnvConfigRepo struct {
	rows []*types.TenantSandboxConfigEntity
}

func (r *userEnvConfigRepo) Create(context.Context, *types.TenantSandboxConfigEntity) error {
	panic("Create is outside the user env surface")
}

func (r *userEnvConfigRepo) GetByID(
	_ context.Context, tenantID uint64, id string,
) (*types.TenantSandboxConfigEntity, error) {
	for _, row := range r.rows {
		if row.TenantID == tenantID && row.ID == id {
			return row, nil
		}
	}
	return nil, nil
}

func (r *userEnvConfigRepo) ListByTenant(
	_ context.Context, tenantID uint64,
) ([]*types.TenantSandboxConfigEntity, error) {
	var out []*types.TenantSandboxConfigEntity
	for _, row := range r.rows {
		if row.TenantID == tenantID {
			out = append(out, row)
		}
	}
	return out, nil
}

func (r *userEnvConfigRepo) ListAll(context.Context) ([]*types.TenantSandboxConfigEntity, error) {
	panic("ListAll is outside the user env surface")
}
func (r *userEnvConfigRepo) Update(context.Context, *types.TenantSandboxConfigEntity) error {
	panic("Update is outside the user env surface")
}
func (r *userEnvConfigRepo) SoftDelete(context.Context, uint64, string) error {
	panic("SoftDelete is outside the user env surface")
}
func (r *userEnvConfigRepo) SetCordon(context.Context, uint64, string, time.Time) error {
	panic("SetCordon is outside the user env surface")
}
func (r *userEnvConfigRepo) ClearCordon(context.Context, uint64, string) error {
	panic("ClearCordon is outside the user env surface")
}

var _ repository.TenantSandboxConfigRepository = (*userEnvConfigRepo)(nil)

// newUserEnvFixture seeds one workspace whose skills cover every case the
// listing has to decide: the one that is exposed, the disabled one, the one
// still installing, the one declaring nothing, and one belonging to somebody
// else's workspace.
func newUserEnvFixture(t *testing.T) (*UserEnvService, *installSkillRepo) {
	t.Helper()
	repo := newInstallSkillRepo()
	ctx := context.Background()
	for _, skill := range []*types.TenantSkillEntity{
		{
			ID: "sk-ready", TenantID: userEnvTenantID, SandboxConfigID: "cfg-1",
			Name: "pdf-tools", Description: "Extracts text from PDFs",
			Enabled: true, Status: types.SkillStatusReady,
			Envs: types.SkillEnvVars{
				{Name: "API_TOKEN", Description: "workspace token", Required: true, Value: "admin-secret"},
				{Name: "USER_TOKEN", Description: "your own token", Required: true},
				{Name: "OPTIONAL_HINT", Description: "nice to have"},
			},
		},
		{
			ID: "sk-no-declaration", TenantID: userEnvTenantID, SandboxConfigID: "cfg-1",
			Name: "plain", Enabled: true, Status: types.SkillStatusReady,
		},
		{
			ID: "sk-disabled", TenantID: userEnvTenantID, SandboxConfigID: "cfg-1",
			Name: "hidden", Enabled: false, Status: types.SkillStatusReady,
			Envs: types.SkillEnvVars{{Name: "HIDDEN_TOKEN"}},
		},
		{
			ID: "sk-installing", TenantID: userEnvTenantID, SandboxConfigID: "cfg-2",
			Name: "half-done", Enabled: true, Status: types.SkillStatusInstalling,
			Envs: types.SkillEnvVars{{Name: "HALF_TOKEN"}},
		},
		{
			ID: "sk-foreign", TenantID: 8, SandboxConfigID: "cfg-9",
			Name: "theirs", Enabled: true, Status: types.SkillStatusReady,
			Envs: types.SkillEnvVars{{Name: "THEIR_TOKEN"}},
		},
	} {
		require.NoError(t, repo.CreateSkill(ctx, skill))
	}
	configs := &userEnvConfigRepo{rows: []*types.TenantSandboxConfigEntity{
		{ID: "cfg-1", TenantID: userEnvTenantID, Name: "Production", Description: "Prod cluster"},
		{ID: "cfg-2", TenantID: userEnvTenantID, Name: "Staging"},
		{ID: "cfg-9", TenantID: 8, Name: "Theirs"},
	}}
	return NewUserEnvService(repo, configs), repo
}

func configByID(t *testing.T, groups []ConfigEnvGroup, configID string) ConfigEnvGroup {
	t.Helper()
	for _, g := range groups {
		if g.SandboxConfigID == configID {
			return g
		}
	}
	t.Fatalf("no group for config %q (%+v)", configID, groups)
	return ConfigEnvGroup{}
}

func skillByID(t *testing.T, group ConfigEnvGroup, skillID string) SkillEnvGroup {
	t.Helper()
	for _, s := range group.Skills {
		if s.SkillID == skillID {
			return s
		}
	}
	t.Fatalf("no skill %q on config %q (%+v)", skillID, group.SandboxConfigID, group.Skills)
	return SkillEnvGroup{}
}

func viewByName(t *testing.T, vars []EnvVarView, name string) EnvVarView {
	t.Helper()
	for _, v := range vars {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("no variable %q (%+v)", name, vars)
	return EnvVarView{}
}

func TestListMineReportsTheThreeSourceStates(t *testing.T) {
	svc, _ := newUserEnvFixture(t)
	ctx := userEnvCtx(userEnvTenantID, "alice")
	require.NoError(t, svc.SetMineSkill(ctx, "sk-ready", "USER_TOKEN", "alice-secret"))

	groups, err := svc.ListMine(ctx)
	require.NoError(t, err)
	skill := skillByID(t, configByID(t, groups, "cfg-1"), "sk-ready")
	require.Equal(t, "pdf-tools", skill.SkillName)
	require.Equal(t, "Extracts text from PDFs", skill.Description)

	workspace := viewByName(t, skill.Vars, "API_TOKEN")
	require.Equal(t, EnvSourceWorkspace, workspace.Source)
	require.Equal(t, "workspace token", workspace.Description)
	require.True(t, workspace.Required)

	own := viewByName(t, skill.Vars, "USER_TOKEN")
	require.Equal(t, EnvSourceUser, own.Source)
	require.NotNil(t, own.UpdatedAt)

	require.Equal(t, EnvSourceUnset, viewByName(t, skill.Vars, "OPTIONAL_HINT").Source)
}

// Every config is listed so the config-wide editor is always reachable, even on
// a config carrying no skills at all.
func TestListMineListsEveryConfigWithItsName(t *testing.T) {
	svc, _ := newUserEnvFixture(t)

	groups, err := svc.ListMine(userEnvCtx(userEnvTenantID, "alice"))

	require.NoError(t, err)
	require.Len(t, groups, 2)
	require.Equal(t, "Production", groups[0].SandboxConfigName)
	require.Equal(t, "Prod cluster", groups[0].Description)
	require.Equal(t, "Staging", groups[1].SandboxConfigName)
	require.Empty(t, groups[1].Description)
	require.Empty(t, configByID(t, groups, "cfg-2").Skills)
}

// Both lists must marshal as [] rather than null: the settings page reads
// .length off them, and a null blanks the whole page.
func TestListMineNeverReturnsNullLists(t *testing.T) {
	svc, _ := newUserEnvFixture(t)

	groups, err := svc.ListMine(userEnvCtx(userEnvTenantID, "alice"))

	require.NoError(t, err)
	require.NotEmpty(t, groups)
	for _, group := range groups {
		require.NotNil(t, group.Vars, "config %s", group.SandboxConfigID)
		require.NotNil(t, group.Skills, "config %s", group.SandboxConfigID)
		for _, skill := range group.Skills {
			require.NotNil(t, skill.Vars, "skill %s", skill.SkillID)
		}
	}
}

// A disabled or half-installed skill is never handed to the agent, and a skill
// declaring nothing has nothing to ask for.
func TestListMineOnlyListsReadySkillsThatDeclaredSomething(t *testing.T) {
	svc, _ := newUserEnvFixture(t)

	groups, err := svc.ListMine(userEnvCtx(userEnvTenantID, "alice"))

	require.NoError(t, err)
	cfg1 := configByID(t, groups, "cfg-1")
	require.Len(t, cfg1.Skills, 1)
	require.Equal(t, "sk-ready", cfg1.Skills[0].SkillID)
}

func TestListMineReportsMyConfigWideVariables(t *testing.T) {
	svc, _ := newUserEnvFixture(t)
	ctx := userEnvCtx(userEnvTenantID, "alice")
	require.NoError(t, svc.SetMineSandbox(ctx, "cfg-1", "HTTP_PROXY", "http://proxy:8080"))

	groups, err := svc.ListMine(ctx)
	require.NoError(t, err)
	cfg1 := configByID(t, groups, "cfg-1")

	require.Len(t, cfg1.Vars, 1)
	require.Equal(t, "HTTP_PROXY", cfg1.Vars[0].Name)
	require.Equal(t, EnvSourceUser, cfg1.Vars[0].Source)
	require.NotNil(t, cfg1.Vars[0].UpdatedAt)
}

func TestListMineDoesNotLeakAnotherPrincipalsValues(t *testing.T) {
	svc, _ := newUserEnvFixture(t)
	alice := userEnvCtx(userEnvTenantID, "alice")
	require.NoError(t, svc.SetMineSkill(alice, "sk-ready", "USER_TOKEN", "alice-secret"))
	require.NoError(t, svc.SetMineSandbox(alice, "cfg-1", "HTTP_PROXY", "alice-proxy"))

	groups, err := svc.ListMine(userEnvCtx(userEnvTenantID, "bob"))
	require.NoError(t, err)
	cfg1 := configByID(t, groups, "cfg-1")

	require.Empty(t, cfg1.Vars, "bob must not even see that alice added a name")
	skill := skillByID(t, cfg1, "sk-ready")
	require.Equal(t, EnvSourceUnset, viewByName(t, skill.Vars, "USER_TOKEN").Source)
	require.Equal(t, EnvSourceWorkspace, viewByName(t, skill.Vars, "API_TOKEN").Source)
}

// Free-form names belong to the config-wide scope; a skill credential exists
// only because the skill declared it.
func TestSetMineSkillRefusesAnUndeclaredName(t *testing.T) {
	svc, repo := newUserEnvFixture(t)

	err := svc.SetMineSkill(
		userEnvCtx(userEnvTenantID, "alice"), "sk-ready", "INVENTED_TOKEN", "v")

	require.Error(t, err)
	require.Empty(t, repo.userEnvs)
}

func TestSetMineRejectsUnusableNames(t *testing.T) {
	svc, repo := newUserEnvFixture(t)
	ctx := userEnvCtx(userEnvTenantID, "alice")

	for _, name := range []string{"PATH", "WEKNORA_SKILL_OUTPUT_DIR", "lower_case", "HAS SPACE", ""} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, svc.SetMineSandbox(ctx, "cfg-1", name, "whatever"))
		})
	}
	require.Empty(t, repo.userEnvs)
}

func TestSetMineRejectsAnOversizedValue(t *testing.T) {
	svc, repo := newUserEnvFixture(t)

	err := svc.SetMineSandbox(userEnvCtx(userEnvTenantID, "alice"), "cfg-1", "TOKEN",
		strings.Repeat("x", MaxEnvValueBytes+1))

	require.Error(t, err)
	require.Empty(t, repo.userEnvs)
}

// The quota bounds how many names one principal keeps in one scope, so a user
// already at the limit must still be able to rotate a key they hold.
func TestSetMineEnforcesTheQuotaButStillAllowsRotation(t *testing.T) {
	svc, _ := newUserEnvFixture(t)
	ctx := userEnvCtx(userEnvTenantID, "alice")
	for i := 0; i < MaxUserEnvVarsPerScope; i++ {
		require.NoError(t, svc.SetMineSandbox(ctx, "cfg-1", envNameForIndex(i), "v"))
	}

	require.Error(t, svc.SetMineSandbox(ctx, "cfg-1", "ONE_TOO_MANY", "v"))
	require.NoError(t, svc.SetMineSandbox(ctx, "cfg-1", envNameForIndex(0), "rotated"))
}

func envNameForIndex(i int) string {
	return "VAR_" + string(rune('A'+i/26)) + string(rune('A'+i%26))
}

// Without this check a member could write rows against another workspace's
// skill ID, which the resolver would then inject into that workspace's run.
func TestSetMineRefusesASkillOfAnotherWorkspace(t *testing.T) {
	svc, repo := newUserEnvFixture(t)

	err := svc.SetMineSkill(userEnvCtx(userEnvTenantID, "alice"), "sk-foreign", "THEIR_TOKEN", "v")

	require.Error(t, err)
	require.Empty(t, repo.userEnvs)
}

func TestSetMineSandboxRefusesAConfigOfAnotherWorkspace(t *testing.T) {
	svc, repo := newUserEnvFixture(t)

	err := svc.SetMineSandbox(userEnvCtx(userEnvTenantID, "alice"), "cfg-9", "TOKEN", "v")

	require.Error(t, err)
	require.Empty(t, repo.userEnvs)
}

// A missing principal is an error, never a default: falling back would let one
// identity's values be written under another's key.
func TestSetMineRequiresAPrincipal(t *testing.T) {
	svc, repo := newUserEnvFixture(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, userEnvTenantID)

	require.Error(t, svc.SetMineSkill(ctx, "sk-ready", "USER_TOKEN", "v"))
	require.Error(t, svc.SetMineSandbox(ctx, "cfg-1", "TOKEN", "v"))
	require.Empty(t, repo.userEnvs)

	_, err := svc.ListMine(ctx)
	require.Error(t, err)
	require.Error(t, svc.DeleteMineSkill(ctx, "sk-ready", "USER_TOKEN"))
	require.Error(t, svc.DeleteMineSandbox(ctx, "cfg-1", "TOKEN"))
}

func TestDeleteMineReportsNothingToDeleteTheSecondTime(t *testing.T) {
	svc, _ := newUserEnvFixture(t)
	ctx := userEnvCtx(userEnvTenantID, "alice")
	require.NoError(t, svc.SetMineSkill(ctx, "sk-ready", "USER_TOKEN", "alice-secret"))
	require.NoError(t, svc.SetMineSandbox(ctx, "cfg-1", "HTTP_PROXY", "p"))

	require.NoError(t, svc.DeleteMineSkill(ctx, "sk-ready", "USER_TOKEN"))
	require.True(t, errors.Is(
		svc.DeleteMineSkill(ctx, "sk-ready", "USER_TOKEN"), types.ErrEnvVarNotFound))

	require.NoError(t, svc.DeleteMineSandbox(ctx, "cfg-1", "HTTP_PROXY"))
	require.True(t, errors.Is(
		svc.DeleteMineSandbox(ctx, "cfg-1", "HTTP_PROXY"), types.ErrEnvVarNotFound))
}

// Deleting is scoped to the caller's own rows, so one member's revocation
// cannot touch another's value for the same variable.
func TestDeleteMineTouchesOnlyTheCallersOwnValue(t *testing.T) {
	svc, repo := newUserEnvFixture(t)
	alice := userEnvCtx(userEnvTenantID, "alice")
	bob := userEnvCtx(userEnvTenantID, "bob")
	require.NoError(t, svc.SetMineSkill(alice, "sk-ready", "USER_TOKEN", "alice-secret"))
	require.NoError(t, svc.SetMineSkill(bob, "sk-ready", "USER_TOKEN", "bob-secret"))

	require.NoError(t, svc.DeleteMineSkill(bob, "sk-ready", "USER_TOKEN"))

	require.Len(t, repo.userEnvs, 1)
	require.Equal(t, "alice", repo.userEnvs[0].PrincipalID)
	require.Equal(t, "alice-secret", repo.userEnvs[0].Value)
}

// Revoking must keep working after an admin disables the skill, which is
// precisely when somebody is most likely to want the credential gone.
func TestDeleteMineSkillWorksAfterTheSkillIsDisabled(t *testing.T) {
	svc, repo := newUserEnvFixture(t)
	ctx := userEnvCtx(userEnvTenantID, "alice")
	require.NoError(t, svc.SetMineSkill(ctx, "sk-ready", "USER_TOKEN", "alice-secret"))
	skill, err := repo.GetSkill(context.Background(), userEnvTenantID, "cfg-1", "sk-ready")
	require.NoError(t, err)
	skill.Enabled = false
	require.NoError(t, repo.UpdateSkill(context.Background(), skill))

	require.NoError(t, svc.DeleteMineSkill(ctx, "sk-ready", "USER_TOKEN"))
	require.Empty(t, repo.userEnvs)
}

// The two scopes are independent rows, so the same name can be held in both.
func TestSetMineKeepsTheTwoScopesApart(t *testing.T) {
	svc, repo := newUserEnvFixture(t)
	ctx := userEnvCtx(userEnvTenantID, "alice")

	require.NoError(t, svc.SetMineSandbox(ctx, "cfg-1", "API_TOKEN", "config-wide"))
	require.NoError(t, svc.SetMineSkill(ctx, "sk-ready", "API_TOKEN", "skill-scoped"))

	require.Len(t, repo.userEnvs, 2)
}

func userEnvByName(t *testing.T, rows []*types.TenantUserEnvVar, name string) *types.TenantUserEnvVar {
	t.Helper()
	for _, row := range rows {
		if row != nil && row.Name == name && row.SkillID == "sk-ready" {
			return row
		}
	}
	t.Fatalf("no skill-scoped row for %q (%+v)", name, rows)
	return nil
}

func TestCaptureSkillEnvWritesDeclaredNamesForTheSpeaker(t *testing.T) {
	svc, repo := newUserEnvFixture(t)
	ctx := userEnvCtx(userEnvTenantID, "alice")

	require.NoError(t, svc.CaptureSkillEnv(ctx, "cfg-1", "pdf-tools", map[string]string{
		"USER_TOKEN": "alice-from-chat",
	}))

	require.Len(t, repo.userEnvs, 1)
	got := repo.userEnvs[0]
	require.Equal(t, "alice", got.PrincipalID)
	require.Equal(t, "sk-ready", got.SkillID)
	require.Equal(t, "cfg-1", got.SandboxConfigID)
	require.Equal(t, "USER_TOKEN", got.Name)
	require.Equal(t, "alice-from-chat", got.Value)
}

func TestCaptureSkillEnvSkipsUndeclaredAndReservedNames(t *testing.T) {
	svc, repo := newUserEnvFixture(t)

	require.NoError(t, svc.CaptureSkillEnv(
		userEnvCtx(userEnvTenantID, "alice"), "cfg-1", "pdf-tools", map[string]string{
			"USER_TOKEN":        "keep-me",
			"INVENTED":          "no",
			"PATH":              "/bin",
			"WEKNORA_SKILL_DIR": "no",
		}))

	require.Len(t, repo.userEnvs, 1)
	require.Equal(t, "USER_TOKEN", repo.userEnvs[0].Name)
	require.Equal(t, "keep-me", repo.userEnvs[0].Value)
}

func TestCaptureSkillEnvDoesNotCopyAdminValueIntoUserSlot(t *testing.T) {
	svc, repo := newUserEnvFixture(t)

	require.NoError(t, svc.CaptureSkillEnv(
		userEnvCtx(userEnvTenantID, "alice"), "cfg-1", "pdf-tools", map[string]string{
			"API_TOKEN": "admin-secret",
		}))

	require.Empty(t, repo.userEnvs)
}

// A workspace value is one an admin typed. A value read out of a command the
// model composed must not displace it for this caller — that is what the
// settings page is for.
func TestCaptureSkillEnvLeavesTheAdminValueInPlace(t *testing.T) {
	svc, repo := newUserEnvFixture(t)

	require.NoError(t, svc.CaptureSkillEnv(
		userEnvCtx(userEnvTenantID, "alice"), "cfg-1", "pdf-tools", map[string]string{
			"API_TOKEN": "alice-override",
		}))

	require.Empty(t, repo.userEnvs)
}

func TestCaptureSkillEnvSkipsWhenUserValueAlreadyMatches(t *testing.T) {
	svc, repo := newUserEnvFixture(t)
	ctx := userEnvCtx(userEnvTenantID, "alice")
	require.NoError(t, svc.SetMineSkill(ctx, "sk-ready", "USER_TOKEN", "already-mine"))
	require.Len(t, repo.userEnvs, 1)

	require.NoError(t, svc.CaptureSkillEnv(ctx, "cfg-1", "pdf-tools", map[string]string{
		"USER_TOKEN": "already-mine",
	}))

	require.Len(t, repo.userEnvs, 1)
	require.Equal(t, "already-mine", repo.userEnvs[0].Value)
}

// Capture fills blanks and nothing else. A prompt injection, a hallucination or
// one `export TOKEN=test` must not be able to replace a key its owner entered,
// which nobody would notice until every later turn authenticated as nobody.
func TestCaptureSkillEnvNeverOverwritesAStoredUserValue(t *testing.T) {
	svc, repo := newUserEnvFixture(t)
	ctx := userEnvCtx(userEnvTenantID, "alice")
	require.NoError(t, svc.SetMineSkill(ctx, "sk-ready", "USER_TOKEN", "old-key"))

	require.NoError(t, svc.CaptureSkillEnv(ctx, "cfg-1", "pdf-tools", map[string]string{
		"USER_TOKEN": "new-key",
	}))

	require.Len(t, repo.userEnvs, 1)
	require.Equal(t, "old-key", repo.userEnvs[0].Value)
}

func TestCaptureSkillEnvIgnoresUnknownOrUnusableSkills(t *testing.T) {
	svc, repo := newUserEnvFixture(t)
	ctx := userEnvCtx(userEnvTenantID, "alice")

	require.NoError(t, svc.CaptureSkillEnv(ctx, "cfg-1", "", map[string]string{"USER_TOKEN": "v"}))
	require.NoError(t, svc.CaptureSkillEnv(ctx, "cfg-1", "no-such-skill", map[string]string{"USER_TOKEN": "v"}))
	require.NoError(t, svc.CaptureSkillEnv(ctx, "cfg-1", "plain", map[string]string{"USER_TOKEN": "v"}))
	require.NoError(t, svc.CaptureSkillEnv(ctx, "cfg-1", "hidden", map[string]string{"HIDDEN_TOKEN": "v"}))
	require.NoError(t, svc.CaptureSkillEnv(ctx, "cfg-2", "half-done", map[string]string{"HALF_TOKEN": "v"}))

	require.Empty(t, repo.userEnvs)
}
