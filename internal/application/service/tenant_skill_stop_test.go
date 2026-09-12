package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestStopSkillFailsAnAbandonedInstall(t *testing.T) {
	fx := newInstallFixture(t)

	got, err := fx.svc.StopSkill(context.Background(), 7, "cfg-1", "sk-1")

	require.NoError(t, err)
	require.Equal(t, types.SkillStatusFailed, got.Status)
	require.Equal(t, skillInstallStoppedMessage, got.Error)
	require.Nil(t, got.InstallingSince)
}

func TestStopSkillIsIdempotentWhenAlreadyFailed(t *testing.T) {
	fx := newInstallFixture(t)
	require.NoError(t, fx.svc.updateSkillFields(context.Background(), 7, "cfg-1", "sk-1",
		func(e *types.TenantSkillEntity) {
			e.Status = types.SkillStatusFailed
			e.Error = "python verification failed"
			e.InstallingSince = nil
		}))

	got, err := fx.svc.StopSkill(context.Background(), 7, "cfg-1", "sk-1")

	require.NoError(t, err)
	require.Equal(t, types.SkillStatusFailed, got.Status)
	require.Equal(t, "python verification failed", got.Error,
		"a second stop must not overwrite the failure that already unblocked the UI")
}

func TestStopSkillRejectsAReadySkill(t *testing.T) {
	fx := newInstallFixture(t)
	require.NoError(t, fx.svc.updateSkillFields(context.Background(), 7, "cfg-1", "sk-1",
		func(e *types.TenantSkillEntity) {
			e.Status = types.SkillStatusReady
			e.InstallingSince = nil
		}))

	_, err := fx.svc.StopSkill(context.Background(), 7, "cfg-1", "sk-1")

	require.ErrorContains(t, err, "not installing")
}

func TestStopSkillRejectsAnUnknownSkill(t *testing.T) {
	fx := newInstallFixture(t)

	_, err := fx.svc.StopSkill(context.Background(), 7, "cfg-1", "nope")

	require.ErrorContains(t, err, "not found")
}

func TestStopSkillCancelsALiveInstall(t *testing.T) {
	fx := newInstallFixture(t)
	started := make(chan struct{})
	released := make(chan struct{})
	go func() {
		_ = fx.svc.withSkillRunLock(context.Background(), 7, "cfg-1", "sk-1",
			func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				close(released)
				return ctx.Err()
			})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("live install never entered the lock")
	}

	got, err := fx.svc.StopSkill(context.Background(), 7, "cfg-1", "sk-1")

	require.NoError(t, err)
	require.Equal(t, types.SkillStatusFailed, got.Status)
	require.Equal(t, skillInstallStoppedMessage, got.Error)
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("StopSkill did not cancel the live install")
	}
}

func TestStopSkillLeavesARemovingSkillAlone(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedInstalledSkill("sk-1", "snap-old", 3)

	_, err := fx.svc.StopSkill(context.Background(), 7, "cfg-1", "sk-1")

	require.ErrorContains(t, err, "not installing")
	skill, getErr := fx.skillRepo.GetSkill(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, getErr)
	require.Equal(t, types.SkillStatusRemoving, skill.Status,
		"stop is install-only; a removal in flight must keep running")
}
