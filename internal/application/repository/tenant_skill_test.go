package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newSkillTestRepo(t *testing.T) TenantSkillRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.TenantSkillEntity{}, &types.TenantSkillSnapshotEntity{},
	))
	// AutoMigrate cannot express the partial unique index, so add it here to
	// match the production migration.
	require.NoError(t, db.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_skills_config_name
		 ON tenant_skills (sandbox_config_id, name) WHERE deleted_at IS NULL`).Error)
	return NewTenantSkillRepository(db)
}

func skillRow(id, configID, name string) *types.TenantSkillEntity {
	return &types.TenantSkillEntity{
		ID: id, TenantID: 7, SandboxConfigID: configID, Name: name,
		Status: types.SkillStatusInstalling, Enabled: true,
	}
}

func TestSkillRepoIsolatesConfigs(t *testing.T) {
	repo := newSkillTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-a", "cfg-1", "pdf")))
	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-b", "cfg-2", "pdf")))

	list, err := repo.ListSkillsByConfig(ctx, 7, "cfg-1")
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "sk-a", list[0].ID)

	got, err := repo.GetSkill(ctx, 7, "cfg-1", "sk-b")
	require.NoError(t, err)
	require.Nil(t, got, "a skill from another config must read as absent, not as an error")
}

func TestSkillRepoUpdatePersistsPointerAndStatus(t *testing.T) {
	repo := newSkillTestRepo(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-a", "cfg-1", "pdf")))

	row, err := repo.GetSkill(ctx, 7, "cfg-1", "sk-a")
	require.NoError(t, err)
	row.Status = types.SkillStatusReady
	row.InstalledSnapshotID = "snap-1"
	row.Enabled = false
	row.InstallSessionID = "sess-1"
	row.InstallMessageID = "msg-1"
	require.NoError(t, repo.UpdateSkill(ctx, row))

	got, err := repo.GetSkill(ctx, 7, "cfg-1", "sk-a")
	require.NoError(t, err)
	require.Equal(t, types.SkillStatusReady, got.Status)
	require.Equal(t, "snap-1", got.InstalledSnapshotID)
	require.False(t, got.Enabled, "disabling a skill must round-trip; it is the visibility switch")
	// The locators go through the same explicit column map as everything else,
	// so a field added to the entity but not to that map reads back empty.
	require.Equal(t, "sess-1", got.InstallSessionID)
	require.Equal(t, "msg-1", got.InstallMessageID)
}

func TestSnapshotLedgerRecordsChain(t *testing.T) {
	repo := newSkillTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.CreateSnapshotRow(ctx, &types.TenantSkillSnapshotEntity{
		ID: "ins-1", TenantID: 7, SandboxConfigID: "cfg-1", SkillID: "sk-a",
		ParentSnapshotID: "tpl-base", Generation: 1,
		Trigger: types.SkillSnapshotTriggerInstall,
		State:   types.SkillSnapshotStateBuilding,
	}))

	require.NoError(t, repo.MarkSnapshotState(ctx, 7, "ins-1", types.SkillSnapshotStateActive, "snap-1"))

	rows, err := repo.ListSnapshotsByConfig(ctx, 7, "cfg-1")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, types.SkillSnapshotStateActive, rows[0].State)
	require.Equal(t, "snap-1", rows[0].SnapshotID)
}

func TestListStaleInstallingFindsAbandonedRuns(t *testing.T) {
	repo := newSkillTestRepo(t)
	ctx := context.Background()
	cutoff := time.Now().Add(-time.Hour)

	oldInstalling := skillRow("sk-old-install", "cfg-1", "old-install")
	sinceOld := cutoff.Add(-time.Hour)
	oldInstalling.InstallingSince = &sinceOld
	require.NoError(t, repo.CreateSkill(ctx, oldInstalling))

	oldRemoving := skillRow("sk-old-remove", "cfg-1", "old-remove")
	oldRemoving.Status = types.SkillStatusRemoving
	sinceRemove := cutoff.Add(-30 * time.Minute)
	oldRemoving.InstallingSince = &sinceRemove
	require.NoError(t, repo.CreateSkill(ctx, oldRemoving))

	fresh := skillRow("sk-new", "cfg-1", "new")
	now := time.Now()
	fresh.InstallingSince = &now
	require.NoError(t, repo.CreateSkill(ctx, fresh))

	ready := skillRow("sk-ready", "cfg-1", "ready")
	ready.Status = types.SkillStatusReady
	readySince := cutoff.Add(-2 * time.Hour)
	ready.InstallingSince = &readySince
	require.NoError(t, repo.CreateSkill(ctx, ready))

	atCutoff := skillRow("sk-at-cutoff", "cfg-1", "at-cutoff")
	atCutoff.InstallingSince = &cutoff
	require.NoError(t, repo.CreateSkill(ctx, atCutoff))

	stale, err := repo.ListStaleInstalling(ctx, cutoff)
	require.NoError(t, err)
	require.Len(t, stale, 2)
	ids := []string{stale[0].ID, stale[1].ID}
	require.ElementsMatch(t, []string{"sk-old-install", "sk-old-remove"}, ids)
}

func TestSkillRepoSoftDeleteAllowsNameReuse(t *testing.T) {
	repo := newSkillTestRepo(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-a", "cfg-1", "pdf")))
	require.NoError(t, repo.DeleteSkill(ctx, 7, "cfg-1", "sk-a"))

	require.NoError(t, repo.CreateSkill(ctx, skillRow("sk-b", "cfg-1", "pdf")),
		"a soft-deleted name must be reusable for an in-place reinstall")
	got, err := repo.GetSkillByName(ctx, 7, "cfg-1", "pdf")
	require.NoError(t, err)
	require.Equal(t, "sk-b", got.ID)
}

func TestMarkSnapshotStateIsTenantScoped(t *testing.T) {
	repo := newSkillTestRepo(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateSnapshotRow(ctx, &types.TenantSkillSnapshotEntity{
		ID: "ins-1", TenantID: 7, SandboxConfigID: "cfg-1", SkillID: "sk-a",
		ParentSnapshotID: "tpl-base", Generation: 1,
		Trigger: types.SkillSnapshotTriggerInstall,
		State:   types.SkillSnapshotStateBuilding,
	}))

	require.NoError(t, repo.MarkSnapshotState(ctx, 8, "ins-1", types.SkillSnapshotStateActive, "snap-stolen"))

	rows, err := repo.ListSnapshotsByConfig(ctx, 7, "cfg-1")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, types.SkillSnapshotStateBuilding, rows[0].State,
		"a snapshot row must not move when the caller is a different tenant")
	require.Empty(t, rows[0].SnapshotID)
}
