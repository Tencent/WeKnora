package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type knowledgeFolderEnsurePathsTestBatchCall struct {
	attempt  int
	tenantID uint64
	kbID     string
	parentID string
	names    []string
}

type knowledgeFolderEnsurePathsTestPointCall struct {
	attempt  int
	tenantID uint64
	kbID     string
	parentID string
	name     string
}

type knowledgeFolderEnsurePathsTestRepository struct {
	interfaces.KnowledgeFolderRepository
	folders map[string]*types.KnowledgeFolder

	transactionCalls  int
	callbackCalls     int
	transactionTenant uint64
	transactionKB     string
	replayAttempts    int
	skipCallback      bool
	transactionErr    error

	batchCalls                   []knowledgeFolderEnsurePathsTestBatchCall
	pointCalls                   []knowledgeFolderEnsurePathsTestPointCall
	createIfAbsentCallsByAttempt [][]*types.KnowledgeFolder
	getByIDCalls                 int
	listByIDsCalls               int
	listByIDsOverride            func(*knowledgeFolderEnsurePathsTestTreeRepository, []string) ([]*types.KnowledgeFolder, error)
	listOverride                 func(*knowledgeFolderEnsurePathsTestTreeRepository, string, []string) ([]*types.KnowledgeFolder, error)
	pointOverride                func(*knowledgeFolderEnsurePathsTestTreeRepository, string, string) (*types.KnowledgeFolder, error)
	createIfAbsentOverride       func(*knowledgeFolderEnsurePathsTestTreeRepository, *types.KnowledgeFolder) (bool, error)
}

type knowledgeFolderEnsurePathsTestTreeRepository struct {
	interfaces.KnowledgeFolderTreeRepository
	owner   *knowledgeFolderEnsurePathsTestRepository
	attempt int
	state   map[string]*types.KnowledgeFolder
}

func newKnowledgeFolderEnsurePathsTestRepository(
	folders ...*types.KnowledgeFolder,
) *knowledgeFolderEnsurePathsTestRepository {
	repo := &knowledgeFolderEnsurePathsTestRepository{
		folders: make(map[string]*types.KnowledgeFolder, len(folders)),
	}
	for _, folder := range folders {
		copyOfFolder := *folder
		repo.folders[folder.ID] = &copyOfFolder
	}
	return repo
}

func (r *knowledgeFolderEnsurePathsTestRepository) RunTreeWriteTransaction(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	fn interfaces.KnowledgeFolderTreeWriteFunc,
) error {
	r.transactionCalls++
	r.transactionTenant = tenantID
	r.transactionKB = kbID
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.transactionErr != nil {
		return r.transactionErr
	}
	if r.skipCallback {
		return nil
	}

	attempts := r.replayAttempts
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		r.callbackCalls++
		for len(r.createIfAbsentCallsByAttempt) <= attempt {
			r.createIfAbsentCallsByAttempt = append(r.createIfAbsentCallsByAttempt, nil)
		}
		txRepo := &knowledgeFolderEnsurePathsTestTreeRepository{
			owner:   r,
			attempt: attempt,
			state:   cloneKnowledgeFolderEnsurePathsTestState(r.folders),
		}
		err := fn(txRepo)
		if err != nil {
			if attempt+1 < attempts && isKnowledgeFolderEnsurePathsTestBusy(err) {
				continue
			}
			return err
		}
		if attempt+1 < attempts {
			continue
		}
		r.folders = txRepo.state
		return nil
	}
	return nil
}

func (r *knowledgeFolderEnsurePathsTestTreeRepository) GetByID(
	_ context.Context,
	tenantID uint64,
	kbID string,
	folderID string,
) (*types.KnowledgeFolder, error) {
	r.owner.getByIDCalls++
	folder := r.state[folderID]
	if folder == nil ||
		folder.DeletedAt.Valid ||
		folder.TenantID != tenantID ||
		folder.KnowledgeBaseID != kbID {
		return nil, repository.ErrKnowledgeFolderNotFound
	}
	return folder, nil
}

func (r *knowledgeFolderEnsurePathsTestTreeRepository) ListByIDs(
	_ context.Context,
	tenantID uint64,
	kbID string,
	folderIDs []string,
) ([]*types.KnowledgeFolder, error) {
	r.owner.listByIDsCalls++
	if r.owner.listByIDsOverride != nil {
		return r.owner.listByIDsOverride(r, append([]string(nil), folderIDs...))
	}
	folders := make([]*types.KnowledgeFolder, 0, len(folderIDs))
	for index := len(folderIDs) - 1; index >= 0; index-- {
		folder := r.state[folderIDs[index]]
		if folder == nil ||
			folder.DeletedAt.Valid ||
			folder.TenantID != tenantID ||
			folder.KnowledgeBaseID != kbID {
			continue
		}
		folders = append(folders, folder)
	}
	return folders, nil
}

func (r *knowledgeFolderEnsurePathsTestTreeRepository) ListByParentAndNames(
	_ context.Context,
	tenantID uint64,
	kbID string,
	parentID string,
	names []string,
) ([]*types.KnowledgeFolder, error) {
	r.owner.batchCalls = append(r.owner.batchCalls, knowledgeFolderEnsurePathsTestBatchCall{
		attempt:  r.attempt,
		tenantID: tenantID,
		kbID:     kbID,
		parentID: parentID,
		names:    append([]string(nil), names...),
	})
	if r.owner.listOverride != nil {
		return r.owner.listOverride(r, parentID, append([]string(nil), names...))
	}
	expected := make(map[string]struct{}, len(names))
	for _, name := range names {
		expected[name] = struct{}{}
	}
	folders := make([]*types.KnowledgeFolder, 0, len(names))
	for _, folder := range r.state {
		if folder.DeletedAt.Valid ||
			folder.TenantID != tenantID ||
			folder.KnowledgeBaseID != kbID ||
			folder.ParentID != parentID {
			continue
		}
		if _, ok := expected[folder.Name]; ok {
			folders = append(folders, folder)
		}
	}
	sort.Slice(folders, func(i int, j int) bool {
		if folders[i].Name == folders[j].Name {
			return folders[i].ID > folders[j].ID
		}
		return folders[i].Name > folders[j].Name
	})
	return folders, nil
}

func (r *knowledgeFolderEnsurePathsTestTreeRepository) GetByParentAndName(
	_ context.Context,
	tenantID uint64,
	kbID string,
	parentID string,
	name string,
) (*types.KnowledgeFolder, error) {
	r.owner.pointCalls = append(r.owner.pointCalls, knowledgeFolderEnsurePathsTestPointCall{
		attempt:  r.attempt,
		tenantID: tenantID,
		kbID:     kbID,
		parentID: parentID,
		name:     name,
	})
	if r.owner.pointOverride != nil {
		return r.owner.pointOverride(r, parentID, name)
	}
	var matches []*types.KnowledgeFolder
	for _, folder := range r.state {
		if !folder.DeletedAt.Valid &&
			folder.TenantID == tenantID &&
			folder.KnowledgeBaseID == kbID &&
			folder.ParentID == parentID &&
			folder.Name == name {
			matches = append(matches, folder)
		}
	}
	if len(matches) == 0 {
		return nil, repository.ErrKnowledgeFolderNotFound
	}
	sort.Slice(matches, func(i int, j int) bool { return matches[i].ID < matches[j].ID })
	return matches[0], nil
}

func (r *knowledgeFolderEnsurePathsTestTreeRepository) Create(
	_ context.Context,
	folder *types.KnowledgeFolder,
) error {
	if _, err := types.ValidateKnowledgeFolderStructure(folder); err != nil {
		return fmt.Errorf("%w: %v", repository.ErrKnowledgeFolderInvalid, err)
	}
	for _, existing := range r.state {
		if existing.ID == folder.ID ||
			(!existing.DeletedAt.Valid &&
				existing.TenantID == folder.TenantID &&
				existing.KnowledgeBaseID == folder.KnowledgeBaseID &&
				existing.ParentID == folder.ParentID &&
				existing.Name == folder.Name) {
			return repository.ErrKnowledgeFolderConflict
		}
	}
	copyForState := *folder
	r.state[folder.ID] = &copyForState
	return nil
}

func (r *knowledgeFolderEnsurePathsTestTreeRepository) CreateIfAbsent(
	_ context.Context,
	folder *types.KnowledgeFolder,
) (bool, error) {
	copyOfFolder := *folder
	r.owner.createIfAbsentCallsByAttempt[r.attempt] = append(
		r.owner.createIfAbsentCallsByAttempt[r.attempt],
		&copyOfFolder,
	)
	if r.owner.createIfAbsentOverride != nil {
		return r.owner.createIfAbsentOverride(r, folder)
	}
	return r.createIfAbsentDefault(folder)
}

func (r *knowledgeFolderEnsurePathsTestTreeRepository) createIfAbsentDefault(
	folder *types.KnowledgeFolder,
) (bool, error) {
	if _, err := types.ValidateKnowledgeFolderStructure(folder); err != nil {
		return false, fmt.Errorf("%w: %v", repository.ErrKnowledgeFolderInvalid, err)
	}
	for _, existing := range r.state {
		if existing.ID == folder.ID {
			return false, nil
		}
		if !existing.DeletedAt.Valid &&
			existing.TenantID == folder.TenantID &&
			existing.KnowledgeBaseID == folder.KnowledgeBaseID &&
			existing.ParentID == folder.ParentID &&
			existing.Name == folder.Name {
			return false, nil
		}
	}
	copyForState := *folder
	r.state[folder.ID] = &copyForState
	return true, nil
}

func cloneKnowledgeFolderEnsurePathsTestState(
	source map[string]*types.KnowledgeFolder,
) map[string]*types.KnowledgeFolder {
	clone := make(map[string]*types.KnowledgeFolder, len(source))
	for id, folder := range source {
		copyOfFolder := *folder
		clone[id] = &copyOfFolder
	}
	return clone
}

func isKnowledgeFolderEnsurePathsTestBusy(err error) bool {
	var sqliteErr sqlite3.Error
	return errors.As(err, &sqliteErr) &&
		(sqliteErr.Code == sqlite3.ErrBusy || sqliteErr.Code == sqlite3.ErrLocked)
}

func knowledgeFolderEnsurePathsTestContext() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
}

func knowledgeFolderEnsurePathsTestInput(
	clientKey string,
	segments ...string,
) types.KnowledgeFolderEnsurePathInput {
	return types.KnowledgeFolderEnsurePathInput{
		ClientKey: clientKey,
		Segments:  segments,
	}
}

func knowledgeFolderEnsurePathsTestRequest(
	parentID string,
	paths ...types.KnowledgeFolderEnsurePathInput,
) *types.KnowledgeFolderEnsurePathsRequest {
	return &types.KnowledgeFolderEnsurePathsRequest{ParentID: parentID, Paths: paths}
}

func cloneKnowledgeFolderEnsurePathsTestRequest(
	req *types.KnowledgeFolderEnsurePathsRequest,
) *types.KnowledgeFolderEnsurePathsRequest {
	if req == nil {
		return nil
	}
	clone := &types.KnowledgeFolderEnsurePathsRequest{
		ParentID: req.ParentID,
		Paths:    append([]types.KnowledgeFolderEnsurePathInput(nil), req.Paths...),
	}
	for index := range clone.Paths {
		clone.Paths[index].Segments = append([]string(nil), req.Paths[index].Segments...)
	}
	return clone
}

func knowledgeFolderEnsurePathsTestChain(depth int) []*types.KnowledgeFolder {
	folders := make([]*types.KnowledgeFolder, 0, depth)
	parentID := types.KnowledgeFolderRootID
	parentPath := ""
	for level := 1; level <= depth; level++ {
		id := knowledgeFolderServiceTestID(fmt.Sprintf("ensure-parent-%02d", level))
		path := knowledgeFolderChildPath(parentPath, id)
		folders = append(folders, &types.KnowledgeFolder{
			ID:              id,
			TenantID:        1,
			KnowledgeBaseID: "kb-1",
			ParentID:        parentID,
			Name:            fmt.Sprintf("Parent %02d", level),
			Path:            path,
			Depth:           level,
		})
		parentID = id
		parentPath = path
	}
	return folders
}

func knowledgeFolderEnsurePathsTestChild(
	label string,
	name string,
	parent *types.KnowledgeFolder,
) *types.KnowledgeFolder {
	parentID := types.KnowledgeFolderRootID
	parentPath := ""
	depth := 1
	if parent != nil {
		parentID = parent.ID
		parentPath = parent.Path
		depth = parent.Depth + 1
	}
	id := knowledgeFolderServiceTestID(label)
	return &types.KnowledgeFolder{
		ID:              id,
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParentID:        parentID,
		Name:            name,
		Path:            knowledgeFolderChildPath(parentPath, id),
		Depth:           depth,
	}
}

func knowledgeFolderEnsurePathsTestFindByName(
	repo *knowledgeFolderEnsurePathsTestRepository,
	name string,
) *types.KnowledgeFolder {
	for _, folder := range repo.folders {
		if !folder.DeletedAt.Valid && folder.Name == name {
			return folder
		}
	}
	return nil
}

func TestKnowledgeFolderServiceEnsurePathsRejectsInvalidRequestsBeforeTransaction(t *testing.T) {
	canonicalParentID := "7b5584d0-73f5-4fd6-8f2a-efdb2a9a7641"
	tests := []struct {
		name    string
		req     *types.KnowledgeFolderEnsurePathsRequest
		wantErr error
	}{
		{name: "nil request", wantErr: ErrKnowledgeFolderInvalidArgument},
		{
			name:    "paths empty",
			req:     knowledgeFolderEnsurePathsTestRequest(""),
			wantErr: ErrKnowledgeFolderInvalidArgument,
		},
		{
			name: "paths exceed limit",
			req: func() *types.KnowledgeFolderEnsurePathsRequest {
				paths := make([]types.KnowledgeFolderEnsurePathInput, knowledgeFolderEnsurePathsMaxPaths+1)
				for index := range paths {
					paths[index] = knowledgeFolderEnsurePathsTestInput(
						fmt.Sprintf("key-%03d", index),
						"Folder",
					)
				}
				return knowledgeFolderEnsurePathsTestRequest("", paths...)
			}(),
			wantErr: ErrKnowledgeFolderInvalidArgument,
		},
		{
			name: "total segments exceed limit",
			req: func() *types.KnowledgeFolderEnsurePathsRequest {
				segments := make([]string, types.KnowledgeFolderMaxDepth)
				for index := range segments {
					segments[index] = fmt.Sprintf("Segment %02d", index)
				}
				paths := make([]types.KnowledgeFolderEnsurePathInput, 63)
				for index := range paths {
					paths[index] = knowledgeFolderEnsurePathsTestInput(
						fmt.Sprintf("key-%02d", index),
						append([]string(nil), segments...)...,
					)
				}
				return knowledgeFolderEnsurePathsTestRequest("", paths...)
			}(),
			wantErr: ErrKnowledgeFolderInvalidArgument,
		},
		{
			name: "unique nodes exceed limit",
			req: func() *types.KnowledgeFolderEnsurePathsRequest {
				paths := make([]types.KnowledgeFolderEnsurePathInput, 34)
				for pathIndex := range paths {
					segments := make([]string, 30)
					for segmentIndex := range segments {
						segments[segmentIndex] = fmt.Sprintf(
							"Path %02d Segment %02d",
							pathIndex,
							segmentIndex,
						)
					}
					paths[pathIndex] = knowledgeFolderEnsurePathsTestInput(
						fmt.Sprintf("key-%02d", pathIndex),
						segments...,
					)
				}
				return knowledgeFolderEnsurePathsTestRequest("", paths...)
			}(),
			wantErr: ErrKnowledgeFolderInvalidArgument,
		},
		{
			name: "empty client key",
			req: knowledgeFolderEnsurePathsTestRequest(
				"",
				knowledgeFolderEnsurePathsTestInput("", "Folder"),
			),
			wantErr: ErrKnowledgeFolderInvalidArgument,
		},
		{
			name: "client key has surrounding whitespace",
			req: knowledgeFolderEnsurePathsTestRequest(
				"",
				knowledgeFolderEnsurePathsTestInput(" key ", "Folder"),
			),
			wantErr: ErrKnowledgeFolderInvalidArgument,
		},
		{
			name: "client key exceeds rune limit",
			req: knowledgeFolderEnsurePathsTestRequest(
				"",
				knowledgeFolderEnsurePathsTestInput(strings.Repeat("界", 513), "Folder"),
			),
			wantErr: ErrKnowledgeFolderInvalidArgument,
		},
		{
			name: "client key contains control",
			req: knowledgeFolderEnsurePathsTestRequest(
				"",
				knowledgeFolderEnsurePathsTestInput("key\nvalue", "Folder"),
			),
			wantErr: ErrKnowledgeFolderInvalidArgument,
		},
		{
			name: "client key contains nul",
			req: knowledgeFolderEnsurePathsTestRequest(
				"",
				knowledgeFolderEnsurePathsTestInput("key\x00value", "Folder"),
			),
			wantErr: ErrKnowledgeFolderInvalidArgument,
		},
		{
			name: "client key invalid utf8",
			req: knowledgeFolderEnsurePathsTestRequest(
				"",
				knowledgeFolderEnsurePathsTestInput(string([]byte{0xff}), "Folder"),
			),
			wantErr: ErrKnowledgeFolderInvalidArgument,
		},
		{
			name: "duplicate client key",
			req: knowledgeFolderEnsurePathsTestRequest(
				"",
				knowledgeFolderEnsurePathsTestInput("same", "A"),
				knowledgeFolderEnsurePathsTestInput("same", "B"),
			),
			wantErr: ErrKnowledgeFolderInvalidArgument,
		},
		{
			name: "segments empty",
			req: knowledgeFolderEnsurePathsTestRequest(
				"",
				knowledgeFolderEnsurePathsTestInput("key"),
			),
			wantErr: ErrKnowledgeFolderInvalidArgument,
		},
		{
			name: "path segment count exceeds limit",
			req: func() *types.KnowledgeFolderEnsurePathsRequest {
				segments := make([]string, types.KnowledgeFolderMaxDepth+1)
				for index := range segments {
					segments[index] = fmt.Sprintf("Segment %02d", index)
				}
				return knowledgeFolderEnsurePathsTestRequest(
					"",
					knowledgeFolderEnsurePathsTestInput("key", segments...),
				)
			}(),
			wantErr: ErrKnowledgeFolderInvalidArgument,
		},
		{
			name: "invalid segment name",
			req: knowledgeFolderEnsurePathsTestRequest(
				"",
				knowledgeFolderEnsurePathsTestInput("key", "bad/name"),
			),
			wantErr: ErrKnowledgeFolderInvalidName,
		},
		{
			name: "parent id malformed",
			req: knowledgeFolderEnsurePathsTestRequest(
				"not-a-uuid",
				knowledgeFolderEnsurePathsTestInput("key", "Folder"),
			),
			wantErr: ErrKnowledgeFolderInvalidArgument,
		},
		{
			name: "parent id uppercase",
			req: knowledgeFolderEnsurePathsTestRequest(
				strings.ToUpper(canonicalParentID),
				knowledgeFolderEnsurePathsTestInput("key", "Folder"),
			),
			wantErr: ErrKnowledgeFolderInvalidArgument,
		},
		{
			name: "parent id surrounding whitespace",
			req: knowledgeFolderEnsurePathsTestRequest(
				" "+canonicalParentID+" ",
				knowledgeFolderEnsurePathsTestInput("key", "Folder"),
			),
			wantErr: ErrKnowledgeFolderInvalidArgument,
		},
		{
			name: "whitespace is not virtual root",
			req: knowledgeFolderEnsurePathsTestRequest(
				" ",
				knowledgeFolderEnsurePathsTestInput("key", "Folder"),
			),
			wantErr: ErrKnowledgeFolderInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newKnowledgeFolderEnsurePathsTestRepository()
			service := &knowledgeFolderService{repo: repo}
			before := cloneKnowledgeFolderEnsurePathsTestRequest(tt.req)

			result, err := service.EnsurePaths(
				knowledgeFolderEnsurePathsTestContext(),
				"kb-1",
				tt.req,
			)

			require.ErrorIs(t, err, tt.wantErr)
			assert.Nil(t, result)
			assert.Zero(t, repo.transactionCalls)
			assert.Equal(t, before, tt.req)
		})
	}
}

func TestKnowledgeFolderServiceEnsurePathsRequiresEffectiveScope(t *testing.T) {
	req := knowledgeFolderEnsurePathsTestRequest(
		"",
		knowledgeFolderEnsurePathsTestInput("key", "Folder"),
	)
	tests := []struct {
		name string
		ctx  context.Context
		kbID string
	}{
		{name: "missing tenant", ctx: context.Background(), kbID: "kb-1"},
		{name: "zero tenant", ctx: context.WithValue(context.Background(), types.TenantIDContextKey, uint64(0)), kbID: "kb-1"},
		{name: "empty knowledge base", ctx: knowledgeFolderEnsurePathsTestContext(), kbID: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newKnowledgeFolderEnsurePathsTestRepository()
			result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(tt.ctx, tt.kbID, req)
			require.ErrorIs(t, err, ErrKnowledgeFolderInvalidArgument)
			assert.Nil(t, result)
			assert.Zero(t, repo.transactionCalls)
		})
	}
}

func TestKnowledgeFolderServiceEnsurePathsCreatesFromVirtualRoot(t *testing.T) {
	repo := newKnowledgeFolderEnsurePathsTestRepository()
	req := knowledgeFolderEnsurePathsTestRequest(
		"",
		knowledgeFolderEnsurePathsTestInput("src/internal", " src ", "internal"),
	)

	result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
		knowledgeFolderEnsurePathsTestContext(),
		"kb-1",
		req,
	)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "src/internal", result[0].ClientKey)
	parsedID, parseErr := uuid.Parse(result[0].FolderID)
	require.NoError(t, parseErr)
	assert.Equal(t, result[0].FolderID, parsedID.String())
	assert.Equal(t, 1, repo.transactionCalls)
	assert.Equal(t, uint64(1), repo.transactionTenant)
	assert.Equal(t, "kb-1", repo.transactionKB)
	require.Len(t, repo.createIfAbsentCallsByAttempt, 1)
	require.Len(t, repo.createIfAbsentCallsByAttempt[0], 2)
	assert.Equal(t, repo.createIfAbsentCallsByAttempt[0][1].ID, result[0].FolderID)

	src := knowledgeFolderEnsurePathsTestFindByName(repo, "src")
	require.NotNil(t, src)
	assert.Equal(t, types.KnowledgeFolderRootID, src.ParentID)
	assert.Equal(t, "/"+src.ID+"/", src.Path)
	assert.Equal(t, 1, src.Depth)
	internal := repo.folders[result[0].FolderID]
	require.NotNil(t, internal)
	assert.Equal(t, src.ID, internal.ParentID)
	assert.Equal(t, src.Path+internal.ID+"/", internal.Path)
	assert.Equal(t, 2, internal.Depth)
	assert.Zero(t, internal.SortOrder)
	assert.Empty(t, repo.pointCalls)
}

func TestKnowledgeFolderServiceEnsurePathsValidatesNonRootParentChain(t *testing.T) {
	chain := knowledgeFolderEnsurePathsTestChain(2)
	parent := chain[len(chain)-1]
	repo := newKnowledgeFolderEnsurePathsTestRepository(chain...)

	result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
		knowledgeFolderEnsurePathsTestContext(),
		"kb-1",
		knowledgeFolderEnsurePathsTestRequest(
			parent.ID,
			knowledgeFolderEnsurePathsTestInput("child/leaf", "Child", "Leaf"),
		),
	)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, 1, repo.getByIDCalls)
	assert.Equal(t, 1, repo.listByIDsCalls)
	child := knowledgeFolderEnsurePathsTestFindByName(repo, "Child")
	require.NotNil(t, child)
	assert.Equal(t, parent.ID, child.ParentID)
	assert.Equal(t, parent.Path+child.ID+"/", child.Path)
	assert.Equal(t, 3, child.Depth)
	leaf := repo.folders[result[0].FolderID]
	require.NotNil(t, leaf)
	assert.Equal(t, child.ID, leaf.ParentID)
	assert.Equal(t, 4, leaf.Depth)
}

func TestKnowledgeFolderServiceEnsurePathsRejectsMissingOrCorruptParentChain(t *testing.T) {
	ctx := knowledgeFolderEnsurePathsTestContext()
	reqFor := func(parentID string) *types.KnowledgeFolderEnsurePathsRequest {
		return knowledgeFolderEnsurePathsTestRequest(
			parentID,
			knowledgeFolderEnsurePathsTestInput("key", "Child"),
		)
	}

	t.Run("parent not found", func(t *testing.T) {
		repo := newKnowledgeFolderEnsurePathsTestRepository()
		result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
			ctx,
			"kb-1",
			reqFor(knowledgeFolderServiceTestID("missing-parent")),
		)
		require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
		assert.Nil(t, result)
		assert.Empty(t, repo.folders)
	})

	t.Run("ancestor missing", func(t *testing.T) {
		missingID := knowledgeFolderServiceTestID("missing-ancestor")
		parentID := knowledgeFolderServiceTestID("orphan-parent")
		parent := &types.KnowledgeFolder{
			ID:              parentID,
			TenantID:        1,
			KnowledgeBaseID: "kb-1",
			ParentID:        missingID,
			Name:            "Orphan",
			Path:            "/" + missingID + "/" + parentID + "/",
			Depth:           2,
		}
		repo := newKnowledgeFolderEnsurePathsTestRepository(parent)
		result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(ctx, "kb-1", reqFor(parent.ID))
		require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
		assert.Nil(t, result)
		assert.Len(t, repo.folders, 1)
		assert.Empty(t, repo.createIfAbsentCallsByAttempt[0])
	})

	t.Run("soft deleted ancestor explicitly rejected", func(t *testing.T) {
		chain := knowledgeFolderEnsurePathsTestChain(2)
		chain[0].DeletedAt = gorm.DeletedAt{Time: time.Now().UTC(), Valid: true}
		repo := newKnowledgeFolderEnsurePathsTestRepository(chain...)
		repo.listByIDsOverride = func(
			tx *knowledgeFolderEnsurePathsTestTreeRepository,
			_ []string,
		) ([]*types.KnowledgeFolder, error) {
			return []*types.KnowledgeFolder{tx.state[chain[1].ID], tx.state[chain[0].ID]}, nil
		}
		result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
			ctx,
			"kb-1",
			reqFor(chain[1].ID),
		)
		require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
		assert.Nil(t, result)
		assert.Empty(t, repo.createIfAbsentCallsByAttempt[0])
	})
}

func TestKnowledgeFolderServiceEnsurePathsEnforcesFinalDepth(t *testing.T) {
	t.Run("final depth 32 succeeds", func(t *testing.T) {
		chain := knowledgeFolderEnsurePathsTestChain(types.KnowledgeFolderMaxDepth - 1)
		parent := chain[len(chain)-1]
		repo := newKnowledgeFolderEnsurePathsTestRepository(chain...)
		result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
			knowledgeFolderEnsurePathsTestContext(),
			"kb-1",
			knowledgeFolderEnsurePathsTestRequest(
				parent.ID,
				knowledgeFolderEnsurePathsTestInput("terminal", "Terminal"),
			),
		)
		require.NoError(t, err)
		require.Len(t, result, 1)
		terminal := repo.folders[result[0].FolderID]
		require.NotNil(t, terminal)
		assert.Equal(t, types.KnowledgeFolderMaxDepth, terminal.Depth)
	})

	t.Run("final depth 33 fails before child queries or writes", func(t *testing.T) {
		parent := knowledgeFolderEnsurePathsTestChain(1)[0]
		repo := newKnowledgeFolderEnsurePathsTestRepository(parent)
		segments := make([]string, types.KnowledgeFolderMaxDepth)
		for index := range segments {
			segments[index] = fmt.Sprintf("Level %02d", index+1)
		}
		result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
			knowledgeFolderEnsurePathsTestContext(),
			"kb-1",
			knowledgeFolderEnsurePathsTestRequest(
				parent.ID,
				knowledgeFolderEnsurePathsTestInput("too-deep", segments...),
			),
		)
		require.ErrorIs(t, err, ErrKnowledgeFolderDepthExceeded)
		assert.Nil(t, result)
		assert.Empty(t, repo.batchCalls)
		assert.Empty(t, repo.createIfAbsentCallsByAttempt[0])
		assert.Len(t, repo.folders, 1)
	})
}

func TestKnowledgeFolderServiceEnsurePathsSharesPrefixesAndPreservesRequestOrder(t *testing.T) {
	repo := newKnowledgeFolderEnsurePathsTestRepository()
	req := knowledgeFolderEnsurePathsTestRequest(
		"",
		knowledgeFolderEnsurePathsTestInput("first", " A ", "B", "C"),
		knowledgeFolderEnsurePathsTestInput("second", "A", "B", "D"),
		knowledgeFolderEnsurePathsTestInput("third", "A ", "B", "C"),
	)

	result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
		knowledgeFolderEnsurePathsTestContext(),
		"kb-1",
		req,
	)

	require.NoError(t, err)
	require.Len(t, result, 3)
	assert.Equal(t, []string{"first", "second", "third"}, []string{
		result[0].ClientKey,
		result[1].ClientKey,
		result[2].ClientKey,
	})
	assert.Equal(t, result[0].FolderID, result[2].FolderID)
	assert.NotEqual(t, result[0].FolderID, result[1].FolderID)
	require.Len(t, repo.createIfAbsentCallsByAttempt, 1)
	assert.Len(t, repo.createIfAbsentCallsByAttempt[0], 4)
	require.Len(t, repo.batchCalls, 3)
	assert.Equal(t, []string{"A"}, repo.batchCalls[0].names)
	assert.Equal(t, []string{"B"}, repo.batchCalls[1].names)
	assert.Equal(t, []string{"C", "D"}, repo.batchCalls[2].names)
	assert.Empty(t, repo.pointCalls, "normal path must not issue per-name reads")
}

func TestKnowledgeFolderServiceEnsurePathsReusesExistingPrefixAndTerminal(t *testing.T) {
	t.Run("existing prefix", func(t *testing.T) {
		prefix := knowledgeFolderEnsurePathsTestChild("existing-prefix", "A", nil)
		repo := newKnowledgeFolderEnsurePathsTestRepository(prefix)
		result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
			knowledgeFolderEnsurePathsTestContext(),
			"kb-1",
			knowledgeFolderEnsurePathsTestRequest(
				"",
				knowledgeFolderEnsurePathsTestInput("path", "A", "B"),
			),
		)
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Len(t, repo.createIfAbsentCallsByAttempt[0], 1)
		assert.Equal(t, "B", repo.createIfAbsentCallsByAttempt[0][0].Name)
		terminal := repo.folders[result[0].FolderID]
		require.NotNil(t, terminal)
		assert.Equal(t, prefix.ID, terminal.ParentID)
	})

	t.Run("existing terminal performs no writes", func(t *testing.T) {
		prefix := knowledgeFolderEnsurePathsTestChild("existing-prefix", "A", nil)
		terminal := knowledgeFolderEnsurePathsTestChild("existing-terminal", "B", prefix)
		repo := newKnowledgeFolderEnsurePathsTestRepository(prefix, terminal)
		result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
			knowledgeFolderEnsurePathsTestContext(),
			"kb-1",
			knowledgeFolderEnsurePathsTestRequest(
				"",
				knowledgeFolderEnsurePathsTestInput("path", "A", "B"),
			),
		)
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, terminal.ID, result[0].FolderID)
		assert.Empty(t, repo.createIfAbsentCallsByAttempt[0])
		assert.Empty(t, repo.pointCalls)
		assert.Len(t, repo.folders, 2)
	})
}

func TestKnowledgeFolderServiceEnsurePathsDoesNotModifyRequest(t *testing.T) {
	repo := newKnowledgeFolderEnsurePathsTestRepository()
	req := knowledgeFolderEnsurePathsTestRequest(
		"",
		knowledgeFolderEnsurePathsTestInput("key-a", " A ", "B"),
		knowledgeFolderEnsurePathsTestInput("key-b", " A ", "C"),
	)
	before := cloneKnowledgeFolderEnsurePathsTestRequest(req)

	result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
		knowledgeFolderEnsurePathsTestContext(),
		"kb-1",
		req,
	)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, before, req)
}

func TestKnowledgeFolderServiceEnsurePathsRejectsCorruptExistingChildren(t *testing.T) {
	tests := []struct {
		name string
		rows func() []*types.KnowledgeFolder
	}{
		{
			name: "nil row",
			rows: func() []*types.KnowledgeFolder {
				return []*types.KnowledgeFolder{nil}
			},
		},
		{
			name: "wrong path",
			rows: func() []*types.KnowledgeFolder {
				folder := knowledgeFolderEnsurePathsTestChild("wrong-path", "A", nil)
				folder.Path = "/" + knowledgeFolderServiceTestID("other") + "/"
				return []*types.KnowledgeFolder{folder}
			},
		},
		{
			name: "wrong depth",
			rows: func() []*types.KnowledgeFolder {
				folder := knowledgeFolderEnsurePathsTestChild("wrong-depth", "A", nil)
				folder.Depth = 2
				return []*types.KnowledgeFolder{folder}
			},
		},
		{
			name: "wrong parent",
			rows: func() []*types.KnowledgeFolder {
				parent := knowledgeFolderEnsurePathsTestChild("other-parent", "Other", nil)
				return []*types.KnowledgeFolder{
					knowledgeFolderEnsurePathsTestChild("wrong-parent", "A", parent),
				}
			},
		},
		{
			name: "wrong tenant",
			rows: func() []*types.KnowledgeFolder {
				folder := knowledgeFolderEnsurePathsTestChild("wrong-tenant", "A", nil)
				folder.TenantID = 2
				return []*types.KnowledgeFolder{folder}
			},
		},
		{
			name: "wrong knowledge base",
			rows: func() []*types.KnowledgeFolder {
				folder := knowledgeFolderEnsurePathsTestChild("wrong-kb", "A", nil)
				folder.KnowledgeBaseID = "kb-2"
				return []*types.KnowledgeFolder{folder}
			},
		},
		{
			name: "soft deleted",
			rows: func() []*types.KnowledgeFolder {
				folder := knowledgeFolderEnsurePathsTestChild("deleted", "A", nil)
				folder.DeletedAt = gorm.DeletedAt{Time: time.Now().UTC(), Valid: true}
				return []*types.KnowledgeFolder{folder}
			},
		},
		{
			name: "name not requested",
			rows: func() []*types.KnowledgeFolder {
				return []*types.KnowledgeFolder{
					knowledgeFolderEnsurePathsTestChild("unexpected-name", "B", nil),
				}
			},
		},
		{
			name: "duplicate active name",
			rows: func() []*types.KnowledgeFolder {
				return []*types.KnowledgeFolder{
					knowledgeFolderEnsurePathsTestChild("duplicate-a", "A", nil),
					knowledgeFolderEnsurePathsTestChild("duplicate-b", "A", nil),
				}
			},
		},
		{
			name: "duplicate id",
			rows: func() []*types.KnowledgeFolder {
				folder := knowledgeFolderEnsurePathsTestChild("duplicate-id", "A", nil)
				return []*types.KnowledgeFolder{folder, folder}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newKnowledgeFolderEnsurePathsTestRepository()
			repo.listOverride = func(
				_ *knowledgeFolderEnsurePathsTestTreeRepository,
				_ string,
				_ []string,
			) ([]*types.KnowledgeFolder, error) {
				return tt.rows(), nil
			}
			result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
				knowledgeFolderEnsurePathsTestContext(),
				"kb-1",
				knowledgeFolderEnsurePathsTestRequest(
					"",
					knowledgeFolderEnsurePathsTestInput("key", "A"),
				),
			)
			require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
			assert.Nil(t, result)
			assert.Empty(t, repo.folders)
			assert.Empty(t, repo.createIfAbsentCallsByAttempt[0])
		})
	}
}

func TestKnowledgeFolderServiceEnsurePathsRereadsCreateIfAbsentConflictWithFullScope(t *testing.T) {
	repo := newKnowledgeFolderEnsurePathsTestRepository()
	raceFolder := knowledgeFolderEnsurePathsTestChild("race-winner", "A", nil)
	repo.createIfAbsentOverride = func(
		tx *knowledgeFolderEnsurePathsTestTreeRepository,
		folder *types.KnowledgeFolder,
	) (bool, error) {
		if folder.Name != "A" {
			return tx.createIfAbsentDefault(folder)
		}
		copyOfRaceFolder := *raceFolder
		tx.state[raceFolder.ID] = &copyOfRaceFolder
		return false, nil
	}

	result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
		knowledgeFolderEnsurePathsTestContext(),
		"kb-1",
		knowledgeFolderEnsurePathsTestRequest(
			"",
			knowledgeFolderEnsurePathsTestInput("key", "A", "B"),
		),
	)

	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Len(t, repo.createIfAbsentCallsByAttempt, 1)
	require.Len(t, repo.createIfAbsentCallsByAttempt[0], 2)
	assert.NotEqual(t, raceFolder.ID, repo.createIfAbsentCallsByAttempt[0][0].ID)
	require.Len(t, repo.pointCalls, 1)
	assert.Equal(t, uint64(1), repo.pointCalls[0].tenantID)
	assert.Equal(t, "kb-1", repo.pointCalls[0].kbID)
	assert.Equal(t, types.KnowledgeFolderRootID, repo.pointCalls[0].parentID)
	assert.Equal(t, "A", repo.pointCalls[0].name)
	require.Len(t, repo.folders, 2)
	assert.NotNil(t, repo.folders[raceFolder.ID])
	terminal := repo.folders[result[0].FolderID]
	require.NotNil(t, terminal)
	assert.Equal(t, "B", terminal.Name)
	assert.Equal(t, raceFolder.ID, terminal.ParentID)
}

func TestKnowledgeFolderServiceEnsurePathsFailsClosedWhenConflictRereadIsInvalid(t *testing.T) {
	tests := []struct {
		name          string
		pointOverride func(*knowledgeFolderEnsurePathsTestTreeRepository, string, string) (*types.KnowledgeFolder, error)
	}{
		{
			name: "not found",
			pointOverride: func(
				_ *knowledgeFolderEnsurePathsTestTreeRepository,
				_ string,
				_ string,
			) (*types.KnowledgeFolder, error) {
				return nil, repository.ErrKnowledgeFolderNotFound
			},
		},
		{
			name: "nil without error",
			pointOverride: func(
				_ *knowledgeFolderEnsurePathsTestTreeRepository,
				_ string,
				_ string,
			) (*types.KnowledgeFolder, error) {
				return nil, nil
			},
		},
		{
			name: "wrong path",
			pointOverride: func(
				_ *knowledgeFolderEnsurePathsTestTreeRepository,
				_ string,
				_ string,
			) (*types.KnowledgeFolder, error) {
				folder := knowledgeFolderEnsurePathsTestChild("bad-reread", "A", nil)
				folder.Path = "/" + knowledgeFolderServiceTestID("other") + "/"
				return folder, nil
			},
		},
		{
			name: "soft deleted",
			pointOverride: func(
				_ *knowledgeFolderEnsurePathsTestTreeRepository,
				_ string,
				_ string,
			) (*types.KnowledgeFolder, error) {
				folder := knowledgeFolderEnsurePathsTestChild("deleted-reread", "A", nil)
				folder.DeletedAt = gorm.DeletedAt{Time: time.Now().UTC(), Valid: true}
				return folder, nil
			},
		},
		{
			name: "wrong tenant",
			pointOverride: func(
				_ *knowledgeFolderEnsurePathsTestTreeRepository,
				_ string,
				_ string,
			) (*types.KnowledgeFolder, error) {
				folder := knowledgeFolderEnsurePathsTestChild("wrong-tenant-reread", "A", nil)
				folder.TenantID = 2
				return folder, nil
			},
		},
		{
			name: "wrong knowledge base",
			pointOverride: func(
				_ *knowledgeFolderEnsurePathsTestTreeRepository,
				_ string,
				_ string,
			) (*types.KnowledgeFolder, error) {
				folder := knowledgeFolderEnsurePathsTestChild("wrong-kb-reread", "A", nil)
				folder.KnowledgeBaseID = "kb-2"
				return folder, nil
			},
		},
		{
			name: "wrong parent",
			pointOverride: func(
				_ *knowledgeFolderEnsurePathsTestTreeRepository,
				_ string,
				_ string,
			) (*types.KnowledgeFolder, error) {
				otherParent := knowledgeFolderEnsurePathsTestChild("reread-other-parent", "Other", nil)
				return knowledgeFolderEnsurePathsTestChild("wrong-parent-reread", "A", otherParent), nil
			},
		},
		{
			name: "wrong depth",
			pointOverride: func(
				_ *knowledgeFolderEnsurePathsTestTreeRepository,
				_ string,
				_ string,
			) (*types.KnowledgeFolder, error) {
				folder := knowledgeFolderEnsurePathsTestChild("wrong-depth-reread", "A", nil)
				folder.Depth = 2
				return folder, nil
			},
		},
		{
			name: "wrong name",
			pointOverride: func(
				_ *knowledgeFolderEnsurePathsTestTreeRepository,
				_ string,
				_ string,
			) (*types.KnowledgeFolder, error) {
				return knowledgeFolderEnsurePathsTestChild("wrong-name-reread", "B", nil), nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newKnowledgeFolderEnsurePathsTestRepository()
			repo.createIfAbsentOverride = func(
				_ *knowledgeFolderEnsurePathsTestTreeRepository,
				_ *types.KnowledgeFolder,
			) (bool, error) {
				return false, nil
			}
			repo.pointOverride = tt.pointOverride
			result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
				knowledgeFolderEnsurePathsTestContext(),
				"kb-1",
				knowledgeFolderEnsurePathsTestRequest(
					"",
					knowledgeFolderEnsurePathsTestInput("key", "A"),
				),
			)
			require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
			assert.Nil(t, result)
			assert.Empty(t, repo.folders)
			require.Len(t, repo.pointCalls, 1)
		})
	}
}

func TestKnowledgeFolderServiceEnsurePathsMapsCreateIfAbsentErrors(t *testing.T) {
	req := knowledgeFolderEnsurePathsTestRequest(
		"",
		knowledgeFolderEnsurePathsTestInput("key", "A"),
	)

	t.Run("repository data integrity", func(t *testing.T) {
		repo := newKnowledgeFolderEnsurePathsTestRepository()
		repo.createIfAbsentOverride = func(
			_ *knowledgeFolderEnsurePathsTestTreeRepository,
			_ *types.KnowledgeFolder,
		) (bool, error) {
			return false, repository.ErrKnowledgeFolderDataIntegrity
		}

		result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
			knowledgeFolderEnsurePathsTestContext(),
			"kb-1",
			req,
		)

		require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
		require.ErrorIs(t, err, repository.ErrKnowledgeFolderDataIntegrity)
		assert.Nil(t, result)
		require.Len(t, repo.createIfAbsentCallsByAttempt, 1)
		require.Len(t, repo.createIfAbsentCallsByAttempt[0], 1)
		assert.Empty(t, repo.pointCalls)
		assert.Empty(t, repo.folders)
	})

	t.Run("database error", func(t *testing.T) {
		databaseErr := errors.New("create-if-absent database unavailable")
		repo := newKnowledgeFolderEnsurePathsTestRepository()
		repo.createIfAbsentOverride = func(
			_ *knowledgeFolderEnsurePathsTestTreeRepository,
			_ *types.KnowledgeFolder,
		) (bool, error) {
			return false, databaseErr
		}

		result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
			knowledgeFolderEnsurePathsTestContext(),
			"kb-1",
			req,
		)

		require.ErrorIs(t, err, databaseErr)
		require.ErrorIs(t, err, ErrKnowledgeFolderInternal)
		assert.Nil(t, result)
		require.Len(t, repo.createIfAbsentCallsByAttempt, 1)
		require.Len(t, repo.createIfAbsentCallsByAttempt[0], 1)
		assert.Empty(t, repo.pointCalls)
		assert.Empty(t, repo.folders)
	})
}

func TestKnowledgeFolderServiceEnsurePathsFailsClosedOnCandidatePrimaryKeyConflict(t *testing.T) {
	repo := newKnowledgeFolderEnsurePathsTestRepository()
	repo.createIfAbsentOverride = func(
		tx *knowledgeFolderEnsurePathsTestTreeRepository,
		folder *types.KnowledgeFolder,
	) (bool, error) {
		occupied := *folder
		occupied.Name = "Occupied by another folder"
		tx.state[occupied.ID] = &occupied
		return false, nil
	}

	result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
		knowledgeFolderEnsurePathsTestContext(),
		"kb-1",
		knowledgeFolderEnsurePathsTestRequest(
			"",
			knowledgeFolderEnsurePathsTestInput("key", "A"),
		),
	)

	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
	assert.Nil(t, result)
	require.Len(t, repo.createIfAbsentCallsByAttempt, 1)
	require.Len(t, repo.createIfAbsentCallsByAttempt[0], 1)
	require.Len(t, repo.pointCalls, 1)
	assert.Equal(t, uint64(1), repo.pointCalls[0].tenantID)
	assert.Equal(t, "kb-1", repo.pointCalls[0].kbID)
	assert.Equal(t, types.KnowledgeFolderRootID, repo.pointCalls[0].parentID)
	assert.Equal(t, "A", repo.pointCalls[0].name)
	assert.Empty(t, repo.folders)
}

func TestKnowledgeFolderServiceEnsurePathsRollsBackAllEarlierCreates(t *testing.T) {
	downstreamErr := errors.New("later create failed")
	repo := newKnowledgeFolderEnsurePathsTestRepository()
	repo.createIfAbsentOverride = func(
		tx *knowledgeFolderEnsurePathsTestTreeRepository,
		folder *types.KnowledgeFolder,
	) (bool, error) {
		if folder.Name == "C" {
			return false, downstreamErr
		}
		return tx.createIfAbsentDefault(folder)
	}

	result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
		knowledgeFolderEnsurePathsTestContext(),
		"kb-1",
		knowledgeFolderEnsurePathsTestRequest(
			"",
			knowledgeFolderEnsurePathsTestInput("deep", "A", "B", "C"),
		),
	)

	require.ErrorIs(t, err, downstreamErr)
	require.ErrorIs(t, err, ErrKnowledgeFolderInternal)
	assert.Nil(t, result)
	assert.Empty(t, repo.folders)
	require.Len(t, repo.createIfAbsentCallsByAttempt, 1)
	assert.Len(t, repo.createIfAbsentCallsByAttempt[0], 3)
}

func TestKnowledgeFolderServiceEnsurePathsKeepsCandidateIDsStableAcrossReplay(t *testing.T) {
	repo := newKnowledgeFolderEnsurePathsTestRepository()
	repo.replayAttempts = 2

	result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
		knowledgeFolderEnsurePathsTestContext(),
		"kb-1",
		knowledgeFolderEnsurePathsTestRequest(
			"",
			knowledgeFolderEnsurePathsTestInput("path", "A", "B"),
		),
	)

	require.NoError(t, err)
	assert.Equal(t, 2, repo.callbackCalls)
	require.Len(t, repo.createIfAbsentCallsByAttempt, 2)
	require.Len(t, repo.createIfAbsentCallsByAttempt[0], 2)
	require.Len(t, repo.createIfAbsentCallsByAttempt[1], 2)
	for index := range repo.createIfAbsentCallsByAttempt[0] {
		assert.Equal(t, repo.createIfAbsentCallsByAttempt[0][index].ID, repo.createIfAbsentCallsByAttempt[1][index].ID)
		assert.Equal(t, repo.createIfAbsentCallsByAttempt[0][index].Path, repo.createIfAbsentCallsByAttempt[1][index].Path)
	}
	assert.Equal(t, repo.createIfAbsentCallsByAttempt[1][1].ID, result[0].FolderID)
	assert.Len(t, repo.folders, 2)
}

func TestKnowledgeFolderServiceEnsurePathsDiscardsFailedAttemptResolution(t *testing.T) {
	repo := newKnowledgeFolderEnsurePathsTestRepository()
	repo.replayAttempts = 2
	failedAttemptFolder := knowledgeFolderEnsurePathsTestChild("failed-attempt-a", "A", nil)
	repo.createIfAbsentOverride = func(
		tx *knowledgeFolderEnsurePathsTestTreeRepository,
		folder *types.KnowledgeFolder,
	) (bool, error) {
		if tx.attempt != 0 {
			return tx.createIfAbsentDefault(folder)
		}
		switch folder.Name {
		case "A":
			copyOfFailedFolder := *failedAttemptFolder
			tx.state[failedAttemptFolder.ID] = &copyOfFailedFolder
			return false, nil
		case "B":
			return false, sqlite3.Error{Code: sqlite3.ErrBusy}
		default:
			return tx.createIfAbsentDefault(folder)
		}
	}

	result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
		knowledgeFolderEnsurePathsTestContext(),
		"kb-1",
		knowledgeFolderEnsurePathsTestRequest(
			"",
			knowledgeFolderEnsurePathsTestInput("prefix", "A"),
			knowledgeFolderEnsurePathsTestInput("terminal", "A", "B"),
		),
	)

	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Len(t, repo.createIfAbsentCallsByAttempt, 2)
	require.Len(t, repo.createIfAbsentCallsByAttempt[1], 2)
	assert.Equal(t, repo.createIfAbsentCallsByAttempt[1][0].ID, result[0].FolderID)
	assert.Equal(t, repo.createIfAbsentCallsByAttempt[1][1].ID, result[1].FolderID)
	assert.NotEqual(t, failedAttemptFolder.ID, result[0].FolderID)
	assert.Nil(t, repo.folders[failedAttemptFolder.ID])
	assert.Len(t, repo.folders, 2)
}

func TestKnowledgeFolderServiceEnsurePathsFailsClosedWhenTerminalIsUnresolved(t *testing.T) {
	repo := newKnowledgeFolderEnsurePathsTestRepository()
	repo.skipCallback = true

	result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
		knowledgeFolderEnsurePathsTestContext(),
		"kb-1",
		knowledgeFolderEnsurePathsTestRequest(
			"",
			knowledgeFolderEnsurePathsTestInput("key", "A"),
		),
	)

	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
	assert.Nil(t, result)
	assert.Zero(t, repo.callbackCalls)
	assert.Empty(t, repo.folders)
}

func TestKnowledgeFolderServiceEnsurePathsPropagatesContextAndDatabaseErrors(t *testing.T) {
	req := knowledgeFolderEnsurePathsTestRequest(
		"",
		knowledgeFolderEnsurePathsTestInput("key", "A"),
	)

	t.Run("context canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(knowledgeFolderEnsurePathsTestContext())
		cancel()
		repo := newKnowledgeFolderEnsurePathsTestRepository()
		result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(ctx, "kb-1", req)
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, ErrKnowledgeFolderInternal)
		assert.Nil(t, result)
		assert.Zero(t, repo.callbackCalls)
	})

	t.Run("database error", func(t *testing.T) {
		databaseErr := errors.New("database unavailable")
		repo := newKnowledgeFolderEnsurePathsTestRepository()
		repo.transactionErr = databaseErr
		result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
			knowledgeFolderEnsurePathsTestContext(),
			"kb-1",
			req,
		)
		require.ErrorIs(t, err, databaseErr)
		require.ErrorIs(t, err, ErrKnowledgeFolderInternal)
		assert.Nil(t, result)
		assert.Zero(t, repo.callbackCalls)
	})

	t.Run("batch query error", func(t *testing.T) {
		queryErr := errors.New("batch query failed")
		repo := newKnowledgeFolderEnsurePathsTestRepository()
		repo.listOverride = func(
			_ *knowledgeFolderEnsurePathsTestTreeRepository,
			_ string,
			_ []string,
		) ([]*types.KnowledgeFolder, error) {
			return nil, queryErr
		}
		result, err := (&knowledgeFolderService{repo: repo}).EnsurePaths(
			knowledgeFolderEnsurePathsTestContext(),
			"kb-1",
			req,
		)
		require.ErrorIs(t, err, queryErr)
		require.ErrorIs(t, err, ErrKnowledgeFolderInternal)
		assert.Nil(t, result)
		assert.Empty(t, repo.folders)
		assert.Empty(t, repo.createIfAbsentCallsByAttempt[0])
	})
}
