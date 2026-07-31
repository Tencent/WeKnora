package service

import (
	"context"
	"errors"
	"math"
	"sort"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	knowledgeFolderMoveTestKnowledgeA = "11111111-1111-4111-8111-111111111111"
	knowledgeFolderMoveTestKnowledgeB = "22222222-2222-4222-8222-222222222222"
	knowledgeFolderMoveTestKnowledgeC = "33333333-3333-4333-8333-333333333333"
	knowledgeFolderMoveTestFolderA    = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	knowledgeFolderMoveTestFolderB    = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

type knowledgeFolderMoveServiceRepository struct {
	tx       *knowledgeFolderMoveServiceTransaction
	runErr   error
	runCalls int
}

func (r *knowledgeFolderMoveServiceRepository) RunKnowledgeFolderMoveTransaction(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	fn interfaces.KnowledgeFolderMoveWriteFunc,
) error {
	r.runCalls++
	if r.runErr != nil {
		return r.runErr
	}
	return fn(r.tx)
}

type knowledgeFolderMoveServiceTransaction struct {
	interfaces.KnowledgeFolderReader
	knowledges      []*types.Knowledge
	lockErr         error
	updateErr       error
	pendingErr      error
	folder          *types.KnowledgeFolder
	folderErr       error
	lockedIDs       []string
	folderLookups   int
	folderListReads int
	updateCalls     []interfaces.KnowledgeFolderMoveUpdate
	pending         []*types.KnowledgeFolderIndexPending
}

func (r *knowledgeFolderMoveServiceTransaction) GetByID(
	_ context.Context,
	tenantID uint64,
	kbID string,
	folderID string,
) (*types.KnowledgeFolder, error) {
	r.folderLookups++
	if r.folderErr != nil {
		return nil, r.folderErr
	}
	if r.folder == nil {
		return nil, repository.ErrKnowledgeFolderNotFound
	}
	copy := *r.folder
	return &copy, nil
}

func (r *knowledgeFolderMoveServiceTransaction) ListByIDs(
	_ context.Context,
	tenantID uint64,
	kbID string,
	folderIDs []string,
) ([]*types.KnowledgeFolder, error) {
	r.folderListReads++
	if r.folderErr != nil {
		return nil, r.folderErr
	}
	if r.folder == nil {
		return nil, nil
	}
	copy := *r.folder
	return []*types.KnowledgeFolder{&copy}, nil
}

func (r *knowledgeFolderMoveServiceTransaction) LockKnowledgeForFolderMove(
	_ context.Context,
	tenantID uint64,
	kbID string,
	knowledgeIDs []string,
) ([]*types.Knowledge, error) {
	r.lockedIDs = append([]string(nil), knowledgeIDs...)
	if r.lockErr != nil {
		return nil, r.lockErr
	}
	result := make([]*types.Knowledge, len(r.knowledges))
	for index, knowledge := range r.knowledges {
		if knowledge == nil {
			continue
		}
		copy := *knowledge
		result[index] = &copy
	}
	return result, nil
}

func (r *knowledgeFolderMoveServiceTransaction) UpdateKnowledgeFolderForMove(
	_ context.Context,
	params interfaces.KnowledgeFolderMoveUpdate,
) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updateCalls = append(r.updateCalls, params)
	return nil
}

func (r *knowledgeFolderMoveServiceTransaction) UpsertKnowledgeFolderIndexPending(
	_ context.Context,
	pending *types.KnowledgeFolderIndexPending,
) error {
	if r.pendingErr != nil {
		return r.pendingErr
	}
	copy := *pending
	r.pending = append(r.pending, &copy)
	return nil
}

func knowledgeFolderMoveServiceTestContext() context.Context {
	return context.WithValue(
		context.Background(),
		types.TenantIDContextKey,
		uint64(7),
	)
}

func knowledgeFolderMoveServiceTestInput(
	knowledgeIDs []string,
	targetFolderID string,
) *types.KnowledgeFolderMoveInput {
	return &types.KnowledgeFolderMoveInput{
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		KnowledgeIDs:    knowledgeIDs,
		TargetFolderID:  targetFolderID,
	}
}

func knowledgeFolderMoveServiceTestKnowledge(
	id string,
	folderID string,
	version uint64,
	indexedVersion uint64,
) *types.Knowledge {
	return &types.Knowledge{
		ID:                   id,
		TenantID:             7,
		KnowledgeBaseID:      "kb-1",
		FolderID:             folderID,
		FolderVersion:        version,
		FolderIndexedVersion: indexedVersion,
		ParseStatus:          types.ParseStatusCompleted,
	}
}

func knowledgeFolderMoveServiceTestFolder() *types.KnowledgeFolder {
	return &types.KnowledgeFolder{
		ID:              knowledgeFolderMoveTestFolderA,
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		ParentID:        types.KnowledgeFolderRootID,
		Name:            "Folder",
		Path:            "/" + knowledgeFolderMoveTestFolderA + "/",
		Depth:           1,
	}
}

func newKnowledgeFolderMoveServiceTest(
	tx *knowledgeFolderMoveServiceTransaction,
) (interfaces.KnowledgeFolderMoveService, *knowledgeFolderMoveServiceRepository) {
	repo := &knowledgeFolderMoveServiceRepository{tx: tx}
	return NewKnowledgeFolderMoveService(repo), repo
}

func TestKnowledgeFolderMoveServiceMovesSingleKnowledgeToRoot(t *testing.T) {
	tx := &knowledgeFolderMoveServiceTransaction{
		knowledges: []*types.Knowledge{
			knowledgeFolderMoveServiceTestKnowledge(
				knowledgeFolderMoveTestKnowledgeA,
				knowledgeFolderMoveTestFolderA,
				1,
				0,
			),
		},
	}
	folderService, _ := newKnowledgeFolderMoveServiceTest(tx)

	result, err := folderService.MoveKnowledge(
		knowledgeFolderMoveServiceTestContext(),
		knowledgeFolderMoveServiceTestInput(
			[]string{knowledgeFolderMoveTestKnowledgeA},
			types.KnowledgeFolderRootID,
		),
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.ChangedCount)
	assert.Zero(t, result.UnchangedCount)
	require.Len(t, tx.updateCalls, 1)
	assert.Equal(t, types.KnowledgeFolderRootID, tx.updateCalls[0].TargetFolderID)
	assert.Equal(t, uint64(1), tx.updateCalls[0].ExpectedFolderVersion)
	require.Len(t, tx.pending, 1)
	pendingID, err := uuid.Parse(tx.pending[0].ID)
	require.NoError(t, err)
	assert.Equal(t, tx.pending[0].ID, pendingID.String())
	assert.Equal(t, uint64(2), tx.pending[0].RequestedVersion)
	assert.Equal(t, types.KnowledgeFolderRootID, tx.pending[0].TargetFolderID)
	assert.Zero(t, tx.folderLookups)
	assert.Zero(t, tx.folderListReads)
}

func TestKnowledgeFolderMoveServiceMovesSingleKnowledgeToValidatedFolder(t *testing.T) {
	tx := &knowledgeFolderMoveServiceTransaction{
		knowledges: []*types.Knowledge{
			knowledgeFolderMoveServiceTestKnowledge(
				knowledgeFolderMoveTestKnowledgeA,
				types.KnowledgeFolderRootID,
				4,
				2,
			),
		},
		folder: knowledgeFolderMoveServiceTestFolder(),
	}
	folderService, _ := newKnowledgeFolderMoveServiceTest(tx)

	result, err := folderService.MoveKnowledge(
		knowledgeFolderMoveServiceTestContext(),
		knowledgeFolderMoveServiceTestInput(
			[]string{knowledgeFolderMoveTestKnowledgeA},
			knowledgeFolderMoveTestFolderA,
		),
	)

	require.NoError(t, err)
	assert.Equal(t, &types.KnowledgeFolderMoveResult{
		ChangedCount:   1,
		UnchangedCount: 0,
	}, result)
	assert.Equal(t, 1, tx.folderLookups)
	assert.Equal(t, 1, tx.folderListReads)
	require.Len(t, tx.pending, 1)
	assert.Equal(t, uint64(5), tx.pending[0].RequestedVersion)
}

func TestKnowledgeFolderMoveServiceDeduplicatesAndSortsBatch(t *testing.T) {
	tx := &knowledgeFolderMoveServiceTransaction{
		knowledges: []*types.Knowledge{
			knowledgeFolderMoveServiceTestKnowledge(
				knowledgeFolderMoveTestKnowledgeA,
				knowledgeFolderMoveTestFolderB,
				5,
				3,
			),
			knowledgeFolderMoveServiceTestKnowledge(
				knowledgeFolderMoveTestKnowledgeB,
				knowledgeFolderMoveTestFolderA,
				8,
				7,
			),
		},
		folder: knowledgeFolderMoveServiceTestFolder(),
	}
	folderService, _ := newKnowledgeFolderMoveServiceTest(tx)

	result, err := folderService.MoveKnowledge(
		knowledgeFolderMoveServiceTestContext(),
		knowledgeFolderMoveServiceTestInput(
			[]string{
				knowledgeFolderMoveTestKnowledgeB,
				knowledgeFolderMoveTestKnowledgeA,
				knowledgeFolderMoveTestKnowledgeB,
			},
			knowledgeFolderMoveTestFolderA,
		),
	)

	require.NoError(t, err)
	assert.Equal(t, 1, result.ChangedCount)
	assert.Equal(t, 1, result.UnchangedCount)
	assert.Equal(t, []string{
		knowledgeFolderMoveTestKnowledgeA,
		knowledgeFolderMoveTestKnowledgeB,
	}, tx.lockedIDs)
	require.Len(t, tx.updateCalls, 1)
	assert.Equal(t, knowledgeFolderMoveTestKnowledgeA, tx.updateCalls[0].KnowledgeID)
	require.Len(t, tx.pending, 1)
	assert.Equal(t, knowledgeFolderMoveTestKnowledgeA, tx.pending[0].KnowledgeID)
}

func TestKnowledgeFolderMoveServiceNoOpDoesNotWriteOrIncrement(t *testing.T) {
	for _, knowledges := range [][]*types.Knowledge{
		{
			knowledgeFolderMoveServiceTestKnowledge(
				knowledgeFolderMoveTestKnowledgeA,
				knowledgeFolderMoveTestFolderA,
				6,
				4,
			),
		},
		{
			knowledgeFolderMoveServiceTestKnowledge(
				knowledgeFolderMoveTestKnowledgeA,
				knowledgeFolderMoveTestFolderA,
				6,
				4,
			),
			knowledgeFolderMoveServiceTestKnowledge(
				knowledgeFolderMoveTestKnowledgeB,
				knowledgeFolderMoveTestFolderA,
				9,
				8,
			),
		},
	} {
		tx := &knowledgeFolderMoveServiceTransaction{
			knowledges: knowledges,
			folder:     knowledgeFolderMoveServiceTestFolder(),
		}
		folderService, _ := newKnowledgeFolderMoveServiceTest(tx)
		ids := make([]string, len(knowledges))
		for index, knowledge := range knowledges {
			ids[index] = knowledge.ID
		}

		result, err := folderService.MoveKnowledge(
			knowledgeFolderMoveServiceTestContext(),
			knowledgeFolderMoveServiceTestInput(ids, knowledgeFolderMoveTestFolderA),
		)

		require.NoError(t, err)
		assert.Zero(t, result.ChangedCount)
		assert.Equal(t, len(knowledges), result.UnchangedCount)
		assert.Empty(t, tx.updateCalls)
		assert.Empty(t, tx.pending)
	}
}

func TestKnowledgeFolderMoveServiceAllowsProcessingKnowledge(t *testing.T) {
	knowledge := knowledgeFolderMoveServiceTestKnowledge(
		knowledgeFolderMoveTestKnowledgeA,
		types.KnowledgeFolderRootID,
		1,
		0,
	)
	knowledge.ParseStatus = types.ParseStatusProcessing
	tx := &knowledgeFolderMoveServiceTransaction{
		knowledges: []*types.Knowledge{knowledge},
		folder:     knowledgeFolderMoveServiceTestFolder(),
	}
	folderService, _ := newKnowledgeFolderMoveServiceTest(tx)

	result, err := folderService.MoveKnowledge(
		knowledgeFolderMoveServiceTestContext(),
		knowledgeFolderMoveServiceTestInput(
			[]string{knowledgeFolderMoveTestKnowledgeA},
			knowledgeFolderMoveTestFolderA,
		),
	)

	require.NoError(t, err)
	assert.Equal(t, 1, result.ChangedCount)
}

func TestKnowledgeFolderMoveServiceRejectsDeletingKnowledgeAsUnavailable(
	t *testing.T,
) {
	tests := []struct {
		name           string
		targetFolderID string
		folder         *types.KnowledgeFolder
	}{
		{
			name:           "changed placement",
			targetFolderID: types.KnowledgeFolderRootID,
		},
		{
			name:           "already in target",
			targetFolderID: knowledgeFolderMoveTestFolderA,
			folder:         knowledgeFolderMoveServiceTestFolder(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			knowledge := knowledgeFolderMoveServiceTestKnowledge(
				knowledgeFolderMoveTestKnowledgeA,
				knowledgeFolderMoveTestFolderA,
				4,
				2,
			)
			knowledge.ParseStatus = types.ParseStatusDeleting
			tx := &knowledgeFolderMoveServiceTransaction{
				knowledges: []*types.Knowledge{knowledge},
				folder:     tt.folder,
			}
			folderService, _ := newKnowledgeFolderMoveServiceTest(tx)

			result, err := folderService.MoveKnowledge(
				knowledgeFolderMoveServiceTestContext(),
				knowledgeFolderMoveServiceTestInput(
					[]string{knowledgeFolderMoveTestKnowledgeA},
					tt.targetFolderID,
				),
			)

			assert.Nil(t, result)
			assert.ErrorIs(t, err, ErrKnowledgeFolderMoveKnowledgeNotFound)
			assert.NotContains(t, err.Error(), knowledgeFolderMoveTestKnowledgeA)
			assert.NotContains(t, err.Error(), types.ParseStatusDeleting)
			assert.Empty(t, tx.updateCalls)
			assert.Empty(t, tx.pending)
		})
	}
}

func TestKnowledgeFolderMoveServiceRejectsMixedDeletingBatchBeforeWrites(
	t *testing.T,
) {
	deleting := knowledgeFolderMoveServiceTestKnowledge(
		knowledgeFolderMoveTestKnowledgeB,
		knowledgeFolderMoveTestFolderB,
		7,
		4,
	)
	deleting.ParseStatus = types.ParseStatusDeleting
	tx := &knowledgeFolderMoveServiceTransaction{
		knowledges: []*types.Knowledge{
			knowledgeFolderMoveServiceTestKnowledge(
				knowledgeFolderMoveTestKnowledgeA,
				knowledgeFolderMoveTestFolderA,
				5,
				3,
			),
			deleting,
		},
	}
	folderService, _ := newKnowledgeFolderMoveServiceTest(tx)

	result, err := folderService.MoveKnowledge(
		knowledgeFolderMoveServiceTestContext(),
		knowledgeFolderMoveServiceTestInput(
			[]string{
				knowledgeFolderMoveTestKnowledgeA,
				knowledgeFolderMoveTestKnowledgeB,
			},
			types.KnowledgeFolderRootID,
		),
	)

	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrKnowledgeFolderMoveKnowledgeNotFound)
	assert.Empty(t, tx.updateCalls)
	assert.Empty(t, tx.pending)
}

func TestKnowledgeFolderMoveServiceRejectsInvalidInputBeforeTransaction(t *testing.T) {
	valid := knowledgeFolderMoveServiceTestInput(
		[]string{knowledgeFolderMoveTestKnowledgeA},
		types.KnowledgeFolderRootID,
	)
	tooMany := make([]string, knowledgeFolderMoveMaxKnowledgeIDs+1)
	for index := range tooMany {
		tooMany[index] = knowledgeFolderMoveTestKnowledgeA
	}
	mismatchedTenant := *valid
	mismatchedTenant.TenantID = 8
	emptyKB := *valid
	emptyKB.KnowledgeBaseID = ""
	emptyIDs := *valid
	emptyIDs.KnowledgeIDs = nil
	oversized := *valid
	oversized.KnowledgeIDs = tooMany
	malformedKnowledge := *valid
	malformedKnowledge.KnowledgeIDs = []string{"not-a-uuid"}
	malformedFolder := *valid
	malformedFolder.TargetFolderID = "not-a-uuid"
	whitespaceFolder := *valid
	whitespaceFolder.TargetFolderID = " " + knowledgeFolderMoveTestFolderA

	tests := []struct {
		name  string
		input *types.KnowledgeFolderMoveInput
	}{
		{name: "nil input", input: nil},
		{name: "tenant mismatch", input: &mismatchedTenant},
		{name: "empty knowledge base", input: &emptyKB},
		{name: "empty ids", input: &emptyIDs},
		{name: "raw count over limit", input: &oversized},
		{name: "malformed knowledge id", input: &malformedKnowledge},
		{name: "malformed folder id", input: &malformedFolder},
		{name: "folder id whitespace", input: &whitespaceFolder},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := &knowledgeFolderMoveServiceTransaction{}
			folderService, repo := newKnowledgeFolderMoveServiceTest(tx)

			result, err := folderService.MoveKnowledge(
				knowledgeFolderMoveServiceTestContext(),
				tt.input,
			)

			assert.Nil(t, result)
			assert.ErrorIs(t, err, ErrKnowledgeFolderInvalidArgument)
			assert.Zero(t, repo.runCalls)
		})
	}
}

func TestKnowledgeFolderMoveServiceMapsFolderScopeAndIntegrityFailures(t *testing.T) {
	corrupt := knowledgeFolderMoveServiceTestFolder()
	corrupt.Path = "/corrupt/"

	tests := []struct {
		name      string
		folder    *types.KnowledgeFolder
		folderErr error
		expected  error
	}{
		{
			name:      "missing",
			folderErr: repository.ErrKnowledgeFolderNotFound,
			expected:  ErrKnowledgeFolderNotFound,
		},
		{
			name:      "wrong tenant",
			folderErr: repository.ErrKnowledgeFolderNotFound,
			expected:  ErrKnowledgeFolderNotFound,
		},
		{
			name:      "wrong knowledge base",
			folderErr: repository.ErrKnowledgeFolderNotFound,
			expected:  ErrKnowledgeFolderNotFound,
		},
		{
			name:      "deleted",
			folderErr: repository.ErrKnowledgeFolderNotFound,
			expected:  ErrKnowledgeFolderNotFound,
		},
		{name: "corrupt chain", folder: corrupt, expected: ErrKnowledgeFolderDataIntegrity},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := &knowledgeFolderMoveServiceTransaction{
				knowledges: []*types.Knowledge{
					knowledgeFolderMoveServiceTestKnowledge(
						knowledgeFolderMoveTestKnowledgeA,
						types.KnowledgeFolderRootID,
						1,
						0,
					),
				},
				folder:    tt.folder,
				folderErr: tt.folderErr,
			}
			folderService, _ := newKnowledgeFolderMoveServiceTest(tx)

			result, err := folderService.MoveKnowledge(
				knowledgeFolderMoveServiceTestContext(),
				knowledgeFolderMoveServiceTestInput(
					[]string{knowledgeFolderMoveTestKnowledgeA},
					knowledgeFolderMoveTestFolderA,
				),
			)

			assert.Nil(t, result)
			assert.ErrorIs(t, err, tt.expected)
			assert.Empty(t, tx.updateCalls)
			assert.Empty(t, tx.pending)
		})
	}
}

func TestKnowledgeFolderMoveServiceMapsMissingKnowledgeWithoutIdentifyingIt(t *testing.T) {
	tx := &knowledgeFolderMoveServiceTransaction{
		lockErr: repository.ErrKnowledgeFolderMoveKnowledgeNotFound,
	}
	folderService, _ := newKnowledgeFolderMoveServiceTest(tx)

	result, err := folderService.MoveKnowledge(
		knowledgeFolderMoveServiceTestContext(),
		knowledgeFolderMoveServiceTestInput(
			[]string{knowledgeFolderMoveTestKnowledgeA},
			types.KnowledgeFolderRootID,
		),
	)

	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrKnowledgeFolderMoveKnowledgeNotFound)
	assert.NotContains(t, err.Error(), knowledgeFolderMoveTestKnowledgeA)
}

func TestKnowledgeFolderMoveServiceRejectsCorruptOrOverflowVersions(t *testing.T) {
	for _, version := range []uint64{0, math.MaxInt64} {
		tx := &knowledgeFolderMoveServiceTransaction{
			knowledges: []*types.Knowledge{
				knowledgeFolderMoveServiceTestKnowledge(
					knowledgeFolderMoveTestKnowledgeA,
					knowledgeFolderMoveTestFolderA,
					version,
					0,
				),
			},
		}
		folderService, _ := newKnowledgeFolderMoveServiceTest(tx)

		result, err := folderService.MoveKnowledge(
			knowledgeFolderMoveServiceTestContext(),
			knowledgeFolderMoveServiceTestInput(
				[]string{knowledgeFolderMoveTestKnowledgeA},
				types.KnowledgeFolderRootID,
			),
		)

		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
		assert.Empty(t, tx.updateCalls)
		assert.Empty(t, tx.pending)
	}
}

func TestKnowledgeFolderMoveServiceAllowsValidIndexedVersionRelations(t *testing.T) {
	tests := []struct {
		name           string
		version        uint64
		indexedVersion uint64
	}{
		{name: "indexed equals authoritative", version: 5, indexedVersion: 5},
		{name: "indexed lags authoritative", version: 5, indexedVersion: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := &knowledgeFolderMoveServiceTransaction{
				knowledges: []*types.Knowledge{
					knowledgeFolderMoveServiceTestKnowledge(
						knowledgeFolderMoveTestKnowledgeA,
						knowledgeFolderMoveTestFolderA,
						tt.version,
						tt.indexedVersion,
					),
				},
			}
			folderService, _ := newKnowledgeFolderMoveServiceTest(tx)

			result, err := folderService.MoveKnowledge(
				knowledgeFolderMoveServiceTestContext(),
				knowledgeFolderMoveServiceTestInput(
					[]string{knowledgeFolderMoveTestKnowledgeA},
					types.KnowledgeFolderRootID,
				),
			)

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, 1, result.ChangedCount)
			require.Len(t, tx.updateCalls, 1)
			assert.Equal(t, tt.version, tx.updateCalls[0].ExpectedFolderVersion)
			require.Len(t, tx.pending, 1)
			assert.Equal(t, tt.version+1, tx.pending[0].RequestedVersion)
		})
	}
}

func TestKnowledgeFolderMoveServiceRejectsIndexedVersionAheadBeforeWrites(
	t *testing.T,
) {
	tx := &knowledgeFolderMoveServiceTransaction{
		knowledges: []*types.Knowledge{
			knowledgeFolderMoveServiceTestKnowledge(
				knowledgeFolderMoveTestKnowledgeA,
				knowledgeFolderMoveTestFolderA,
				5,
				3,
			),
			knowledgeFolderMoveServiceTestKnowledge(
				knowledgeFolderMoveTestKnowledgeB,
				knowledgeFolderMoveTestFolderB,
				7,
				8,
			),
		},
	}
	folderService, _ := newKnowledgeFolderMoveServiceTest(tx)

	result, err := folderService.MoveKnowledge(
		knowledgeFolderMoveServiceTestContext(),
		knowledgeFolderMoveServiceTestInput(
			[]string{
				knowledgeFolderMoveTestKnowledgeA,
				knowledgeFolderMoveTestKnowledgeB,
			},
			types.KnowledgeFolderRootID,
		),
	)

	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
	assert.Empty(t, tx.updateCalls)
	assert.Empty(t, tx.pending)
}

func TestKnowledgeFolderMoveServiceAcceptsValidRowsInDifferentDatabaseOrder(
	t *testing.T,
) {
	tx := &knowledgeFolderMoveServiceTransaction{
		knowledges: []*types.Knowledge{
			knowledgeFolderMoveServiceTestKnowledge(
				knowledgeFolderMoveTestKnowledgeB,
				knowledgeFolderMoveTestFolderB,
				7,
				4,
			),
			knowledgeFolderMoveServiceTestKnowledge(
				knowledgeFolderMoveTestKnowledgeA,
				knowledgeFolderMoveTestFolderA,
				5,
				3,
			),
		},
	}
	folderService, _ := newKnowledgeFolderMoveServiceTest(tx)

	result, err := folderService.MoveKnowledge(
		knowledgeFolderMoveServiceTestContext(),
		knowledgeFolderMoveServiceTestInput(
			[]string{
				knowledgeFolderMoveTestKnowledgeB,
				knowledgeFolderMoveTestKnowledgeA,
			},
			types.KnowledgeFolderRootID,
		),
	)

	require.NoError(t, err)
	assert.Equal(t, &types.KnowledgeFolderMoveResult{
		ChangedCount:   2,
		UnchangedCount: 0,
	}, result)
	require.Len(t, tx.updateCalls, 2)
	assert.Equal(t, knowledgeFolderMoveTestKnowledgeA, tx.updateCalls[0].KnowledgeID)
	assert.Equal(t, knowledgeFolderMoveTestKnowledgeB, tx.updateCalls[1].KnowledgeID)
	require.Len(t, tx.pending, 2)
	assert.Equal(t, knowledgeFolderMoveTestKnowledgeA, tx.pending[0].KnowledgeID)
	assert.Equal(t, knowledgeFolderMoveTestKnowledgeB, tx.pending[1].KnowledgeID)
}

func TestKnowledgeFolderMoveServiceRejectsInvalidReturnedRowSetsBeforeWrites(
	t *testing.T,
) {
	validA := knowledgeFolderMoveServiceTestKnowledge(
		knowledgeFolderMoveTestKnowledgeA,
		knowledgeFolderMoveTestFolderA,
		5,
		3,
	)
	validB := knowledgeFolderMoveServiceTestKnowledge(
		knowledgeFolderMoveTestKnowledgeB,
		knowledgeFolderMoveTestFolderB,
		7,
		4,
	)
	unexpected := knowledgeFolderMoveServiceTestKnowledge(
		knowledgeFolderMoveTestKnowledgeC,
		knowledgeFolderMoveTestFolderB,
		7,
		4,
	)
	wrongTenant := *validB
	wrongTenant.TenantID = 8
	wrongKB := *validB
	wrongKB.KnowledgeBaseID = "kb-other"
	deleted := *validB
	deleted.DeletedAt.Valid = true

	tests := []struct {
		name       string
		knowledges []*types.Knowledge
		expected   error
	}{
		{
			name:       "nil row",
			knowledges: []*types.Knowledge{validA, nil},
			expected:   ErrKnowledgeFolderDataIntegrity,
		},
		{
			name:       "duplicate returned id",
			knowledges: []*types.Knowledge{validA, validA},
			expected:   ErrKnowledgeFolderDataIntegrity,
		},
		{
			name:       "unexpected returned id",
			knowledges: []*types.Knowledge{validA, unexpected},
			expected:   ErrKnowledgeFolderDataIntegrity,
		},
		{
			name:       "missing returned id",
			knowledges: []*types.Knowledge{validA},
			expected:   ErrKnowledgeFolderMoveKnowledgeNotFound,
		},
		{
			name:       "wrong tenant",
			knowledges: []*types.Knowledge{validA, &wrongTenant},
			expected:   ErrKnowledgeFolderDataIntegrity,
		},
		{
			name:       "wrong knowledge base",
			knowledges: []*types.Knowledge{validA, &wrongKB},
			expected:   ErrKnowledgeFolderDataIntegrity,
		},
		{
			name:       "soft deleted",
			knowledges: []*types.Knowledge{validA, &deleted},
			expected:   ErrKnowledgeFolderDataIntegrity,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := &knowledgeFolderMoveServiceTransaction{
				knowledges: tt.knowledges,
			}
			folderService, _ := newKnowledgeFolderMoveServiceTest(tx)

			result, err := folderService.MoveKnowledge(
				knowledgeFolderMoveServiceTestContext(),
				knowledgeFolderMoveServiceTestInput(
					[]string{
						knowledgeFolderMoveTestKnowledgeA,
						knowledgeFolderMoveTestKnowledgeB,
					},
					types.KnowledgeFolderRootID,
				),
			)

			assert.Nil(t, result)
			assert.ErrorIs(t, err, tt.expected)
			assert.Empty(t, tx.updateCalls)
			assert.Empty(t, tx.pending)
		})
	}
}

func TestKnowledgeFolderMoveServicePropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(knowledgeFolderMoveServiceTestContext())
	cancel()
	tx := &knowledgeFolderMoveServiceTransaction{}
	folderService, repo := newKnowledgeFolderMoveServiceTest(tx)

	result, err := folderService.MoveKnowledge(
		ctx,
		knowledgeFolderMoveServiceTestInput(
			[]string{knowledgeFolderMoveTestKnowledgeA},
			types.KnowledgeFolderRootID,
		),
	)

	assert.Nil(t, result)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, repo.runCalls)
}

func TestKnowledgeFolderMoveServiceMapsRepositoryFailures(t *testing.T) {
	tests := []struct {
		name     string
		repo     *knowledgeFolderMoveServiceRepository
		expected error
	}{
		{
			name: "transaction",
			repo: &knowledgeFolderMoveServiceRepository{
				tx:     &knowledgeFolderMoveServiceTransaction{},
				runErr: errors.New("driver detail"),
			},
			expected: ErrKnowledgeFolderInternal,
		},
		{
			name: "conditional update",
			repo: &knowledgeFolderMoveServiceRepository{
				tx: &knowledgeFolderMoveServiceTransaction{
					knowledges: []*types.Knowledge{
						knowledgeFolderMoveServiceTestKnowledge(
							knowledgeFolderMoveTestKnowledgeA,
							knowledgeFolderMoveTestFolderA,
							1,
							0,
						),
					},
					updateErr: repository.ErrKnowledgeFolderMoveDataIntegrity,
				},
			},
			expected: ErrKnowledgeFolderDataIntegrity,
		},
		{
			name: "pending",
			repo: &knowledgeFolderMoveServiceRepository{
				tx: &knowledgeFolderMoveServiceTransaction{
					knowledges: []*types.Knowledge{
						knowledgeFolderMoveServiceTestKnowledge(
							knowledgeFolderMoveTestKnowledgeA,
							knowledgeFolderMoveTestFolderA,
							1,
							0,
						),
					},
					pendingErr: errors.New("table detail"),
				},
			},
			expected: ErrKnowledgeFolderInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			folderService := NewKnowledgeFolderMoveService(tt.repo)

			result, err := folderService.MoveKnowledge(
				knowledgeFolderMoveServiceTestContext(),
				knowledgeFolderMoveServiceTestInput(
					[]string{knowledgeFolderMoveTestKnowledgeA},
					types.KnowledgeFolderRootID,
				),
			)

			assert.Nil(t, result)
			assert.ErrorIs(t, err, tt.expected)
		})
	}
}

func TestKnowledgeFolderMoveServiceUsesStableKnowledgeOrder(t *testing.T) {
	tx := &knowledgeFolderMoveServiceTransaction{
		knowledges: []*types.Knowledge{
			knowledgeFolderMoveServiceTestKnowledge(
				knowledgeFolderMoveTestKnowledgeA,
				knowledgeFolderMoveTestFolderA,
				1,
				0,
			),
			knowledgeFolderMoveServiceTestKnowledge(
				knowledgeFolderMoveTestKnowledgeB,
				knowledgeFolderMoveTestFolderA,
				1,
				0,
			),
		},
	}
	folderService, _ := newKnowledgeFolderMoveServiceTest(tx)
	inputIDs := []string{
		knowledgeFolderMoveTestKnowledgeB,
		knowledgeFolderMoveTestKnowledgeA,
	}

	_, err := folderService.MoveKnowledge(
		knowledgeFolderMoveServiceTestContext(),
		knowledgeFolderMoveServiceTestInput(inputIDs, types.KnowledgeFolderRootID),
	)

	require.NoError(t, err)
	assert.True(t, sort.StringsAreSorted(tx.lockedIDs))
	assert.Equal(t, inputIDs, []string{
		knowledgeFolderMoveTestKnowledgeB,
		knowledgeFolderMoveTestKnowledgeA,
	})
}
