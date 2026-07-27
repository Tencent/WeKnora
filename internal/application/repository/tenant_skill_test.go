package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTenantSkillRepositoryForTest(t *testing.T) (*tenantSkillRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.TenantSkill{},
		&types.TenantSkillVersion{},
		&types.SkillExecutionAudit{},
	))
	return &tenantSkillRepository{db: db}, db
}

func seedTenantSkillForTest(t *testing.T, db *gorm.DB, tenantID uint64, id, name string) *types.TenantSkill {
	t.Helper()
	skill := &types.TenantSkill{
		ID: id, TenantID: tenantID, Name: name, Description: name,
		Category: types.SkillCategoryOther, Status: types.TenantSkillEnabled,
		UploadedBy: "user-1",
	}
	require.NoError(t, db.Create(skill).Error)
	return skill
}

func seedTenantSkillVersionForTest(
	t *testing.T,
	db *gorm.DB,
	skill *types.TenantSkill,
	id string,
	version int64,
	state types.TenantSkillVersionState,
) *types.TenantSkillVersion {
	t.Helper()
	record := &types.TenantSkillVersion{
		ID: id, TenantID: skill.TenantID, SkillID: skill.ID, Version: version,
		State: state, StoragePath: "10000/" + skill.ID + "/" + id,
		ContentHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestJSON: []byte(`{"name":"` + skill.Name + `"}`), CreatedBy: "user-1",
	}
	require.NoError(t, db.Create(record).Error)
	return record
}

func TestTenantSkillRepositoryGetRequiresTenant(t *testing.T) {
	repo, db := newTenantSkillRepositoryForTest(t)
	skill := seedTenantSkillForTest(t, db, 10000, "skill-1", "invoice-reader")

	_, err := repo.GetByID(context.Background(), 20000, skill.ID)
	require.ErrorIs(t, err, ErrTenantSkillNotFound)

	got, err := repo.GetByID(context.Background(), 10000, skill.ID)
	require.NoError(t, err)
	require.Equal(t, uint64(10000), got.TenantID)
}

func TestTenantSkillRepositorySwitchCurrentVersion(t *testing.T) {
	repo, db := newTenantSkillRepositoryForTest(t)
	skill := seedTenantSkillForTest(t, db, 10000, "skill-2", "data-helper")
	v1 := seedTenantSkillVersionForTest(t, db, skill, "version-1", 1, types.SkillVersionCurrent)
	v2 := seedTenantSkillVersionForTest(t, db, skill, "version-2", 2, types.SkillVersionReady)
	skill.CurrentVersionID = &v1.ID
	require.NoError(t, db.Save(skill).Error)

	require.NoError(t, repo.SwitchCurrentVersion(
		context.Background(), 10000, skill.ID, v1.ID, v2.ID,
	))

	var oldVersion, newVersion types.TenantSkillVersion
	require.NoError(t, db.First(&oldVersion, "id = ?", v1.ID).Error)
	require.NoError(t, db.First(&newVersion, "id = ?", v2.ID).Error)
	require.Equal(t, types.SkillVersionGarbage, oldVersion.State)
	require.Equal(t, types.SkillVersionCurrent, newVersion.State)

	updated, err := repo.GetByID(context.Background(), 10000, skill.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.CurrentVersionID)
	require.Equal(t, v2.ID, *updated.CurrentVersionID)
}
