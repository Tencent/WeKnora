package service

import (
	"context"
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
