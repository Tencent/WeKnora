package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeKBShareService struct {
	allowedKBs map[string]bool
}

func (f *fakeKBShareService) ShareKnowledgeBase(context.Context, string, string, string, uint64, types.OrgMemberRole) (*types.KnowledgeBaseShare, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeKBShareService) UpdateSharePermission(context.Context, string, types.OrgMemberRole, string) error {
	return errors.New("not implemented")
}
func (f *fakeKBShareService) RemoveShare(context.Context, string, string) error {
	return errors.New("not implemented")
}
func (f *fakeKBShareService) ListSharesByKnowledgeBase(context.Context, string, uint64) ([]*types.KnowledgeBaseShare, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeKBShareService) ListSharesByOrganization(context.Context, string) ([]*types.KnowledgeBaseShare, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeKBShareService) ListSharedKnowledgeBases(context.Context, string, uint64) ([]*types.SharedKnowledgeBaseInfo, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeKBShareService) ListSharedKnowledgeBasesInOrganization(context.Context, string, string, uint64) ([]*types.OrganizationSharedKnowledgeBaseItem, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeKBShareService) ListSharedKnowledgeBaseIDsByOrganizations(context.Context, []string, string) (map[string][]string, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeKBShareService) GetShare(context.Context, string) (*types.KnowledgeBaseShare, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeKBShareService) GetShareByKBAndOrg(context.Context, string, string) (*types.KnowledgeBaseShare, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeKBShareService) CheckUserKBPermission(context.Context, string, string) (types.OrgMemberRole, bool, error) {
	return "", false, errors.New("not implemented")
}
func (f *fakeKBShareService) HasKBPermission(ctx context.Context, kbID string, userID string, requiredRole types.OrgMemberRole) (bool, error) {
	return f.allowedKBs[kbID], nil
}
func (f *fakeKBShareService) GetKBSourceTenant(context.Context, string) (uint64, error) {
	return 0, errors.New("not implemented")
}
func (f *fakeKBShareService) CountSharesByKnowledgeBaseIDs(context.Context, []string) (map[string]int64, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeKBShareService) CountByOrganizations(context.Context, []string) (map[string]int64, error) {
	return nil, errors.New("not implemented")
}

func setupKnowledgeSharedAccessDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}))
	return db
}

func newKnowledgeSharedAccessService(t *testing.T, kbShare interfaces.KBShareService) (*knowledgeService, *gorm.DB) {
	t.Helper()

	db := setupKnowledgeSharedAccessDB(t)
	repo := repository.NewKnowledgeRepository(db)
	return &knowledgeService{
		repo:           repo,
		kbShareService: kbShare,
	}, db
}

func newSharedAccessContext() context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "user-1")
	return ctx
}

func seedKnowledge(t *testing.T, db *gorm.DB, knowledge *types.Knowledge) {
	t.Helper()
	require.NoError(t, db.Create(knowledge).Error)
}

func TestGetKnowledgeBatchWithSharedAccess_IncludesSharedKnowledgeWithPermission(t *testing.T) {
	service, db := newKnowledgeSharedAccessService(t, &fakeKBShareService{
		allowedKBs: map[string]bool{"kb-shared": true},
	})

	now := time.Now()
	sharedKnowledge := &types.Knowledge{
		ID:              "k-shared",
		TenantID:        2,
		KnowledgeBaseID: "kb-shared",
		Type:            "file",
		Title:           "shared doc",
		FileName:        "shared.txt",
		FileType:        "txt",
		ParseStatus:     types.ParseStatusCompleted,
		EnableStatus:    "enabled",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	seedKnowledge(t, db, sharedKnowledge)

	got, err := service.GetKnowledgeBatchWithSharedAccess(newSharedAccessContext(), 1, []string{"k-shared"})

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "k-shared", got[0].ID)
	require.Equal(t, uint64(2), got[0].TenantID)
	require.Equal(t, "kb-shared", got[0].KnowledgeBaseID)
}

func TestGetKnowledgeBatchWithSharedAccess_ExcludesSharedKnowledgeWithoutPermission(t *testing.T) {
	service, db := newKnowledgeSharedAccessService(t, &fakeKBShareService{
		allowedKBs: map[string]bool{},
	})

	now := time.Now()
	sharedKnowledge := &types.Knowledge{
		ID:              "k-shared",
		TenantID:        2,
		KnowledgeBaseID: "kb-shared",
		Type:            "file",
		Title:           "shared doc",
		FileName:        "shared.txt",
		FileType:        "txt",
		ParseStatus:     types.ParseStatusCompleted,
		EnableStatus:    "enabled",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	seedKnowledge(t, db, sharedKnowledge)

	got, err := service.GetKnowledgeBatchWithSharedAccess(newSharedAccessContext(), 1, []string{"k-shared"})

	require.NoError(t, err)
	require.Empty(t, got)
}

var _ interfaces.KBShareService = (*fakeKBShareService)(nil)
