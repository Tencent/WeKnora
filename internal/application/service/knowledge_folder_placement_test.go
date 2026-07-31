package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	knowledgeFolderPlacementTestTenantID = uint64(7)
	knowledgeFolderPlacementTestKBID     = "kb-placement"
	knowledgeFolderPlacementTestRootID   = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	knowledgeFolderPlacementTestChildID  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	knowledgeFolderPlacementTestOtherID  = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
)

type knowledgeFolderPlacementReaderStub struct {
	interfaces.KnowledgeFolderReader

	getByIDCalls  int
	listByIDCalls int

	getTenantID uint64
	getKBID     string
	getFolderID string

	getFolder   *types.KnowledgeFolder
	getErr      error
	listFolders []*types.KnowledgeFolder
	listErr     error
}

func (r *knowledgeFolderPlacementReaderStub) GetByID(
	_ context.Context,
	tenantID uint64,
	kbID string,
	folderID string,
) (*types.KnowledgeFolder, error) {
	r.getByIDCalls++
	r.getTenantID = tenantID
	r.getKBID = kbID
	r.getFolderID = folderID
	return r.getFolder, r.getErr
}

func (r *knowledgeFolderPlacementReaderStub) ListByIDs(
	_ context.Context,
	_ uint64,
	_ string,
	_ []string,
) ([]*types.KnowledgeFolder, error) {
	r.listByIDCalls++
	return r.listFolders, r.listErr
}

func knowledgeFolderPlacementTestContext() context.Context {
	return context.WithValue(
		context.Background(),
		types.TenantIDContextKey,
		knowledgeFolderPlacementTestTenantID,
	)
}

func knowledgeFolderPlacementTestRoot() *types.KnowledgeFolder {
	return &types.KnowledgeFolder{
		ID:              knowledgeFolderPlacementTestRootID,
		TenantID:        knowledgeFolderPlacementTestTenantID,
		KnowledgeBaseID: knowledgeFolderPlacementTestKBID,
		ParentID:        types.KnowledgeFolderRootID,
		Name:            "root",
		Path:            "/" + knowledgeFolderPlacementTestRootID + "/",
		Depth:           1,
	}
}

func knowledgeFolderPlacementTestChild() *types.KnowledgeFolder {
	return &types.KnowledgeFolder{
		ID:              knowledgeFolderPlacementTestChildID,
		TenantID:        knowledgeFolderPlacementTestTenantID,
		KnowledgeBaseID: knowledgeFolderPlacementTestKBID,
		ParentID:        knowledgeFolderPlacementTestRootID,
		Name:            "child",
		Path: "/" + knowledgeFolderPlacementTestRootID +
			"/" + knowledgeFolderPlacementTestChildID + "/",
		Depth: 2,
	}
}

func TestKnowledgeFolderPlacementResolverRootDoesNotRead(t *testing.T) {
	reader := &knowledgeFolderPlacementReaderStub{}
	resolver := NewKnowledgeFolderPlacementResolver(reader)

	got, err := resolver.ResolveForCreate(
		knowledgeFolderPlacementTestContext(),
		knowledgeFolderPlacementTestKBID,
		types.KnowledgeFolderRootID,
	)

	require.NoError(t, err)
	require.Equal(t, types.KnowledgeFolderRootID, got)
	require.Zero(t, reader.getByIDCalls)
	require.Zero(t, reader.listByIDCalls)
}

func TestKnowledgeFolderPlacementResolverRejectsNilContext(t *testing.T) {
	reader := &knowledgeFolderPlacementReaderStub{}
	resolver := NewKnowledgeFolderPlacementResolver(reader)

	got, err := resolver.ResolveForCreate(
		nil,
		knowledgeFolderPlacementTestKBID,
		knowledgeFolderPlacementTestRootID,
	)

	require.Empty(t, got)
	require.ErrorIs(t, err, ErrKnowledgeFolderInvalidArgument)
	require.Zero(t, reader.getByIDCalls)
	require.Zero(t, reader.listByIDCalls)
}

func TestKnowledgeFolderPlacementResolverReturnsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(knowledgeFolderPlacementTestContext())
	cancel()
	reader := &knowledgeFolderPlacementReaderStub{}
	resolver := NewKnowledgeFolderPlacementResolver(reader)

	got, err := resolver.ResolveForCreate(
		ctx,
		knowledgeFolderPlacementTestKBID,
		knowledgeFolderPlacementTestRootID,
	)

	require.Empty(t, got)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, reader.getByIDCalls)
	require.Zero(t, reader.listByIDCalls)
}

func TestKnowledgeFolderPlacementResolverRejectsEmptyKnowledgeBaseID(t *testing.T) {
	for _, kbID := range []string{"", " \t "} {
		t.Run(kbID, func(t *testing.T) {
			reader := &knowledgeFolderPlacementReaderStub{}
			resolver := NewKnowledgeFolderPlacementResolver(reader)

			got, err := resolver.ResolveForCreate(
				knowledgeFolderPlacementTestContext(),
				kbID,
				knowledgeFolderPlacementTestRootID,
			)

			require.Empty(t, got)
			require.ErrorIs(t, err, ErrKnowledgeFolderInvalidArgument)
			require.Zero(t, reader.getByIDCalls)
			require.Zero(t, reader.listByIDCalls)
		})
	}
}

func TestKnowledgeFolderPlacementResolverRejectsNonCanonicalFolderIDsBeforeRead(t *testing.T) {
	tests := []struct {
		name     string
		folderID string
	}{
		{name: "malformed", folderID: "not-a-uuid"},
		{name: "uppercase", folderID: strings.ToUpper(knowledgeFolderPlacementTestRootID)},
		{
			name:     "non-hyphenated",
			folderID: strings.ReplaceAll(knowledgeFolderPlacementTestRootID, "-", ""),
		},
		{name: "leading whitespace", folderID: " " + knowledgeFolderPlacementTestRootID},
		{name: "trailing whitespace", folderID: knowledgeFolderPlacementTestRootID + " "},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := &knowledgeFolderPlacementReaderStub{}
			resolver := NewKnowledgeFolderPlacementResolver(reader)

			got, err := resolver.ResolveForCreate(
				knowledgeFolderPlacementTestContext(),
				knowledgeFolderPlacementTestKBID,
				test.folderID,
			)

			require.Empty(t, got)
			require.ErrorIs(t, err, ErrKnowledgeFolderInvalidArgument)
			require.Zero(t, reader.getByIDCalls)
			require.Zero(t, reader.listByIDCalls)
		})
	}
}

func TestKnowledgeFolderPlacementResolverResolvesValidCanonicalFolder(t *testing.T) {
	root := knowledgeFolderPlacementTestRoot()
	child := knowledgeFolderPlacementTestChild()
	reader := &knowledgeFolderPlacementReaderStub{
		getFolder:   child,
		listFolders: []*types.KnowledgeFolder{child, root},
	}
	resolver := NewKnowledgeFolderPlacementResolver(reader)

	got, err := resolver.ResolveForCreate(
		knowledgeFolderPlacementTestContext(),
		knowledgeFolderPlacementTestKBID,
		knowledgeFolderPlacementTestChildID,
	)

	require.NoError(t, err)
	require.Equal(t, knowledgeFolderPlacementTestChildID, got)
	require.Equal(t, 1, reader.getByIDCalls)
	require.Equal(t, 1, reader.listByIDCalls)
	require.Equal(t, knowledgeFolderPlacementTestTenantID, reader.getTenantID)
	require.Equal(t, knowledgeFolderPlacementTestKBID, reader.getKBID)
	require.Equal(t, knowledgeFolderPlacementTestChildID, reader.getFolderID)
}

func TestKnowledgeFolderPlacementResolverMapsScopedNotFound(t *testing.T) {
	reader := &knowledgeFolderPlacementReaderStub{
		getErr: repository.ErrKnowledgeFolderNotFound,
	}
	resolver := NewKnowledgeFolderPlacementResolver(reader)

	got, err := resolver.ResolveForCreate(
		knowledgeFolderPlacementTestContext(),
		knowledgeFolderPlacementTestKBID,
		knowledgeFolderPlacementTestRootID,
	)

	require.Empty(t, got)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
	require.Equal(t, 1, reader.getByIDCalls)
	require.Zero(t, reader.listByIDCalls)
}

func TestKnowledgeFolderPlacementResolverRejectsWrongScopedOrWrongIDRow(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*types.KnowledgeFolder)
	}{
		{
			name: "wrong tenant",
			mutate: func(folder *types.KnowledgeFolder) {
				folder.TenantID++
			},
		},
		{
			name: "wrong knowledge base",
			mutate: func(folder *types.KnowledgeFolder) {
				folder.KnowledgeBaseID = "kb-other"
			},
		},
		{
			name: "wrong id",
			mutate: func(folder *types.KnowledgeFolder) {
				folder.ID = knowledgeFolderPlacementTestOtherID
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			folder := knowledgeFolderPlacementTestRoot()
			test.mutate(folder)
			reader := &knowledgeFolderPlacementReaderStub{getFolder: folder}
			resolver := NewKnowledgeFolderPlacementResolver(reader)

			got, err := resolver.ResolveForCreate(
				knowledgeFolderPlacementTestContext(),
				knowledgeFolderPlacementTestKBID,
				knowledgeFolderPlacementTestRootID,
			)

			require.Empty(t, got)
			require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
			require.Equal(t, 1, reader.getByIDCalls)
			require.Zero(t, reader.listByIDCalls)
		})
	}
}

func TestKnowledgeFolderPlacementResolverRejectsNilRow(t *testing.T) {
	reader := &knowledgeFolderPlacementReaderStub{}
	resolver := NewKnowledgeFolderPlacementResolver(reader)

	got, err := resolver.ResolveForCreate(
		knowledgeFolderPlacementTestContext(),
		knowledgeFolderPlacementTestKBID,
		knowledgeFolderPlacementTestRootID,
	)

	require.Empty(t, got)
	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
	require.Equal(t, 1, reader.getByIDCalls)
	require.Zero(t, reader.listByIDCalls)
}

func TestKnowledgeFolderPlacementResolverFailsClosedWithoutReader(t *testing.T) {
	resolver := NewKnowledgeFolderPlacementResolver(nil)

	got, err := resolver.ResolveForCreate(
		knowledgeFolderPlacementTestContext(),
		knowledgeFolderPlacementTestKBID,
		knowledgeFolderPlacementTestRootID,
	)

	require.Empty(t, got)
	require.ErrorIs(t, err, ErrKnowledgeFolderInternal)
}

func TestKnowledgeFolderPlacementResolverRejectsSoftDeletedTarget(t *testing.T) {
	folder := knowledgeFolderPlacementTestRoot()
	folder.DeletedAt = gorm.DeletedAt{Valid: true}
	reader := &knowledgeFolderPlacementReaderStub{getFolder: folder}
	resolver := NewKnowledgeFolderPlacementResolver(reader)

	got, err := resolver.ResolveForCreate(
		knowledgeFolderPlacementTestContext(),
		knowledgeFolderPlacementTestKBID,
		knowledgeFolderPlacementTestRootID,
	)

	require.Empty(t, got)
	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
	require.Equal(t, 1, reader.getByIDCalls)
	require.Zero(t, reader.listByIDCalls)
}

func TestKnowledgeFolderPlacementResolverRejectsMissingAncestor(t *testing.T) {
	child := knowledgeFolderPlacementTestChild()
	reader := &knowledgeFolderPlacementReaderStub{
		getFolder:   child,
		listFolders: []*types.KnowledgeFolder{child},
	}
	resolver := NewKnowledgeFolderPlacementResolver(reader)

	got, err := resolver.ResolveForCreate(
		knowledgeFolderPlacementTestContext(),
		knowledgeFolderPlacementTestKBID,
		knowledgeFolderPlacementTestChildID,
	)

	require.Empty(t, got)
	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
	require.Equal(t, 1, reader.listByIDCalls)
}

func TestKnowledgeFolderPlacementResolverRejectsSoftDeletedAncestor(t *testing.T) {
	root := knowledgeFolderPlacementTestRoot()
	root.DeletedAt = gorm.DeletedAt{Valid: true}
	child := knowledgeFolderPlacementTestChild()
	reader := &knowledgeFolderPlacementReaderStub{
		getFolder:   child,
		listFolders: []*types.KnowledgeFolder{root, child},
	}
	resolver := NewKnowledgeFolderPlacementResolver(reader)

	got, err := resolver.ResolveForCreate(
		knowledgeFolderPlacementTestContext(),
		knowledgeFolderPlacementTestKBID,
		knowledgeFolderPlacementTestChildID,
	)

	require.Empty(t, got)
	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
	require.Equal(t, 1, reader.listByIDCalls)
}

func TestKnowledgeFolderPlacementResolverRejectsCorruptFolderStructure(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*types.KnowledgeFolder)
	}{
		{
			name: "parent",
			mutate: func(folder *types.KnowledgeFolder) {
				folder.ParentID = knowledgeFolderPlacementTestOtherID
			},
		},
		{
			name: "path",
			mutate: func(folder *types.KnowledgeFolder) {
				folder.Path = "/" + knowledgeFolderPlacementTestChildID + "/"
			},
		},
		{
			name: "depth",
			mutate: func(folder *types.KnowledgeFolder) {
				folder.Depth++
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := knowledgeFolderPlacementTestRoot()
			child := knowledgeFolderPlacementTestChild()
			test.mutate(child)
			reader := &knowledgeFolderPlacementReaderStub{
				getFolder:   child,
				listFolders: []*types.KnowledgeFolder{root, child},
			}
			resolver := NewKnowledgeFolderPlacementResolver(reader)

			got, err := resolver.ResolveForCreate(
				knowledgeFolderPlacementTestContext(),
				knowledgeFolderPlacementTestKBID,
				knowledgeFolderPlacementTestChildID,
			)

			require.Empty(t, got)
			require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
		})
	}
}

func TestKnowledgeFolderPlacementResolverRejectsNilAncestorElement(t *testing.T) {
	child := knowledgeFolderPlacementTestChild()
	reader := &knowledgeFolderPlacementReaderStub{
		getFolder:   child,
		listFolders: []*types.KnowledgeFolder{nil, child},
	}
	resolver := NewKnowledgeFolderPlacementResolver(reader)

	got, err := resolver.ResolveForCreate(
		knowledgeFolderPlacementTestContext(),
		knowledgeFolderPlacementTestKBID,
		knowledgeFolderPlacementTestChildID,
	)

	require.Empty(t, got)
	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
}

func TestKnowledgeFolderPlacementResolverRejectsTargetPathTailMismatch(t *testing.T) {
	child := knowledgeFolderPlacementTestChild()
	child.Path = "/" + knowledgeFolderPlacementTestRootID +
		"/" + knowledgeFolderPlacementTestOtherID + "/"
	reader := &knowledgeFolderPlacementReaderStub{getFolder: child}
	resolver := NewKnowledgeFolderPlacementResolver(reader)

	got, err := resolver.ResolveForCreate(
		knowledgeFolderPlacementTestContext(),
		knowledgeFolderPlacementTestKBID,
		knowledgeFolderPlacementTestChildID,
	)

	require.Empty(t, got)
	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
}

func TestKnowledgeFolderPlacementResolverMapsRepositoryErrors(t *testing.T) {
	t.Run("invalid repository input is internal", func(t *testing.T) {
		reader := &knowledgeFolderPlacementReaderStub{
			getErr: repository.ErrKnowledgeFolderInvalid,
		}
		resolver := NewKnowledgeFolderPlacementResolver(reader)

		got, err := resolver.ResolveForCreate(
			knowledgeFolderPlacementTestContext(),
			knowledgeFolderPlacementTestKBID,
			knowledgeFolderPlacementTestRootID,
		)

		require.Empty(t, got)
		require.ErrorIs(t, err, ErrKnowledgeFolderInternal)
		require.ErrorIs(t, err, repository.ErrKnowledgeFolderInvalid)
	})

	t.Run("repository integrity error is preserved", func(t *testing.T) {
		reader := &knowledgeFolderPlacementReaderStub{
			getErr: repository.ErrKnowledgeFolderDataIntegrity,
		}
		resolver := NewKnowledgeFolderPlacementResolver(reader)

		got, err := resolver.ResolveForCreate(
			knowledgeFolderPlacementTestContext(),
			knowledgeFolderPlacementTestKBID,
			knowledgeFolderPlacementTestRootID,
		)

		require.Empty(t, got)
		require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
		require.ErrorIs(t, err, repository.ErrKnowledgeFolderDataIntegrity)
	})

	t.Run("unknown get error is internal", func(t *testing.T) {
		repositoryErr := errors.New("database unavailable")
		reader := &knowledgeFolderPlacementReaderStub{getErr: repositoryErr}
		resolver := NewKnowledgeFolderPlacementResolver(reader)

		got, err := resolver.ResolveForCreate(
			knowledgeFolderPlacementTestContext(),
			knowledgeFolderPlacementTestKBID,
			knowledgeFolderPlacementTestRootID,
		)

		require.Empty(t, got)
		require.ErrorIs(t, err, ErrKnowledgeFolderInternal)
		require.ErrorIs(t, err, repositoryErr)
	})

	t.Run("unknown list error is internal", func(t *testing.T) {
		repositoryErr := errors.New("database unavailable")
		folder := knowledgeFolderPlacementTestRoot()
		reader := &knowledgeFolderPlacementReaderStub{
			getFolder: folder,
			listErr:   repositoryErr,
		}
		resolver := NewKnowledgeFolderPlacementResolver(reader)

		got, err := resolver.ResolveForCreate(
			knowledgeFolderPlacementTestContext(),
			knowledgeFolderPlacementTestKBID,
			knowledgeFolderPlacementTestRootID,
		)

		require.Empty(t, got)
		require.ErrorIs(t, err, ErrKnowledgeFolderInternal)
		require.ErrorIs(t, err, repositoryErr)
	})
}

func TestKnowledgeFolderPlacementResolverDoesNotModifyInput(t *testing.T) {
	rawFolderID := knowledgeFolderPlacementTestRootID
	original := rawFolderID
	folder := knowledgeFolderPlacementTestRoot()
	reader := &knowledgeFolderPlacementReaderStub{
		getFolder:   folder,
		listFolders: []*types.KnowledgeFolder{folder},
	}
	resolver := NewKnowledgeFolderPlacementResolver(reader)

	got, err := resolver.ResolveForCreate(
		knowledgeFolderPlacementTestContext(),
		knowledgeFolderPlacementTestKBID,
		rawFolderID,
	)

	require.NoError(t, err)
	require.Equal(t, original, rawFolderID)
	require.Equal(t, original, got)
}
