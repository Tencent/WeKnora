package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type folderServiceKBStub struct {
	interfaces.KnowledgeBaseService
	kb  *types.KnowledgeBase
	err error
}

func (s *folderServiceKBStub) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, s.err
}

type folderServiceRepoStub struct {
	interfaces.KnowledgeFolderRepository
	createCalls int
	updateCalls int
	tenantID    uint64
	parentID    string
	name        string
	err         error
}

func (s *folderServiceRepoStub) Create(_ context.Context, tenantID uint64, _ string, parentID, name string) (*types.KnowledgeFolder, error) {
	s.createCalls++
	s.tenantID, s.parentID, s.name = tenantID, parentID, name
	if s.err != nil {
		return nil, s.err
	}
	return &types.KnowledgeFolder{ID: "folder-1", TenantID: tenantID, ParentID: parentID, Name: name}, nil
}

func (s *folderServiceRepoStub) Update(_ context.Context, tenantID uint64, _ string, _ string, name, parentID *string) (*types.KnowledgeFolder, error) {
	s.updateCalls++
	s.tenantID = tenantID
	if name != nil {
		s.name = *name
	}
	if parentID != nil {
		s.parentID = *parentID
	}
	return &types.KnowledgeFolder{ID: "folder-1", TenantID: tenantID, ParentID: s.parentID, Name: s.name}, s.err
}

func TestValidateKnowledgeFolderName(t *testing.T) {
	t.Parallel()

	valid, err := validateKnowledgeFolderName("  Quarterly reports  ")
	require.NoError(t, err)
	require.Equal(t, "Quarterly reports", valid)

	invalid := []string{"", "   ", ".", "..", "a/b", `a\b`, "line\nbreak", strings.Repeat("x", 101)}
	for _, name := range invalid {
		_, err := validateKnowledgeFolderName(name)
		require.Error(t, err, "name %q should be rejected", name)
		var appErr *apperrors.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, apperrors.ErrBadRequest, appErr.Code)
	}
}

func TestKnowledgeFolderServiceCreateAndRenameNormalizeNames(t *testing.T) {
	t.Parallel()
	repo := &folderServiceRepoStub{}
	svc := NewKnowledgeFolderService(repo, &folderServiceKBStub{kb: &types.KnowledgeBase{
		ID: "kb-1", TenantID: 42, Type: types.KnowledgeBaseTypeDocument,
	}})

	folder, err := svc.Create(context.Background(), "kb-1", "parent-1", "  Reports  ")
	require.NoError(t, err)
	require.Equal(t, "Reports", folder.Name)
	require.EqualValues(t, 42, repo.tenantID)
	require.Equal(t, "parent-1", repo.parentID)

	name := "  Renamed  "
	parent := "parent-2"
	folder, err = svc.Update(context.Background(), "kb-1", "folder-1", &name, &parent)
	require.NoError(t, err)
	require.Equal(t, "Renamed", folder.Name)
	require.Equal(t, "parent-2", folder.ParentID)
	require.Equal(t, 1, repo.updateCalls)
}

func TestKnowledgeFolderServiceRejectsFAQAndMapsConflict(t *testing.T) {
	t.Parallel()
	faqRepo := &folderServiceRepoStub{}
	faqSvc := NewKnowledgeFolderService(faqRepo, &folderServiceKBStub{kb: &types.KnowledgeBase{
		ID: "faq-1", TenantID: 7, Type: types.KnowledgeBaseTypeFAQ,
	}})
	_, err := faqSvc.Create(context.Background(), "faq-1", "", "Folder")
	require.Error(t, err)
	require.Zero(t, faqRepo.createCalls)

	conflictRepo := &folderServiceRepoStub{err: repository.ErrKnowledgeFolderConflict}
	docSvc := NewKnowledgeFolderService(conflictRepo, &folderServiceKBStub{kb: &types.KnowledgeBase{
		ID: "kb-1", TenantID: 7, Type: types.KnowledgeBaseTypeDocument,
	}})
	_, err = docSvc.Create(context.Background(), "kb-1", "", "Folder")
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, apperrors.ErrConflict, appErr.Code)
}
