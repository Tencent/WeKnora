package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// effectiveFixture is one sandbox config whose image is usable, carrying one
// skill of every lifecycle state.
type effectiveFixture struct {
	configs *installConfigRepo
	skills  *installSkillRepo
}

// usableSkillImageConfig is a config whose stored snapshot is the image its
// sessions boot, which is the precondition for offering any installed skill.
func usableSkillImageConfig(id string, tenantID uint64) *types.TenantSandboxConfigEntity {
	fingerprint := sandbox.SkillImageFingerprint("e2b", "key-1", "https://e2b.example")
	return &types.TenantSandboxConfigEntity{
		ID:          id,
		TenantID:    tenantID,
		SandboxType: string(sandbox.SandboxTypeE2B),
		Config: &types.TenantSandboxConfig{
			SandboxType: string(sandbox.SandboxTypeE2B),
			E2B: &types.E2BSandboxConfig{
				APIURL: "https://e2b.example", APIKey: "key-1", TemplateID: "base-template",
			},
			SkillImage: &types.SkillImageConfig{
				SnapshotID: "snap-3", Generation: 3,
				BaseTemplateID: "base-template", OwnerFingerprint: fingerprint,
			},
		},
	}
}

func newEffectiveFixture(t *testing.T) *effectiveFixture {
	t.Helper()
	fx := &effectiveFixture{
		configs: &installConfigRepo{entity: usableSkillImageConfig("cfg-1", 7)},
		skills:  newInstallSkillRepo(),
	}
	rows := []*types.TenantSkillEntity{
		{ID: "sk-1", Name: "ready-enabled", Status: types.SkillStatusReady, Enabled: true},
		{ID: "sk-2", Name: "ready-disabled", Status: types.SkillStatusReady, Enabled: false},
		{ID: "sk-3", Name: "installing", Status: types.SkillStatusInstalling, Enabled: true},
		{ID: "sk-4", Name: "failed", Status: types.SkillStatusFailed, Enabled: true},
		{ID: "sk-5", Name: "removing", Status: types.SkillStatusRemoving, Enabled: true},
		{ID: "sk-6", Name: "second-ready", Status: types.SkillStatusReady, Enabled: true},
	}
	for _, row := range rows {
		row.TenantID = 7
		row.SandboxConfigID = "cfg-1"
		require.NoError(t, fx.skills.CreateSkill(context.Background(), row))
	}
	return fx
}

func (fx *effectiveFixture) derive(ctx context.Context) []*types.TenantSkillEntity {
	return effectiveTenantSkills(ctx, fx.configs, fx.skills, 7, "cfg-1")
}

func skillNames(rows []*types.TenantSkillEntity) []string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}
	return names
}

func TestEffectiveTenantSkillsKeepsOnlyReadyAndEnabledSkills(t *testing.T) {
	fx := newEffectiveFixture(t)

	got := fx.derive(context.Background())

	require.ElementsMatch(t, []string{"ready-enabled", "second-ready"}, skillNames(got))
}

func TestListUsableSkillsMatchesTheEffectiveSet(t *testing.T) {
	fx := newEffectiveFixture(t)
	svc := &TenantSkillService{configs: fx.configs, skills: fx.skills}

	got := svc.ListUsableSkills(context.Background(), 7, "cfg-1")

	require.ElementsMatch(t, []string{"ready-enabled", "second-ready"}, skillNames(got))
}

// The image is what carries the skills. When the live credentials cannot
// resolve the stored snapshot, sessions boot the base template, which has none
// of them - so announcing any would burn turns on calls that cannot work.
func TestEffectiveTenantSkillsInjectsNothingWhenTheFingerprintDisagrees(t *testing.T) {
	fx := newEffectiveFixture(t)
	fx.configs.entity.Config.E2B.APIKey = "rotated-key"

	require.Empty(t, fx.derive(context.Background()))
}

func TestEffectiveTenantSkillsInjectsNothingWhenTheBackendCannotSnapshot(t *testing.T) {
	fx := newEffectiveFixture(t)
	fx.configs.entity.SandboxType = "local"
	fx.configs.entity.Config.SandboxType = "local"

	require.Empty(t, fx.derive(context.Background()))
}

func TestEffectiveTenantSkillsInjectsReadySkillsOnDocker(t *testing.T) {
	fx := newEffectiveFixture(t)
	host := "unix:///var/run/docker.sock"
	fx.configs.entity.SandboxType = "docker"
	fx.configs.entity.Config.SandboxType = "docker"
	fx.configs.entity.Config.E2B = nil
	fx.configs.entity.Config.Docker = &types.DockerSandboxConfig{Image: "weknora/sandbox:base", Host: host}
	fx.configs.entity.Config.SkillImage = &types.SkillImageConfig{
		SnapshotID:       "weknora-skill/weknora-sk-cfg1-g1",
		Generation:       1,
		OwnerFingerprint: sandbox.SkillImageFingerprint("docker", "", host),
	}

	require.ElementsMatch(t, []string{"ready-enabled", "second-ready"},
		skillNames(fx.derive(context.Background())))
}

func TestEffectiveTenantSkillsInjectsNothingWithoutAnImage(t *testing.T) {
	fx := newEffectiveFixture(t)
	fx.configs.entity.Config.SkillImage = nil

	require.Empty(t, fx.derive(context.Background()))
}

func TestEffectiveTenantSkillsInjectsNothingWithoutASelectedConfig(t *testing.T) {
	fx := newEffectiveFixture(t)

	require.Empty(t, effectiveTenantSkills(
		context.Background(), fx.configs, fx.skills, 7, ""))
	require.Empty(t, effectiveTenantSkills(
		context.Background(), fx.configs, fx.skills, 7, "cfg-other"),
		"a config that does not exist for this workspace has no skills")
	require.Empty(t, effectiveTenantSkills(
		context.Background(), fx.configs, fx.skills, 9, "cfg-1"),
		"another workspace must not see these skills")
}

// failingConfigRepo is the config read erroring the way the gorm repository
// does on a dead connection or a cancelled query.
//
// It hands back the entity as well as the error on purpose: a scan that fails
// part way leaves the destination struct populated, so the error - not the
// emptiness of the result - has to be what stops the derivation.
type failingConfigRepo struct {
	entity *types.TenantSandboxConfigEntity
	err    error
}

func (r failingConfigRepo) GetByID(
	context.Context, uint64, string,
) (*types.TenantSandboxConfigEntity, error) {
	return r.entity, r.err
}

// Without the config we cannot tell whether the image sessions boot carries
// these skills, so the derivation must stop there rather than list rows and
// offer them.
func TestEffectiveTenantSkillsInjectsNothingWhenTheConfigLookupFails(t *testing.T) {
	fx := newEffectiveFixture(t)

	failing := failingConfigRepo{
		entity: usableSkillImageConfig("cfg-1", 7),
		err:    errors.New("connection refused"),
	}

	require.Empty(t, effectiveTenantSkills(
		context.Background(), failing, fx.skills, 7, "cfg-1"))
	require.Zero(t, fx.skills.listCalls,
		"a config we could not read says nothing about which skills are usable")
}

// The other half of the same rule, one step later: the config read succeeded
// and listing the rows is what failed.
func TestEffectiveTenantSkillsInjectsNothingWhenTheSkillListingFails(t *testing.T) {
	fx := newEffectiveFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.Empty(t, fx.derive(ctx))
	require.Equal(t, 1, fx.skills.listCalls)
}

// multiConfigRepo answers for several sandbox configs at once, which the
// install fake - built around the single config an install runs against -
// cannot do.
type multiConfigRepo struct {
	entities map[string]*types.TenantSandboxConfigEntity
}

func (r *multiConfigRepo) GetByID(
	_ context.Context, tenantID uint64, id string,
) (*types.TenantSandboxConfigEntity, error) {
	entity, ok := r.entities[id]
	if !ok || entity.TenantID != tenantID {
		return nil, nil
	}
	return entity, nil
}

// twoConfigFixture holds two usable configs with one ready skill each, so a
// test can tell which of them the derivation actually used.
func twoConfigFixture(t *testing.T) (*multiConfigRepo, *installSkillRepo) {
	t.Helper()
	configs := &multiConfigRepo{entities: map[string]*types.TenantSandboxConfigEntity{
		"cfg-a": usableSkillImageConfig("cfg-a", 7),
		"cfg-b": usableSkillImageConfig("cfg-b", 7),
	}}
	skills := newInstallSkillRepo()
	for configID, name := range map[string]string{"cfg-a": "skill-of-a", "cfg-b": "skill-of-b"} {
		require.NoError(t, skills.CreateSkill(context.Background(), &types.TenantSkillEntity{
			ID: "sk-" + configID, Name: name, TenantID: 7, SandboxConfigID: configID,
			Status: types.SkillStatusReady, Enabled: true,
		}))
	}
	return configs, skills
}

// A session's sandbox is long-lived and boots the config it was pinned to. If
// the derivation followed the agent's current choice instead, re-pointing an
// agent mid-conversation would announce skills the running image does not
// carry - the exact hole this derivation exists to close.
func TestSkillsForRunPrefersThePinnedConfigOverTheAgents(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	ctx := context.Background()
	_, err := pinner.Pin(ctx, "s-1", "cfg-a")
	require.NoError(t, err)
	configs, skills := twoConfigFixture(t)

	configID, rows := skillsForRun(ctx, pinner, configs, skills, 7, "s-1", "cfg-b")

	require.Equal(t, "cfg-a", configID)
	require.Equal(t, []string{"skill-of-a"}, skillNames(rows))
}

// Without a pin the session has no sandbox yet, so its first one will boot the
// agent's config.
func TestSkillsForRunFallsBackToTheAgentConfigWhenUnpinned(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	configs, skills := twoConfigFixture(t)

	configID, rows := skillsForRun(
		context.Background(), pinner, configs, skills, 7, "s-1", "cfg-b")

	require.Equal(t, "cfg-b", configID)
	require.Equal(t, []string{"skill-of-b"}, skillNames(rows))
}

// When the pin cannot be read we do not know which image boots, and guessing
// the agent's config is exactly the wrong guess for a re-pointed agent. Offer
// nothing.
func TestSkillsForRunOffersNothingWhenThePinCannotBeRead(t *testing.T) {
	db := newPinTestDB(t)
	pinner := NewSessionSandboxPinner(db)
	require.NoError(t, db.Migrator().DropTable(&types.Session{}))
	configs, skills := twoConfigFixture(t)

	configID, rows := skillsForRun(
		context.Background(), pinner, configs, skills, 7, "s-1", "cfg-b")

	require.Empty(t, configID)
	require.Empty(t, rows)
	require.Zero(t, skills.listCalls)
}

func TestEffectiveTenantSkillsToleratesMissingDependencies(t *testing.T) {
	fx := newEffectiveFixture(t)

	require.Empty(t, effectiveTenantSkills(context.Background(), nil, fx.skills, 7, "cfg-1"))
	require.Empty(t, effectiveTenantSkills(context.Background(), fx.configs, nil, 7, "cfg-1"))
}
