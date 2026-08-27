package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestListSkillsReturnsTheConfigsSkills(t *testing.T) {
	fx := newInstallFixture(t)

	skills, err := fx.svc.ListSkills(context.Background(), 7, "cfg-1")
	require.NoError(t, err)
	require.Len(t, skills, 1)
	require.Equal(t, "sk-1", skills[0].ID)
}

// A config another workspace owns must be unreachable, not merely empty: an
// empty list would tell the caller the config exists.
func TestListSkillsRefusesAConfigOfAnotherWorkspace(t *testing.T) {
	fx := newInstallFixture(t)

	_, err := fx.svc.ListSkills(context.Background(), 8, "cfg-1")
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	require.Equal(t, 404, appErr.HTTPCode)
}

func TestGetSkillIsScopedToWorkspaceAndConfig(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()

	skill, err := fx.svc.GetSkill(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.NotNil(t, skill)

	for _, tc := range []struct {
		name     string
		tenantID uint64
		configID string
	}{
		{"another workspace", 8, "cfg-1"},
		{"another config", 7, "cfg-2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found, err := fx.svc.GetSkill(ctx, tc.tenantID, tc.configID, "sk-1")
			require.NoError(t, err)
			require.Nil(t, found)
		})
	}
}

// Hiding a skill is metadata only: the row's install state must survive it,
// because the files are still in the image.
func TestSetSkillEnabledPersistsWithoutTouchingInstallState(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()

	updated, err := fx.svc.SetSkillEnabled(ctx, 7, "cfg-1", "sk-1", false)
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.False(t, updated.Enabled)

	stored, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.False(t, stored.Enabled)
	require.Equal(t, types.SkillStatusInstalling, stored.Status)

	back, err := fx.svc.SetSkillEnabled(ctx, 7, "cfg-1", "sk-1", true)
	require.NoError(t, err)
	require.True(t, back.Enabled)
}

func TestSetSkillEnabledReturnsNilForAnotherWorkspace(t *testing.T) {
	fx := newInstallFixture(t)

	updated, err := fx.svc.SetSkillEnabled(context.Background(), 8, "cfg-1", "sk-1", false)
	require.NoError(t, err)
	require.Nil(t, updated)

	stored, err := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.True(t, stored.Enabled, "a foreign workspace must not be able to hide this skill")
}

// seedSkillEnvDeclaration puts a declaration on the fixture's skill, as a
// finished install would have left it.
func seedSkillEnvDeclaration(t *testing.T, fx *installFixture, envs types.SkillEnvVars) {
	t.Helper()
	require.NoError(t, fx.skillRepo.UpdateSkillEnvs(context.Background(), 7, "cfg-1", "sk-1", envs))
}

// A name outside the declaration is ignored rather than rejected: a stale UI
// tab must not fail the whole save, and inventing a variable would store a
// value nothing reads.
func TestSetSkillEnvValuesWritesOnlyDeclaredNames(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	seedSkillEnvDeclaration(t, fx, types.SkillEnvVars{
		{Name: "API_TOKEN", Description: "token", Required: true},
		{Name: "REGION"},
	})

	updated, err := fx.svc.SetSkillEnvValues(ctx, 7, "cfg-1", "sk-1", map[string]string{
		"API_TOKEN":  "secret-1",
		"NOT_A_NAME": "ignored",
	})
	require.NoError(t, err)
	require.NotNil(t, updated)

	stored, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.Len(t, stored.Envs, 2)
	token, ok := stored.Envs.Get("API_TOKEN")
	require.True(t, ok)
	require.Equal(t, "secret-1", token.Value)
	require.True(t, token.Required, "writing a value must not rewrite the declaration")
	_, undeclared := stored.Envs.Get("NOT_A_NAME")
	require.False(t, undeclared, "an undeclared name must not be added to the declaration")
}

// "The admin filled nothing in" and "this variable is not needed" are different
// states, so clearing a value must keep the declaration.
func TestSetSkillEnvValuesEmptyStringClearsTheValueKeepingTheDeclaration(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	seedSkillEnvDeclaration(t, fx, types.SkillEnvVars{
		{Name: "API_TOKEN", Description: "token", Required: true, Value: "secret-1"},
	})

	_, err := fx.svc.SetSkillEnvValues(ctx, 7, "cfg-1", "sk-1", map[string]string{"API_TOKEN": ""})
	require.NoError(t, err)

	stored, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.Len(t, stored.Envs, 1)
	token, ok := stored.Envs.Get("API_TOKEN")
	require.True(t, ok)
	require.Empty(t, token.Value)
	require.Equal(t, "token", token.Description)
	require.True(t, token.Required)
}

func TestSetSkillEnvValuesRefusesAnOversizedValue(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	seedSkillEnvDeclaration(t, fx, types.SkillEnvVars{{Name: "API_TOKEN"}})

	_, err := fx.svc.SetSkillEnvValues(ctx, 7, "cfg-1", "sk-1", map[string]string{
		"API_TOKEN": strings.Repeat("x", MaxEnvValueBytes+1),
	})
	require.Error(t, err)

	stored, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	token, _ := stored.Envs.Get("API_TOKEN")
	require.Empty(t, token.Value, "a rejected value must not be partially written")
}

// nil rather than an error, so the handler renders the same 404 every other
// route of this resource renders.
func TestSetSkillEnvValuesReturnsNilForAnUnreachableSkill(t *testing.T) {
	fx := newInstallFixture(t)
	seedSkillEnvDeclaration(t, fx, types.SkillEnvVars{{Name: "API_TOKEN"}})

	updated, err := fx.svc.SetSkillEnvValues(context.Background(), 8, "cfg-1", "sk-1",
		map[string]string{"API_TOKEN": "secret-1"})
	require.NoError(t, err)
	require.Nil(t, updated)

	stored, err := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	token, _ := stored.Envs.Get("API_TOKEN")
	require.Empty(t, token.Value, "a foreign workspace must not be able to write this value")
}

// A request carrying both fields is applied as one state: the toggle and the
// values land together, so a failure cannot persist half of a credential
// rotation.
func TestUpdateSkillAdminAppliesEnabledAndValuesAsOneState(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	seedSkillEnvDeclaration(t, fx, types.SkillEnvVars{
		{Name: "API_TOKEN", Description: "token", Required: true, Value: "old-secret"},
		{Name: "REGION"},
	})

	enabled := false
	updated, err := fx.svc.UpdateSkillAdmin(ctx, 7, "cfg-1", "sk-1", SkillAdminUpdate{
		Enabled:   &enabled,
		EnvValues: map[string]string{"API_TOKEN": "new-secret"},
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.False(t, updated.Enabled)

	stored, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.False(t, stored.Enabled)
	token, ok := stored.Envs.Get("API_TOKEN")
	require.True(t, ok)
	require.Equal(t, "new-secret", token.Value)
	require.True(t, token.Required, "the declaration survives the write")
	region, ok := stored.Envs.Get("REGION")
	require.True(t, ok)
	require.Empty(t, region.Value, "a name the request did not send keeps its stored value")
}

// A rejected value fails the whole request, including the visibility change it
// was sent with: an admin told the save failed must not find the skill already
// re-enabled with the old credential behind it.
func TestUpdateSkillAdminRejectsTheWholeRequestOnAnOversizedValue(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	seedSkillEnvDeclaration(t, fx, types.SkillEnvVars{{Name: "API_TOKEN", Value: "old-secret"}})

	enabled := false
	_, err := fx.svc.UpdateSkillAdmin(ctx, 7, "cfg-1", "sk-1", SkillAdminUpdate{
		Enabled:   &enabled,
		EnvValues: map[string]string{"API_TOKEN": strings.Repeat("x", MaxEnvValueBytes+1)},
	})
	require.Error(t, err)

	stored, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.True(t, stored.Enabled, "the toggle must not persist when the values were refused")
	token, _ := stored.Envs.Get("API_TOKEN")
	require.Equal(t, "old-secret", token.Value)
}

// Without Redis nothing publishes progress, so there is no stream to hand out.
// The closer must still be safe to call: every caller defers it.
func TestSubscribeProgressWithoutRedisYieldsNoStream(t *testing.T) {
	fx := newInstallFixture(t)

	events, release, err := fx.svc.SubscribeProgress(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.Nil(t, events)
	require.NotNil(t, release)
	release()
}
