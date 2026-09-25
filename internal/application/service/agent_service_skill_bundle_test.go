package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type skillBundleFileMap struct {
	files map[string][]byte
}

func (s skillBundleFileMap) CheckConnectivity(context.Context) error { return nil }
func (s skillBundleFileMap) SaveFile(context.Context, *multipart.FileHeader, uint64, string) (string, error) {
	return "", errors.New("not implemented")
}
func (s skillBundleFileMap) SaveBytes(context.Context, []byte, uint64, string, bool) (string, error) {
	return "", errors.New("not implemented")
}
func (s skillBundleFileMap) GetFile(_ context.Context, ref string) (io.ReadCloser, error) {
	data, ok := s.files[ref]
	if !ok {
		return nil, errors.New("bundle not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}
func (s skillBundleFileMap) GetFileURL(context.Context, string) (string, error) { return "", nil }
func (s skillBundleFileMap) DeleteFile(context.Context, string) error           { return nil }
func (s skillBundleFileMap) CopyFile(context.Context, string, uint64, string) (string, error) {
	return "", errors.New("not implemented")
}

type skillBundleResolver struct{ fs interfaces.FileService }

func (r skillBundleResolver) ResolveFileService(
	context.Context, *types.Tenant, string, string, string,
) (interfaces.FileService, string, error) {
	return r.fs, "", nil
}
func (skillBundleResolver) ResolveBackend(
	context.Context, *types.Tenant, string, string,
) (*types.StorageBackend, error) {
	return nil, nil
}

func newAgentSkillBundleService(t *testing.T, files map[string][]byte) (*agentService, *gorm.DB) {
	t.Helper()
	dsn := "file:agent-skill-" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.TenantSkillEntity{}, &types.TenantSkillCatalogEntity{},
		&types.TenantSkillSnapshotEntity{}, &types.TenantUserEnvVar{},
	))
	return &agentService{
		db:              db,
		storageResolver: skillBundleResolver{fs: skillBundleFileMap{files: files}},
	}, db
}

func zipSkillArchive(t *testing.T, extract string) []byte {
	t.Helper()
	archive, err := zipSkillFiles(map[string][]byte{
		"SKILL.md":           []byte(validSkillMD),
		"scripts/extract.py": []byte(extract),
	})
	require.NoError(t, err)
	return archive
}

func TestLoadInstalledSkillBundleReadsAMatchingCatalogArchive(t *testing.T) {
	v1 := zipSkillArchive(t, "print('v1')\n")
	svc, db := newAgentSkillBundleService(t, map[string][]byte{"file://catalog.zip": v1})
	ctx := context.Background()
	require.NoError(t, repository.NewTenantSkillRepository(db).CreateCatalog(ctx, &types.TenantSkillCatalogEntity{
		ID: "cat-1", TenantID: 7, Name: "pdf-tools",
		BundleRef: "file://catalog.zip", BundleSHA256: skillArchiveSHA256(v1),
	}))

	got, err := svc.loadInstalledSkillBundle(ctx, 7, &types.TenantSkillEntity{
		Name: "pdf-tools", CatalogID: "cat-1", BundleSHA256: skillArchiveSHA256(v1),
	})
	require.NoError(t, err)
	require.Equal(t, v1, got)
}

func TestLoadInstalledSkillBundleRejectsAReplacedCatalogArchive(t *testing.T) {
	v2 := zipSkillArchive(t, "print('v2')\n")
	svc, db := newAgentSkillBundleService(t, map[string][]byte{"file://catalog.zip": v2})
	ctx := context.Background()
	require.NoError(t, repository.NewTenantSkillRepository(db).CreateCatalog(ctx, &types.TenantSkillCatalogEntity{
		ID: "cat-1", TenantID: 7, Name: "pdf-tools",
		BundleRef: "file://catalog.zip", BundleSHA256: skillArchiveSHA256(v2),
	}))

	_, err := svc.loadInstalledSkillBundle(ctx, 7, &types.TenantSkillEntity{
		Name: "pdf-tools", CatalogID: "cat-1",
		BundleSHA256: strings.Repeat("d", 64),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "registered again from a different archive")
}

func TestLoadInstalledSkillBundlePrefersAPinnedInstallRef(t *testing.T) {
	v1 := zipSkillArchive(t, "print('v1')\n")
	v2 := zipSkillArchive(t, "print('v2')\n")
	svc, db := newAgentSkillBundleService(t, map[string][]byte{
		"file://old.zip":     v1,
		"file://catalog.zip": v2,
	})
	ctx := context.Background()
	require.NoError(t, repository.NewTenantSkillRepository(db).CreateCatalog(ctx, &types.TenantSkillCatalogEntity{
		ID: "cat-1", TenantID: 7, Name: "pdf-tools",
		BundleRef: "file://catalog.zip", BundleSHA256: skillArchiveSHA256(v2),
	}))

	got, err := svc.loadInstalledSkillBundle(ctx, 7, &types.TenantSkillEntity{
		Name: "pdf-tools", CatalogID: "cat-1",
		BundleRef: "file://old.zip", BundleSHA256: skillArchiveSHA256(v1),
	})
	require.NoError(t, err)
	require.Equal(t, v1, got)
}
