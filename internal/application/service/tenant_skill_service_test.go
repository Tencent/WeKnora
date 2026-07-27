package service

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/skillpkg"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func buildServiceSkillZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}

func newTenantSkillServiceForTest(t *testing.T) *skillService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.TenantSkill{}, &types.TenantSkillVersion{}, &types.SkillExecutionAudit{},
	))
	repo := repository.NewTenantSkillRepository(db)
	storage := skillpkg.NewFileStorage(t.TempDir(), skillpkg.NewValidator(skillpkg.DefaultLimits()))
	return NewSkillService(
		WithTenantSkillRepository(repo),
		WithTenantSkillStorage(storage),
		WithTenantUploadEnabled(true),
	).(*skillService)
}

func tenantSkillArchive(t *testing.T, name string) []byte {
	t.Helper()
	return buildServiceSkillZip(t, map[string]string{
		"SKILL.md":       "---\nname: " + name + "\ndescription: Tenant skill\ncategory: workflow\nscripts:\n  - scripts/run.py\n---\n# Skill\n",
		"scripts/run.py": "print('ok')\n",
	})
}

func TestTenantSkillServiceUploadCreatesCurrentVersion(t *testing.T) {
	service := newTenantSkillServiceForTest(t)
	archive := tenantSkillArchive(t, "workflow-helper")

	skill, err := service.Upload(
		context.Background(), 10000, "user-1", bytes.NewReader(archive), int64(len(archive)),
	)
	require.NoError(t, err)
	require.Equal(t, types.TenantSkillEnabled, skill.Status)
	require.NotNil(t, skill.CurrentVersionID)

	detail, err := service.GetVisible(
		context.Background(), 10000,
		types.SkillReference{Source: types.SkillSourceTenant, SkillID: skill.ID}, true,
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), detail.Version)
	require.True(t, detail.HasScripts)
}

func TestTenantSkillServiceListHidesDisabledFromMembers(t *testing.T) {
	service := newTenantSkillServiceForTest(t)
	archive := tenantSkillArchive(t, "hidden-helper")
	skill, err := service.Upload(context.Background(), 10000, "user-1", bytes.NewReader(archive), int64(len(archive)))
	require.NoError(t, err)

	results := service.SetStatuses(context.Background(), 10000, []types.SkillStatusUpdate{
		{SkillID: skill.ID, Status: types.TenantSkillDisabled},
	})
	require.Len(t, results, 1)
	require.True(t, results[0].Success)

	memberList, err := service.ListVisible(context.Background(), 10000, false)
	require.NoError(t, err)
	require.Empty(t, tenantOnlySummaries(memberList))

	managerList, err := service.ListVisible(context.Background(), 10000, true)
	require.NoError(t, err)
	require.Len(t, tenantOnlySummaries(managerList), 1)
}

func TestTenantSkillServiceUpdateAndDelete(t *testing.T) {
	service := newTenantSkillServiceForTest(t)
	firstArchive := tenantSkillArchive(t, "versioned-helper")
	skill, err := service.Upload(context.Background(), 10000, "user-1", bytes.NewReader(firstArchive), int64(len(firstArchive)))
	require.NoError(t, err)

	secondArchive := tenantSkillArchive(t, "versioned-helper")
	updated, err := service.UpdatePackage(
		context.Background(), 10000, "user-1", skill.ID,
		bytes.NewReader(secondArchive), int64(len(secondArchive)), 1,
	)
	require.NoError(t, err)
	require.Equal(t, int64(2), updated.Version)

	require.NoError(t, service.Delete(context.Background(), 10000, "user-1", skill.ID))
	_, err = service.GetVisible(
		context.Background(), 10000,
		types.SkillReference{Source: types.SkillSourceTenant, SkillID: skill.ID}, true,
	)
	require.Error(t, err)
}

func tenantOnlySummaries(values []*types.SkillSummary) []*types.SkillSummary {
	result := make([]*types.SkillSummary, 0)
	for _, value := range values {
		if value.Source == types.SkillSourceTenant {
			result = append(result, value)
		}
	}
	return result
}
