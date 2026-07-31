package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	knowledgeScopeTestTenant  = uint64(7)
	knowledgeScopeTestKB      = "kb-1"
	knowledgeScopeTestOtherKB = "kb-2"
	knowledgeScopeTestFolderA = "00000000-0000-0000-0000-000000000001"
	knowledgeScopeTestFolderB = "00000000-0000-0000-0000-000000000002"
	knowledgeScopeTestFolderC = "00000000-0000-0000-0000-000000000003"
	knowledgeScopeTestFolderD = "00000000-0000-0000-0000-000000000004"
)

type knowledgeScopeReadCall struct {
	folderIDs []string
	roots     []interfaces.KnowledgeFolderScopeRoot
	limit     int
}

type knowledgeScopeReaderFake struct {
	folders      map[string]*types.KnowledgeFolder
	subtree      []*types.KnowledgeFolder
	listErr      error
	subtreeErr   error
	listCalls    []knowledgeScopeReadCall
	subtreeCalls []knowledgeScopeReadCall
	listFunc     func([]string) ([]*types.KnowledgeFolder, error)
	subtreeFunc  func(
		[]interfaces.KnowledgeFolderScopeRoot,
		int,
	) ([]*types.KnowledgeFolder, error)
}

func (f *knowledgeScopeReaderFake) ListScopeFoldersByIDs(
	folderIDs []string,
) ([]*types.KnowledgeFolder, error) {
	f.listCalls = append(f.listCalls, knowledgeScopeReadCall{
		folderIDs: append([]string(nil), folderIDs...),
	})
	if f.listFunc != nil {
		return f.listFunc(folderIDs)
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	result := make([]*types.KnowledgeFolder, 0, len(folderIDs))
	for _, folderID := range folderIDs {
		if folder := f.folders[folderID]; folder != nil {
			result = append(result, folder)
		}
	}
	return result, nil
}

func (f *knowledgeScopeReaderFake) ListScopeSubtreeCandidates(
	roots []interfaces.KnowledgeFolderScopeRoot,
	limit int,
) ([]*types.KnowledgeFolder, error) {
	f.subtreeCalls = append(f.subtreeCalls, knowledgeScopeReadCall{
		roots: append(
			[]interfaces.KnowledgeFolderScopeRoot(nil),
			roots...,
		),
		limit: limit,
	})
	if f.subtreeFunc != nil {
		return f.subtreeFunc(roots, limit)
	}
	if f.subtreeErr != nil {
		return nil, f.subtreeErr
	}
	return append([]*types.KnowledgeFolder(nil), f.subtree...), nil
}

type knowledgeScopeSnapshotCall struct {
	ctx             context.Context
	sourceTenantID  uint64
	knowledgeBaseID string
}

type knowledgeScopeRepositoryFake struct {
	reader         interfaces.KnowledgeFolderScopeReader
	readers        map[string]interfaces.KnowledgeFolderScopeReader
	attemptReaders []interfaces.KnowledgeFolderScopeReader
	snapshotErr    error
	snapshotErrors map[string]error
	snapshotFunc   func(
		context.Context,
		uint64,
		string,
		interfaces.KnowledgeFolderScopeReadSnapshotFunc,
	) error
	snapshotCalls []knowledgeScopeSnapshotCall
	retryAttempts int
}

func (f *knowledgeScopeRepositoryFake) RunKnowledgeFolderScopeReadSnapshot(
	ctx context.Context,
	sourceTenantID uint64,
	knowledgeBaseID string,
	fn interfaces.KnowledgeFolderScopeReadSnapshotFunc,
) error {
	f.snapshotCalls = append(f.snapshotCalls, knowledgeScopeSnapshotCall{
		ctx:             ctx,
		sourceTenantID:  sourceTenantID,
		knowledgeBaseID: knowledgeBaseID,
	})
	if f.snapshotFunc != nil {
		return f.snapshotFunc(ctx, sourceTenantID, knowledgeBaseID, fn)
	}
	if f.snapshotErr != nil {
		return f.snapshotErr
	}
	if err := f.snapshotErrors[knowledgeBaseID]; err != nil {
		return err
	}
	if len(f.attemptReaders) > 0 {
		for _, reader := range f.attemptReaders {
			f.retryAttempts++
			if err := fn(reader); err != nil {
				return err
			}
		}
		return nil
	}
	f.retryAttempts++
	reader := f.reader
	if scopedReader := f.readers[knowledgeBaseID]; scopedReader != nil {
		reader = scopedReader
	}
	return fn(reader)
}

func newKnowledgeScopeTestResolver(
	t *testing.T,
	repository interfaces.KnowledgeFolderScopeRepository,
	limits KnowledgeScopeLimits,
) interfaces.KnowledgeScopeResolver {
	t.Helper()
	resolver, err := NewKnowledgeScopeResolver(repository, limits)
	require.NoError(t, err)
	return resolver
}

func defaultKnowledgeScopeTestResolver(
	t *testing.T,
	repository interfaces.KnowledgeFolderScopeRepository,
) interfaces.KnowledgeScopeResolver {
	t.Helper()
	return newKnowledgeScopeTestResolver(t, repository, KnowledgeScopeLimits{
		MaxSelectors:         100,
		MaxResolvedFolderIDs: 10000,
	})
}

func knowledgeScopeTestBool(value bool) *bool {
	return &value
}

func knowledgeScopeTestAuthorizedTarget() types.AuthorizedKnowledgeScopeTarget {
	return knowledgeScopeTestAuthorizedTargetFor(
		knowledgeScopeTestTenant,
		knowledgeScopeTestKB,
	)
}

func knowledgeScopeTestAuthorizedTargetFor(
	sourceTenantID uint64,
	knowledgeBaseID string,
) types.AuthorizedKnowledgeScopeTarget {
	return types.AuthorizedKnowledgeScopeTarget{
		KnowledgeBaseID: knowledgeBaseID,
		SourceTenantID:  sourceTenantID,
	}
}

func knowledgeScopeTestInput(
	folderScopes *[]types.FolderScopeRequest,
) types.KnowledgeScopeResolveInput {
	return types.KnowledgeScopeResolveInput{
		Request: &types.KnowledgeScopeRequest{FolderScopes: folderScopes},
		AuthorizedTargets: []types.AuthorizedKnowledgeScopeTarget{
			knowledgeScopeTestAuthorizedTarget(),
		},
	}
}

func knowledgeScopeTestFolder(
	id string,
	parentID string,
	path string,
	depth int,
) *types.KnowledgeFolder {
	return &types.KnowledgeFolder{
		ID:              id,
		TenantID:        knowledgeScopeTestTenant,
		KnowledgeBaseID: knowledgeScopeTestKB,
		ParentID:        parentID,
		Name:            "folder",
		Path:            path,
		Depth:           depth,
	}
}

func knowledgeScopeTestTree() map[string]*types.KnowledgeFolder {
	return map[string]*types.KnowledgeFolder{
		knowledgeScopeTestFolderA: knowledgeScopeTestFolder(
			knowledgeScopeTestFolderA,
			types.KnowledgeFolderRootID,
			"/"+knowledgeScopeTestFolderA+"/",
			1,
		),
		knowledgeScopeTestFolderB: knowledgeScopeTestFolder(
			knowledgeScopeTestFolderB,
			knowledgeScopeTestFolderA,
			"/"+knowledgeScopeTestFolderA+"/"+knowledgeScopeTestFolderB+"/",
			2,
		),
		knowledgeScopeTestFolderC: knowledgeScopeTestFolder(
			knowledgeScopeTestFolderC,
			knowledgeScopeTestFolderB,
			"/"+knowledgeScopeTestFolderA+"/"+knowledgeScopeTestFolderB+
				"/"+knowledgeScopeTestFolderC+"/",
			3,
		),
		knowledgeScopeTestFolderD: knowledgeScopeTestFolder(
			knowledgeScopeTestFolderD,
			types.KnowledgeFolderRootID,
			"/"+knowledgeScopeTestFolderD+"/",
			1,
		),
	}
}

func knowledgeScopeTestTreeForTarget(
	sourceTenantID uint64,
	knowledgeBaseID string,
) map[string]*types.KnowledgeFolder {
	tree := knowledgeScopeTestTree()
	for _, folder := range tree {
		folder.TenantID = sourceTenantID
		folder.KnowledgeBaseID = knowledgeBaseID
	}
	return tree
}

func knowledgeScopeTestRepository(
	tree map[string]*types.KnowledgeFolder,
	subtree []*types.KnowledgeFolder,
) (*knowledgeScopeRepositoryFake, *knowledgeScopeReaderFake) {
	reader := &knowledgeScopeReaderFake{folders: tree, subtree: subtree}
	return &knowledgeScopeRepositoryFake{reader: reader}, reader
}

func knowledgeScopeTestReaderForTarget(
	sourceTenantID uint64,
	knowledgeBaseID string,
	subtreeFolderIDs ...string,
) *knowledgeScopeReaderFake {
	tree := knowledgeScopeTestTreeForTarget(sourceTenantID, knowledgeBaseID)
	subtree := make([]*types.KnowledgeFolder, 0, len(subtreeFolderIDs))
	for _, folderID := range subtreeFolderIDs {
		subtree = append(subtree, tree[folderID])
	}
	return &knowledgeScopeReaderFake{folders: tree, subtree: subtree}
}

func knowledgeScopeTestFilter(
	t *testing.T,
	scope *types.KnowledgeScope,
) types.ResolvedFolderFilter {
	t.Helper()
	targets := scope.Targets()
	require.Len(t, targets, 1)
	return targets[0].FolderFilter()
}

func knowledgeScopeTestFilterForKB(
	t *testing.T,
	scope *types.KnowledgeScope,
	knowledgeBaseID string,
) types.ResolvedFolderFilter {
	t.Helper()
	for _, target := range scope.Targets() {
		if target.KnowledgeBaseID() == knowledgeBaseID {
			return target.FolderFilter()
		}
	}
	require.FailNow(t, "knowledge scope target not found", knowledgeBaseID)
	return types.ResolvedFolderFilter{}
}

func knowledgeScopeTestRootIDs(
	roots []interfaces.KnowledgeFolderScopeRoot,
) []string {
	ids := make([]string, len(roots))
	for index, root := range roots {
		ids[index] = root.ID
	}
	return ids
}

func TestKnowledgeScopeResolverMissingFolderScopesDisablesFilter(t *testing.T) {
	repository, _ := knowledgeScopeTestRepository(nil, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	input := types.KnowledgeScopeResolveInput{
		Request: &types.KnowledgeScopeRequest{},
		AuthorizedTargets: []types.AuthorizedKnowledgeScopeTarget{
			knowledgeScopeTestAuthorizedTarget(),
		},
	}

	scope, err := resolver.Resolve(context.Background(), input)

	require.NoError(t, err)
	assert.False(t, knowledgeScopeTestFilter(t, scope).Enabled())
	assert.Empty(t, repository.snapshotCalls)
}

func TestKnowledgeScopeResolverExplicitEmptyFolderScopesEnablesEmptyFilter(
	t *testing.T,
) {
	repository, _ := knowledgeScopeTestRepository(nil, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	filter := knowledgeScopeTestFilter(t, scope)
	assert.True(t, filter.Enabled())
	assert.True(t, filter.Empty())
	assert.Empty(t, repository.snapshotCalls)
}

func TestKnowledgeScopeResolverMissingKnowledgeBaseEntryEnablesEmptyFilter(
	t *testing.T,
) {
	repository, _ := knowledgeScopeTestRepository(nil, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestOtherKB,
		FolderIDs:       []string{},
	}}
	input := knowledgeScopeTestInput(&folderScopes)
	input.AuthorizedTargets = append(
		input.AuthorizedTargets,
		knowledgeScopeTestAuthorizedTargetFor(
			knowledgeScopeTestTenant,
			knowledgeScopeTestOtherKB,
		),
	)

	scope, err := resolver.Resolve(
		context.Background(),
		input,
	)

	require.NoError(t, err)
	filter := knowledgeScopeTestFilterForKB(t, scope, knowledgeScopeTestKB)
	assert.True(t, filter.Enabled())
	assert.True(t, filter.Empty())
	assert.Empty(t, repository.snapshotCalls)
}

func TestKnowledgeScopeResolverExplicitEmptyKnowledgeBaseEntryEnablesEmptyFilter(
	t *testing.T,
) {
	repository, _ := knowledgeScopeTestRepository(nil, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	filter := knowledgeScopeTestFilter(t, scope)
	assert.True(t, filter.Enabled())
	assert.True(t, filter.Empty())
	assert.Empty(t, repository.snapshotCalls)
}

func TestKnowledgeScopeResolverRejectsFolderScopeWithoutAuthorizedTarget(
	t *testing.T,
) {
	repository, reader := knowledgeScopeTestRepository(
		knowledgeScopeTestTree(),
		nil,
	)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		types.KnowledgeScopeResolveInput{
			Request: &types.KnowledgeScopeRequest{
				FolderScopes: &folderScopes,
			},
		},
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)
	assert.Empty(t, repository.snapshotCalls)
	assert.Empty(t, reader.listCalls)
	assert.Empty(t, reader.subtreeCalls)
}

func TestKnowledgeScopeResolverRejectsAmbiguousAuthorizedTargetsForSameKnowledgeBase(
	t *testing.T,
) {
	repository, reader := knowledgeScopeTestRepository(
		knowledgeScopeTestTree(),
		nil,
	)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}
	input := knowledgeScopeTestInput(&folderScopes)
	input.AuthorizedTargets = append(
		input.AuthorizedTargets,
		knowledgeScopeTestAuthorizedTargetFor(
			knowledgeScopeTestTenant+1,
			knowledgeScopeTestKB,
		),
	)

	scope, err := resolver.Resolve(context.Background(), input)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)
	assert.Empty(t, repository.snapshotCalls)
	assert.Empty(t, reader.listCalls)
	assert.Empty(t, reader.subtreeCalls)
}

func TestKnowledgeScopeResolverValidatesFolderScopeTargetsBeforeRepositoryAccess(
	t *testing.T,
) {
	repository, reader := knowledgeScopeTestRepository(
		knowledgeScopeTestTree(),
		nil,
	)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID:    knowledgeScopeTestKB,
			FolderIDs:          []string{knowledgeScopeTestFolderA},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
		{
			KnowledgeBaseID: knowledgeScopeTestOtherKB,
			FolderIDs:       []string{knowledgeScopeTestFolderA},
		},
	}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)
	assert.Empty(t, repository.snapshotCalls)
	assert.Empty(t, reader.listCalls)
	assert.Empty(t, reader.subtreeCalls)
}

func TestKnowledgeScopeResolverRootDirectUsesZeroQueries(t *testing.T) {
	repository, _ := knowledgeScopeTestRepository(nil, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID:    knowledgeScopeTestKB,
		FolderIDs:          []string{types.KnowledgeFolderRootID},
		IncludeDescendants: knowledgeScopeTestBool(false),
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	filter := knowledgeScopeTestFilter(t, scope)
	assert.True(t, filter.Enabled())
	assert.Equal(t, []string{types.KnowledgeFolderRootID}, filter.FolderIDs())
	assert.Empty(t, repository.snapshotCalls)
}

func TestKnowledgeScopeResolverRootRecursiveUsesZeroQueries(t *testing.T) {
	repository, _ := knowledgeScopeTestRepository(nil, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{types.KnowledgeFolderRootID},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	assert.False(t, knowledgeScopeTestFilter(t, scope).Enabled())
	assert.Empty(t, repository.snapshotCalls)
}

func TestKnowledgeScopeResolverRootRecursiveEntryDisablesOnlyMatchingTarget(
	t *testing.T,
) {
	otherReader := knowledgeScopeTestReaderForTarget(
		knowledgeScopeTestTenant,
		knowledgeScopeTestOtherKB,
	)
	repository := &knowledgeScopeRepositoryFake{
		readers: map[string]interfaces.KnowledgeFolderScopeReader{
			knowledgeScopeTestOtherKB: otherReader,
		},
	}
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID: knowledgeScopeTestKB,
			FolderIDs:       []string{types.KnowledgeFolderRootID},
		},
		{
			KnowledgeBaseID:    knowledgeScopeTestOtherKB,
			FolderIDs:          []string{knowledgeScopeTestFolderA},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
	}
	input := types.KnowledgeScopeResolveInput{
		Request: &types.KnowledgeScopeRequest{FolderScopes: &folderScopes},
		AuthorizedTargets: []types.AuthorizedKnowledgeScopeTarget{
			knowledgeScopeTestAuthorizedTarget(),
			knowledgeScopeTestAuthorizedTargetFor(
				knowledgeScopeTestTenant,
				knowledgeScopeTestOtherKB,
			),
		},
	}

	scope, err := resolver.Resolve(context.Background(), input)

	require.NoError(t, err)
	assert.False(
		t,
		knowledgeScopeTestFilterForKB(
			t,
			scope,
			knowledgeScopeTestKB,
		).Enabled(),
	)
	otherFilter := knowledgeScopeTestFilterForKB(
		t,
		scope,
		knowledgeScopeTestOtherKB,
	)
	assert.True(t, otherFilter.Enabled())
	assert.Equal(t, []string{knowledgeScopeTestFolderA}, otherFilter.FolderIDs())
	require.Len(t, repository.snapshotCalls, 1)
	assert.Equal(
		t,
		knowledgeScopeTestOtherKB,
		repository.snapshotCalls[0].knowledgeBaseID,
	)
}

func TestKnowledgeScopeResolverUsesIndexedFolderScopeLookup(t *testing.T) {
	const (
		rootKnowledgeBaseID    = "kb-3"
		missingKnowledgeBaseID = "kb-4"
	)
	firstReader := knowledgeScopeTestReaderForTarget(
		knowledgeScopeTestTenant,
		knowledgeScopeTestKB,
	)
	secondReader := knowledgeScopeTestReaderForTarget(
		knowledgeScopeTestTenant,
		knowledgeScopeTestOtherKB,
	)
	repository := &knowledgeScopeRepositoryFake{
		readers: map[string]interfaces.KnowledgeFolderScopeReader{
			knowledgeScopeTestKB:      firstReader,
			knowledgeScopeTestOtherKB: secondReader,
		},
	}
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID:    knowledgeScopeTestKB,
			FolderIDs:          []string{knowledgeScopeTestFolderA},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
		{
			KnowledgeBaseID:    knowledgeScopeTestOtherKB,
			FolderIDs:          []string{knowledgeScopeTestFolderD},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
		{
			KnowledgeBaseID: rootKnowledgeBaseID,
			FolderIDs:       []string{types.KnowledgeFolderRootID},
		},
	}
	input := types.KnowledgeScopeResolveInput{
		Request: &types.KnowledgeScopeRequest{FolderScopes: &folderScopes},
		AuthorizedTargets: []types.AuthorizedKnowledgeScopeTarget{
			knowledgeScopeTestAuthorizedTargetFor(
				knowledgeScopeTestTenant,
				missingKnowledgeBaseID,
			),
			knowledgeScopeTestAuthorizedTargetFor(
				knowledgeScopeTestTenant,
				rootKnowledgeBaseID,
			),
			knowledgeScopeTestAuthorizedTargetFor(
				knowledgeScopeTestTenant,
				knowledgeScopeTestOtherKB,
			),
			knowledgeScopeTestAuthorizedTarget(),
		},
	}

	scope, err := resolver.Resolve(context.Background(), input)

	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{knowledgeScopeTestFolderA},
		knowledgeScopeTestFilterForKB(
			t,
			scope,
			knowledgeScopeTestKB,
		).FolderIDs(),
	)
	assert.Equal(
		t,
		[]string{knowledgeScopeTestFolderD},
		knowledgeScopeTestFilterForKB(
			t,
			scope,
			knowledgeScopeTestOtherKB,
		).FolderIDs(),
	)
	assert.False(
		t,
		knowledgeScopeTestFilterForKB(
			t,
			scope,
			rootKnowledgeBaseID,
		).Enabled(),
	)
	missingFilter := knowledgeScopeTestFilterForKB(
		t,
		scope,
		missingKnowledgeBaseID,
	)
	assert.True(t, missingFilter.Enabled())
	assert.True(t, missingFilter.Empty())

	require.Len(t, repository.snapshotCalls, 2)
	require.Len(t, firstReader.listCalls, 1)
	assert.Equal(
		t,
		[]string{knowledgeScopeTestFolderA},
		firstReader.listCalls[0].folderIDs,
	)
	require.Len(t, secondReader.listCalls, 1)
	assert.Equal(
		t,
		[]string{knowledgeScopeTestFolderD},
		secondReader.listCalls[0].folderIDs,
	)
	assert.Empty(t, firstReader.subtreeCalls)
	assert.Empty(t, secondReader.subtreeCalls)
}

func TestKnowledgeScopeResolverRejectsRootRecursiveWithNonRootSelector(t *testing.T) {
	repository, reader := knowledgeScopeTestRepository(nil, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs: []string{
			knowledgeScopeTestFolderA,
			types.KnowledgeFolderRootID,
		},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)
	assert.Empty(t, repository.snapshotCalls)
	assert.Empty(t, reader.listCalls)
	assert.Empty(t, reader.subtreeCalls)
}

func TestKnowledgeScopeResolverDirectFolderValidatesAncestorChain(t *testing.T) {
	tree := knowledgeScopeTestTree()
	repository, reader := knowledgeScopeTestRepository(tree, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID:    knowledgeScopeTestKB,
		FolderIDs:          []string{knowledgeScopeTestFolderC},
		IncludeDescendants: knowledgeScopeTestBool(false),
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{knowledgeScopeTestFolderC},
		knowledgeScopeTestFilter(t, scope).FolderIDs(),
	)
	require.Len(t, reader.listCalls, 2)
	assert.Equal(
		t,
		[]string{knowledgeScopeTestFolderA, knowledgeScopeTestFolderB},
		reader.listCalls[1].folderIDs,
	)
}

func TestKnowledgeScopeResolverRecursiveFolderReturnsStableFolderIDs(t *testing.T) {
	tree := knowledgeScopeTestTree()
	repository, _ := knowledgeScopeTestRepository(
		tree,
		[]*types.KnowledgeFolder{
			tree[knowledgeScopeTestFolderC],
			tree[knowledgeScopeTestFolderA],
			tree[knowledgeScopeTestFolderB],
		},
	)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{
			knowledgeScopeTestFolderA,
			knowledgeScopeTestFolderB,
			knowledgeScopeTestFolderC,
		},
		knowledgeScopeTestFilter(t, scope).FolderIDs(),
	)
}

func TestKnowledgeScopeResolverMultiFolderOR(t *testing.T) {
	tree := knowledgeScopeTestTree()
	repository, _ := knowledgeScopeTestRepository(tree, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs: []string{
			knowledgeScopeTestFolderD,
			knowledgeScopeTestFolderA,
		},
		IncludeDescendants: knowledgeScopeTestBool(false),
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{knowledgeScopeTestFolderA, knowledgeScopeTestFolderD},
		knowledgeScopeTestFilter(t, scope).FolderIDs(),
	)
}

func TestKnowledgeScopeResolverPassesOnlyMinimalRecursiveRoots(t *testing.T) {
	tree := knowledgeScopeTestTree()
	repository, reader := knowledgeScopeTestRepository(
		tree,
		[]*types.KnowledgeFolder{
			tree[knowledgeScopeTestFolderA],
			tree[knowledgeScopeTestFolderB],
			tree[knowledgeScopeTestFolderC],
		},
	)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs: []string{
			knowledgeScopeTestFolderA,
			knowledgeScopeTestFolderB,
		},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{
			knowledgeScopeTestFolderA,
			knowledgeScopeTestFolderB,
			knowledgeScopeTestFolderC,
		},
		knowledgeScopeTestFilter(t, scope).FolderIDs(),
	)
	require.Len(t, reader.subtreeCalls, 1)
	assert.Equal(
		t,
		[]string{knowledgeScopeTestFolderA},
		knowledgeScopeTestRootIDs(reader.subtreeCalls[0].roots),
	)
	assert.Equal(
		t,
		tree[knowledgeScopeTestFolderA].Path,
		reader.subtreeCalls[0].roots[0].Path,
	)
}

func TestKnowledgeScopeResolverRecursiveAncestorCoversRecursiveDescendant(
	t *testing.T,
) {
	tree := knowledgeScopeTestTree()
	repository, reader := knowledgeScopeTestRepository(
		tree,
		[]*types.KnowledgeFolder{
			tree[knowledgeScopeTestFolderA],
			tree[knowledgeScopeTestFolderB],
			tree[knowledgeScopeTestFolderC],
		},
	)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs: []string{
			knowledgeScopeTestFolderC,
			knowledgeScopeTestFolderA,
		},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{
			knowledgeScopeTestFolderA,
			knowledgeScopeTestFolderB,
			knowledgeScopeTestFolderC,
		},
		knowledgeScopeTestFilter(t, scope).FolderIDs(),
	)
	require.Len(t, reader.subtreeCalls, 1)
	assert.Equal(
		t,
		[]string{knowledgeScopeTestFolderA},
		knowledgeScopeTestRootIDs(reader.subtreeCalls[0].roots),
	)
}

func TestKnowledgeScopeResolverRecursiveAncestorCoversDirectDescendant(
	t *testing.T,
) {
	tree := knowledgeScopeTestTree()
	repository, reader := knowledgeScopeTestRepository(
		tree,
		[]*types.KnowledgeFolder{
			tree[knowledgeScopeTestFolderA],
			tree[knowledgeScopeTestFolderB],
			tree[knowledgeScopeTestFolderC],
		},
	)
	resolver := newKnowledgeScopeTestResolver(t, repository, KnowledgeScopeLimits{
		MaxSelectors:         10,
		MaxResolvedFolderIDs: 3,
	})
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID: knowledgeScopeTestKB,
			FolderIDs:       []string{knowledgeScopeTestFolderA},
		},
		{
			KnowledgeBaseID:    knowledgeScopeTestKB,
			FolderIDs:          []string{knowledgeScopeTestFolderC},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
	}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{
			knowledgeScopeTestFolderA,
			knowledgeScopeTestFolderB,
			knowledgeScopeTestFolderC,
		},
		knowledgeScopeTestFilter(t, scope).FolderIDs(),
	)
	require.Len(t, reader.subtreeCalls, 1)
	assert.Equal(t, 4, reader.subtreeCalls[0].limit)
}

func TestKnowledgeScopeResolverRejectsSubtreeMissingCoveredSelector(
	t *testing.T,
) {
	tree := knowledgeScopeTestTree()
	repository, _ := knowledgeScopeTestRepository(
		tree,
		[]*types.KnowledgeFolder{
			tree[knowledgeScopeTestFolderA],
			tree[knowledgeScopeTestFolderB],
		},
	)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID: knowledgeScopeTestKB,
			FolderIDs:       []string{knowledgeScopeTestFolderA},
		},
		{
			KnowledgeBaseID:    knowledgeScopeTestKB,
			FolderIDs:          []string{knowledgeScopeTestFolderC},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
	}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
}

func TestKnowledgeScopeResolverNonRecursiveAncestorDoesNotCoverChild(
	t *testing.T,
) {
	tree := knowledgeScopeTestTree()
	repository, reader := knowledgeScopeTestRepository(
		tree,
		[]*types.KnowledgeFolder{
			tree[knowledgeScopeTestFolderB],
			tree[knowledgeScopeTestFolderC],
		},
	)
	resolver := newKnowledgeScopeTestResolver(t, repository, KnowledgeScopeLimits{
		MaxSelectors:         10,
		MaxResolvedFolderIDs: 3,
	})
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID:    knowledgeScopeTestKB,
			FolderIDs:          []string{knowledgeScopeTestFolderA},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
		{
			KnowledgeBaseID: knowledgeScopeTestKB,
			FolderIDs:       []string{knowledgeScopeTestFolderB},
		},
	}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{
			knowledgeScopeTestFolderA,
			knowledgeScopeTestFolderB,
			knowledgeScopeTestFolderC,
		},
		knowledgeScopeTestFilter(t, scope).FolderIDs(),
	)
	require.Len(t, reader.subtreeCalls, 1)
	assert.Equal(
		t,
		[]string{knowledgeScopeTestFolderB},
		knowledgeScopeTestRootIDs(reader.subtreeCalls[0].roots),
	)
	assert.Equal(t, 3, reader.subtreeCalls[0].limit)
}

func TestKnowledgeScopeResolverKeepsDisjointRecursiveRoots(t *testing.T) {
	tree := knowledgeScopeTestTree()
	repository, reader := knowledgeScopeTestRepository(
		tree,
		[]*types.KnowledgeFolder{
			tree[knowledgeScopeTestFolderD],
			tree[knowledgeScopeTestFolderC],
			tree[knowledgeScopeTestFolderA],
			tree[knowledgeScopeTestFolderB],
		},
	)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs: []string{
			knowledgeScopeTestFolderD,
			knowledgeScopeTestFolderA,
		},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{
			knowledgeScopeTestFolderA,
			knowledgeScopeTestFolderB,
			knowledgeScopeTestFolderC,
			knowledgeScopeTestFolderD,
		},
		knowledgeScopeTestFilter(t, scope).FolderIDs(),
	)
	require.Len(t, reader.subtreeCalls, 1)
	assert.Equal(
		t,
		[]string{knowledgeScopeTestFolderA, knowledgeScopeTestFolderD},
		knowledgeScopeTestRootIDs(reader.subtreeCalls[0].roots),
	)
}

func TestKnowledgeScopeResolverKeepsNonRecursiveParentAndChild(t *testing.T) {
	tree := knowledgeScopeTestTree()
	repository, _ := knowledgeScopeTestRepository(tree, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs: []string{
			knowledgeScopeTestFolderA,
			knowledgeScopeTestFolderB,
		},
		IncludeDescendants: knowledgeScopeTestBool(false),
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{knowledgeScopeTestFolderA, knowledgeScopeTestFolderB},
		knowledgeScopeTestFilter(t, scope).FolderIDs(),
	)
}

func TestKnowledgeScopeResolverSameFolderTrueDominatesFalse(t *testing.T) {
	tree := knowledgeScopeTestTree()
	repository, reader := knowledgeScopeTestRepository(
		tree,
		[]*types.KnowledgeFolder{
			tree[knowledgeScopeTestFolderA],
			tree[knowledgeScopeTestFolderB],
			tree[knowledgeScopeTestFolderC],
		},
	)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID:    knowledgeScopeTestKB,
			FolderIDs:          []string{knowledgeScopeTestFolderA},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
		{
			KnowledgeBaseID:    knowledgeScopeTestKB,
			FolderIDs:          []string{knowledgeScopeTestFolderA},
			IncludeDescendants: knowledgeScopeTestBool(true),
		},
	}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	assert.Len(t, reader.subtreeCalls, 1)
	assert.Equal(
		t,
		[]string{
			knowledgeScopeTestFolderA,
			knowledgeScopeTestFolderB,
			knowledgeScopeTestFolderC,
		},
		knowledgeScopeTestFilter(t, scope).FolderIDs(),
	)
}

func TestKnowledgeScopeResolverUsesScopedSnapshotForFolderReads(t *testing.T) {
	tree := knowledgeScopeTestTree()
	repository, reader := knowledgeScopeTestRepository(
		tree,
		[]*types.KnowledgeFolder{
			tree[knowledgeScopeTestFolderB],
			tree[knowledgeScopeTestFolderC],
		},
	)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderB},
	}}
	ctx := context.WithValue(
		context.Background(),
		knowledgeScopeTestContextKey{},
		"scope",
	)

	_, err := resolver.Resolve(ctx, knowledgeScopeTestInput(&folderScopes))

	require.NoError(t, err)
	require.Len(t, repository.snapshotCalls, 1)
	assert.Same(t, ctx, repository.snapshotCalls[0].ctx)
	assert.Equal(t, knowledgeScopeTestTenant, repository.snapshotCalls[0].sourceTenantID)
	assert.Equal(t, knowledgeScopeTestKB, repository.snapshotCalls[0].knowledgeBaseID)
	assert.NotEmpty(t, reader.listCalls)
}

type knowledgeScopeTestContextKey struct{}

func TestKnowledgeScopeResolverDoesNotExpandKnowledgeIDs(t *testing.T) {
	repository, _ := knowledgeScopeTestRepository(nil, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{}
	input := knowledgeScopeTestInput(&folderScopes)
	input.AuthorizedTargets[0].KnowledgeIDs = []string{"knowledge-2", "knowledge-1"}
	input.AuthorizedTargets[0].TagIDs = []string{"tag-2", "tag-1"}
	input.AuthorizedTargets[0].ScopeTagIDs = []string{"scope-tag"}

	scope, err := resolver.Resolve(context.Background(), input)

	require.NoError(t, err)
	target := scope.Targets()[0]
	assert.Equal(t, []string{"knowledge-1", "knowledge-2"}, target.KnowledgeIDs())
	assert.Equal(t, []string{"tag-1", "tag-2"}, target.TagIDs())
	assert.Equal(t, []string{"scope-tag"}, target.ScopeTagIDs())
	assert.Empty(t, repository.snapshotCalls)
}

func TestKnowledgeScopeResolverRejectsMissingSelectedFolder(t *testing.T) {
	repository, _ := knowledgeScopeTestRepository(
		map[string]*types.KnowledgeFolder{},
		nil,
	)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
}

func TestKnowledgeScopeResolverRejectsDeletedFolder(t *testing.T) {
	tree := knowledgeScopeTestTree()
	tree[knowledgeScopeTestFolderA].DeletedAt = gorm.DeletedAt{
		Time:  time.Now(),
		Valid: true,
	}
	repository, _ := knowledgeScopeTestRepository(tree, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
}

func TestKnowledgeScopeResolverRejectsWrongTenant(t *testing.T) {
	tree := knowledgeScopeTestTree()
	tree[knowledgeScopeTestFolderA].TenantID++
	repository, _ := knowledgeScopeTestRepository(tree, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
}

func TestKnowledgeScopeResolverRejectsWrongKnowledgeBase(t *testing.T) {
	tree := knowledgeScopeTestTree()
	tree[knowledgeScopeTestFolderA].KnowledgeBaseID = knowledgeScopeTestOtherKB
	repository, _ := knowledgeScopeTestRepository(tree, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
}

func TestKnowledgeScopeResolverRejectsMissingAncestorAsIntegrity(t *testing.T) {
	tree := knowledgeScopeTestTree()
	delete(tree, knowledgeScopeTestFolderA)
	repository, _ := knowledgeScopeTestRepository(tree, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID:    knowledgeScopeTestKB,
		FolderIDs:          []string{knowledgeScopeTestFolderB},
		IncludeDescendants: knowledgeScopeTestBool(false),
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
}

func TestKnowledgeScopeResolverRejectsWrongScopedAncestorAsIntegrity(t *testing.T) {
	tree := knowledgeScopeTestTree()
	tree[knowledgeScopeTestFolderA].TenantID++
	repository, _ := knowledgeScopeTestRepository(tree, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID:    knowledgeScopeTestKB,
		FolderIDs:          []string{knowledgeScopeTestFolderB},
		IncludeDescendants: knowledgeScopeTestBool(false),
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
}

func TestKnowledgeScopeResolverRejectsInvalidPath(t *testing.T) {
	tree := knowledgeScopeTestTree()
	tree[knowledgeScopeTestFolderA].Path = knowledgeScopeTestFolderA
	repository, _ := knowledgeScopeTestRepository(tree, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
}

func TestKnowledgeScopeResolverRejectsInvalidParent(t *testing.T) {
	tree := knowledgeScopeTestTree()
	tree[knowledgeScopeTestFolderA].ParentID = knowledgeScopeTestFolderD
	repository, _ := knowledgeScopeTestRepository(tree, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
}

func TestKnowledgeScopeResolverRejectsInvalidDepth(t *testing.T) {
	tree := knowledgeScopeTestTree()
	tree[knowledgeScopeTestFolderA].Depth = 2
	repository, _ := knowledgeScopeTestRepository(tree, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
}

func TestKnowledgeScopeResolverRejectsReachablePathMismatch(t *testing.T) {
	tree := knowledgeScopeTestTree()
	mismatch := *tree[knowledgeScopeTestFolderB]
	mismatch.ParentID = knowledgeScopeTestFolderD
	repository, _ := knowledgeScopeTestRepository(
		tree,
		[]*types.KnowledgeFolder{
			tree[knowledgeScopeTestFolderA],
			&mismatch,
		},
	)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
}

func TestKnowledgeScopeResolverRejectsDuplicateRows(t *testing.T) {
	tree := knowledgeScopeTestTree()
	reader := &knowledgeScopeReaderFake{
		listFunc: func([]string) ([]*types.KnowledgeFolder, error) {
			return []*types.KnowledgeFolder{
				tree[knowledgeScopeTestFolderA],
				tree[knowledgeScopeTestFolderA],
			}, nil
		},
	}
	repository := &knowledgeScopeRepositoryFake{reader: reader}
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID:    knowledgeScopeTestKB,
		FolderIDs:          []string{knowledgeScopeTestFolderA},
		IncludeDescendants: knowledgeScopeTestBool(false),
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
}

func TestKnowledgeScopeResolverRejectsCycle(t *testing.T) {
	tree := knowledgeScopeTestTree()
	cyclicA := *tree[knowledgeScopeTestFolderA]
	cyclicB := *tree[knowledgeScopeTestFolderB]
	cyclicA.ParentID = cyclicB.ID
	cyclicB.ParentID = cyclicA.ID
	reader := &knowledgeScopeReaderFake{
		folders: tree,
		subtree: []*types.KnowledgeFolder{&cyclicA, &cyclicB},
	}
	repository := &knowledgeScopeRepositoryFake{reader: reader}
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, ErrKnowledgeFolderDataIntegrity)
}

func TestKnowledgeScopeResolverRejectsSelectorLimitWithoutPartialResult(t *testing.T) {
	repository, _ := knowledgeScopeTestRepository(
		knowledgeScopeTestTree(),
		nil,
	)
	resolver := newKnowledgeScopeTestResolver(t, repository, KnowledgeScopeLimits{
		MaxSelectors:         1,
		MaxResolvedFolderIDs: 10000,
	})
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs: []string{
			knowledgeScopeTestFolderA,
			knowledgeScopeTestFolderD,
		},
		IncludeDescendants: knowledgeScopeTestBool(false),
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)
	assert.Empty(t, repository.snapshotCalls)
}

func TestKnowledgeScopeResolverRejectsResolvedLimitWithoutPartialResult(t *testing.T) {
	tree := knowledgeScopeTestTree()
	repository, reader := knowledgeScopeTestRepository(
		tree,
		[]*types.KnowledgeFolder{
			tree[knowledgeScopeTestFolderA],
			tree[knowledgeScopeTestFolderB],
			tree[knowledgeScopeTestFolderC],
		},
	)
	resolver := newKnowledgeScopeTestResolver(t, repository, KnowledgeScopeLimits{
		MaxSelectors:         10,
		MaxResolvedFolderIDs: 2,
	})
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)
	require.Len(t, reader.subtreeCalls, 1)
	assert.Equal(t, 3, reader.subtreeCalls[0].limit)
}

func TestKnowledgeScopeResolverPropagatesRepositoryError(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	repository := &knowledgeScopeRepositoryFake{snapshotErr: databaseErr}
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	assert.Same(t, databaseErr, err)
}

func TestKnowledgeScopeResolverMapsSelectedRepositoryNotFound(t *testing.T) {
	reader := &knowledgeScopeReaderFake{
		listErr: fmt.Errorf(
			"scoped folder unavailable: %w",
			repository.ErrKnowledgeFolderNotFound,
		),
	}
	scopeRepository := &knowledgeScopeRepositoryFake{reader: reader}
	resolver := defaultKnowledgeScopeTestResolver(t, scopeRepository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
}

func TestKnowledgeScopeResolverPropagatesContextCancellation(t *testing.T) {
	repository, _ := knowledgeScopeTestRepository(nil, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	scope, err := resolver.Resolve(ctx, knowledgeScopeTestInput(nil))

	assert.Nil(t, scope)
	assert.Same(t, context.Canceled, err)
	assert.Empty(t, repository.snapshotCalls)
}

type knowledgeScopeMutableContext struct {
	context.Context
	err error
}

func (c *knowledgeScopeMutableContext) Err() error {
	return c.err
}

func TestKnowledgeScopeResolverPreservesWrappedCancellationWhenContextIsCanceled(
	t *testing.T,
) {
	wrapped := fmt.Errorf("测试包装上下文: %w", context.Canceled)
	ctx := &knowledgeScopeMutableContext{Context: context.Background()}
	repository := &knowledgeScopeRepositoryFake{
		snapshotFunc: func(
			context.Context,
			uint64,
			string,
			interfaces.KnowledgeFolderScopeReadSnapshotFunc,
		) error {
			ctx.err = context.Canceled
			return wrapped
		},
	}
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		ctx,
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, context.Canceled)
	assert.True(t, strings.Contains(err.Error(), "测试包装上下文"))
}

func TestKnowledgeScopeResolverPreservesWrappedDeadlineWhenContextDeadlineExceeded(
	t *testing.T,
) {
	wrapped := fmt.Errorf("测试包装上下文: %w", context.DeadlineExceeded)
	ctx := &knowledgeScopeMutableContext{Context: context.Background()}
	repository := &knowledgeScopeRepositoryFake{
		snapshotFunc: func(
			context.Context,
			uint64,
			string,
			interfaces.KnowledgeFolderScopeReadSnapshotFunc,
		) error {
			ctx.err = context.DeadlineExceeded
			return wrapped
		},
	}
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		ctx,
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.True(t, strings.Contains(err.Error(), "测试包装上下文"))
}

func TestKnowledgeScopeResolverReturnsImmutableScope(t *testing.T) {
	repository, _ := knowledgeScopeTestRepository(nil, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{}
	input := knowledgeScopeTestInput(&folderScopes)
	input.AuthorizedTargets[0].KnowledgeIDs = []string{"knowledge-1"}
	input.AuthorizedTargets[0].TagIDs = []string{"tag-1"}
	input.AuthorizedTargets[0].ScopeTagIDs = []string{"scope-tag-1"}

	scope, err := resolver.Resolve(context.Background(), input)
	require.NoError(t, err)
	input.AuthorizedTargets[0].KnowledgeIDs[0] = "mutated"
	input.AuthorizedTargets[0].TagIDs[0] = "mutated"
	input.AuthorizedTargets[0].ScopeTagIDs[0] = "mutated"
	first := scope.Targets()
	first[0] = types.KnowledgeScopeTarget{}
	second := scope.Targets()

	assert.Equal(t, []string{"knowledge-1"}, second[0].KnowledgeIDs())
	assert.Equal(t, []string{"tag-1"}, second[0].TagIDs())
	assert.Equal(t, []string{"scope-tag-1"}, second[0].ScopeTagIDs())
	assert.True(t, second[0].FolderFilter().Empty())
}

func TestKnowledgeScopeResolverRetryClearsAttemptLocalResult(t *testing.T) {
	tree := knowledgeScopeTestTree()
	first := &knowledgeScopeReaderFake{
		folders: tree,
		subtree: []*types.KnowledgeFolder{
			tree[knowledgeScopeTestFolderA],
			tree[knowledgeScopeTestFolderB],
		},
	}
	second := &knowledgeScopeReaderFake{
		folders: tree,
		subtree: []*types.KnowledgeFolder{
			tree[knowledgeScopeTestFolderA],
		},
	}
	repository := &knowledgeScopeRepositoryFake{
		attemptReaders: []interfaces.KnowledgeFolderScopeReader{first, second},
	}
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: knowledgeScopeTestKB,
		FolderIDs:       []string{knowledgeScopeTestFolderA},
	}}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{knowledgeScopeTestFolderA},
		knowledgeScopeTestFilter(t, scope).FolderIDs(),
	)
	assert.Equal(t, 2, repository.retryAttempts)
}

func TestNewKnowledgeScopeResolverRejectsInvalidDependencies(t *testing.T) {
	_, err := NewKnowledgeScopeResolver(nil, KnowledgeScopeLimits{
		MaxSelectors:         1,
		MaxResolvedFolderIDs: 1,
	})
	require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)

	var typedNil *knowledgeScopeRepositoryFake
	_, err = NewKnowledgeScopeResolver(typedNil, KnowledgeScopeLimits{
		MaxSelectors:         1,
		MaxResolvedFolderIDs: 1,
	})
	require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)

	repository, _ := knowledgeScopeTestRepository(nil, nil)
	_, err = NewKnowledgeScopeResolver(repository, KnowledgeScopeLimits{})
	require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)
}

func TestKnowledgeScopeResolverDoesNotMutateSelectorInput(t *testing.T) {
	tree := knowledgeScopeTestTree()
	repository, _ := knowledgeScopeTestRepository(tree, nil)
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	ids := []string{knowledgeScopeTestFolderD, knowledgeScopeTestFolderA}
	original := append([]string(nil), ids...)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID:    knowledgeScopeTestKB,
		FolderIDs:          ids,
		IncludeDescendants: knowledgeScopeTestBool(false),
	}}

	_, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	assert.True(t, slices.Equal(original, ids))
}

func TestKnowledgeScopeResolverProcessesAuthorizedTargetsInStableOrder(
	t *testing.T,
) {
	firstTenant := knowledgeScopeTestTenant + 2
	secondTenant := knowledgeScopeTestTenant - 2
	firstReader := knowledgeScopeTestReaderForTarget(
		firstTenant,
		knowledgeScopeTestKB,
	)
	secondReader := knowledgeScopeTestReaderForTarget(
		secondTenant,
		knowledgeScopeTestOtherKB,
	)
	repository := &knowledgeScopeRepositoryFake{
		readers: map[string]interfaces.KnowledgeFolderScopeReader{
			knowledgeScopeTestKB:      firstReader,
			knowledgeScopeTestOtherKB: secondReader,
		},
	}
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID:    knowledgeScopeTestKB,
			FolderIDs:          []string{knowledgeScopeTestFolderA},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
		{
			KnowledgeBaseID:    knowledgeScopeTestOtherKB,
			FolderIDs:          []string{knowledgeScopeTestFolderA},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
	}
	input := types.KnowledgeScopeResolveInput{
		Request: &types.KnowledgeScopeRequest{FolderScopes: &folderScopes},
		AuthorizedTargets: []types.AuthorizedKnowledgeScopeTarget{
			knowledgeScopeTestAuthorizedTargetFor(
				firstTenant,
				knowledgeScopeTestKB,
			),
			knowledgeScopeTestAuthorizedTargetFor(
				secondTenant,
				knowledgeScopeTestOtherKB,
			),
		},
	}

	_, err := resolver.Resolve(context.Background(), input)

	require.NoError(t, err)
	require.Len(t, repository.snapshotCalls, 2)
	assert.Equal(t, secondTenant, repository.snapshotCalls[0].sourceTenantID)
	assert.Equal(
		t,
		knowledgeScopeTestOtherKB,
		repository.snapshotCalls[0].knowledgeBaseID,
	)
	assert.Equal(t, firstTenant, repository.snapshotCalls[1].sourceTenantID)
	assert.Equal(
		t,
		knowledgeScopeTestKB,
		repository.snapshotCalls[1].knowledgeBaseID,
	)
}

func TestKnowledgeScopeResolverDoesNotMutateAuthorizedTargetOrder(t *testing.T) {
	firstReader := knowledgeScopeTestReaderForTarget(
		knowledgeScopeTestTenant,
		knowledgeScopeTestKB,
	)
	secondReader := knowledgeScopeTestReaderForTarget(
		knowledgeScopeTestTenant,
		knowledgeScopeTestOtherKB,
	)
	repository := &knowledgeScopeRepositoryFake{
		readers: map[string]interfaces.KnowledgeFolderScopeReader{
			knowledgeScopeTestKB:      firstReader,
			knowledgeScopeTestOtherKB: secondReader,
		},
	}
	resolver := defaultKnowledgeScopeTestResolver(t, repository)
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID:    knowledgeScopeTestKB,
			FolderIDs:          []string{knowledgeScopeTestFolderA},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
		{
			KnowledgeBaseID:    knowledgeScopeTestOtherKB,
			FolderIDs:          []string{knowledgeScopeTestFolderA},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
	}
	input := types.KnowledgeScopeResolveInput{
		Request: &types.KnowledgeScopeRequest{FolderScopes: &folderScopes},
		AuthorizedTargets: []types.AuthorizedKnowledgeScopeTarget{
			knowledgeScopeTestAuthorizedTargetFor(
				knowledgeScopeTestTenant,
				knowledgeScopeTestOtherKB,
			),
			knowledgeScopeTestAuthorizedTargetFor(
				knowledgeScopeTestTenant,
				knowledgeScopeTestKB,
			),
		},
	}
	original := append(
		[]types.AuthorizedKnowledgeScopeTarget(nil),
		input.AuthorizedTargets...,
	)

	_, err := resolver.Resolve(context.Background(), input)

	require.NoError(t, err)
	assert.Equal(t, original, input.AuthorizedTargets)
}

func TestKnowledgeScopeResolverReturnsDeterministicFirstErrorAcrossInputOrder(
	t *testing.T,
) {
	firstErr := errors.New("stable first error")
	secondErr := errors.New("later error")
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID:    knowledgeScopeTestKB,
			FolderIDs:          []string{knowledgeScopeTestFolderA},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
		{
			KnowledgeBaseID:    knowledgeScopeTestOtherKB,
			FolderIDs:          []string{knowledgeScopeTestFolderA},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
	}
	sortedFirst := knowledgeScopeTestAuthorizedTargetFor(
		knowledgeScopeTestTenant-1,
		knowledgeScopeTestOtherKB,
	)
	sortedSecond := knowledgeScopeTestAuthorizedTargetFor(
		knowledgeScopeTestTenant+1,
		knowledgeScopeTestKB,
	)
	resolve := func(
		targets []types.AuthorizedKnowledgeScopeTarget,
	) (*types.KnowledgeScope, error) {
		repository := &knowledgeScopeRepositoryFake{
			snapshotErrors: map[string]error{
				knowledgeScopeTestOtherKB: firstErr,
				knowledgeScopeTestKB:      secondErr,
			},
		}
		resolver := defaultKnowledgeScopeTestResolver(t, repository)
		return resolver.Resolve(
			context.Background(),
			types.KnowledgeScopeResolveInput{
				Request: &types.KnowledgeScopeRequest{
					FolderScopes: &folderScopes,
				},
				AuthorizedTargets: targets,
			},
		)
	}

	firstScope, first := resolve(
		[]types.AuthorizedKnowledgeScopeTarget{sortedSecond, sortedFirst},
	)
	secondScope, second := resolve(
		[]types.AuthorizedKnowledgeScopeTarget{sortedFirst, sortedSecond},
	)

	assert.Nil(t, firstScope)
	assert.Nil(t, secondScope)
	assert.Same(t, firstErr, first)
	assert.Same(t, firstErr, second)
}

func TestNewKnowledgeScopeResolverRejectsResolvedLimitOverflow(t *testing.T) {
	repository, _ := knowledgeScopeTestRepository(nil, nil)
	maxInt := int(^uint(0) >> 1)

	resolver, err := NewKnowledgeScopeResolver(repository, KnowledgeScopeLimits{
		MaxSelectors:         1,
		MaxResolvedFolderIDs: maxInt,
	})

	assert.Nil(t, resolver)
	require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)
}

func TestKnowledgeScopeResolverUsesRemainingBudgetAcrossTargets(t *testing.T) {
	firstReader := knowledgeScopeTestReaderForTarget(
		knowledgeScopeTestTenant,
		knowledgeScopeTestKB,
	)
	secondReader := knowledgeScopeTestReaderForTarget(
		knowledgeScopeTestTenant,
		knowledgeScopeTestOtherKB,
		knowledgeScopeTestFolderA,
		knowledgeScopeTestFolderB,
	)
	repository := &knowledgeScopeRepositoryFake{
		readers: map[string]interfaces.KnowledgeFolderScopeReader{
			knowledgeScopeTestKB:      firstReader,
			knowledgeScopeTestOtherKB: secondReader,
		},
	}
	resolver := newKnowledgeScopeTestResolver(t, repository, KnowledgeScopeLimits{
		MaxSelectors:         10,
		MaxResolvedFolderIDs: 3,
	})
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID:    knowledgeScopeTestKB,
			FolderIDs:          []string{knowledgeScopeTestFolderA},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
		{
			KnowledgeBaseID: knowledgeScopeTestOtherKB,
			FolderIDs:       []string{knowledgeScopeTestFolderA},
		},
	}
	input := types.KnowledgeScopeResolveInput{
		Request: &types.KnowledgeScopeRequest{FolderScopes: &folderScopes},
		AuthorizedTargets: []types.AuthorizedKnowledgeScopeTarget{
			knowledgeScopeTestAuthorizedTargetFor(
				knowledgeScopeTestTenant,
				knowledgeScopeTestOtherKB,
			),
			knowledgeScopeTestAuthorizedTarget(),
		},
	}

	scope, err := resolver.Resolve(context.Background(), input)

	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{knowledgeScopeTestFolderA},
		knowledgeScopeTestFilterForKB(
			t,
			scope,
			knowledgeScopeTestKB,
		).FolderIDs(),
	)
	assert.Equal(
		t,
		[]string{knowledgeScopeTestFolderA, knowledgeScopeTestFolderB},
		knowledgeScopeTestFilterForKB(
			t,
			scope,
			knowledgeScopeTestOtherKB,
		).FolderIDs(),
	)
	require.Len(t, secondReader.subtreeCalls, 1)
	assert.Equal(t, 3, secondReader.subtreeCalls[0].limit)
}

func TestKnowledgeScopeResolverStopsBeforeQueryWhenDirectIDsExhaustBudget(
	t *testing.T,
) {
	t.Run("global budget already exhausted", func(t *testing.T) {
		secondReader := knowledgeScopeTestReaderForTarget(
			knowledgeScopeTestTenant,
			knowledgeScopeTestOtherKB,
		)
		repository := &knowledgeScopeRepositoryFake{
			readers: map[string]interfaces.KnowledgeFolderScopeReader{
				knowledgeScopeTestOtherKB: secondReader,
			},
		}
		resolver := newKnowledgeScopeTestResolver(t, repository, KnowledgeScopeLimits{
			MaxSelectors:         10,
			MaxResolvedFolderIDs: 1,
		})
		folderScopes := []types.FolderScopeRequest{
			{
				KnowledgeBaseID:    knowledgeScopeTestKB,
				FolderIDs:          []string{types.KnowledgeFolderRootID},
				IncludeDescendants: knowledgeScopeTestBool(false),
			},
			{
				KnowledgeBaseID:    knowledgeScopeTestOtherKB,
				FolderIDs:          []string{knowledgeScopeTestFolderA},
				IncludeDescendants: knowledgeScopeTestBool(false),
			},
		}
		input := types.KnowledgeScopeResolveInput{
			Request: &types.KnowledgeScopeRequest{FolderScopes: &folderScopes},
			AuthorizedTargets: []types.AuthorizedKnowledgeScopeTarget{
				knowledgeScopeTestAuthorizedTarget(),
				knowledgeScopeTestAuthorizedTargetFor(
					knowledgeScopeTestTenant,
					knowledgeScopeTestOtherKB,
				),
			},
		}

		scope, err := resolver.Resolve(context.Background(), input)

		assert.Nil(t, scope)
		require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)
		assert.Empty(t, repository.snapshotCalls)
		assert.Empty(t, secondReader.listCalls)
		assert.Empty(t, secondReader.subtreeCalls)
	})

	t.Run("validated direct IDs exceed remaining", func(t *testing.T) {
		reader := knowledgeScopeTestReaderForTarget(
			knowledgeScopeTestTenant,
			knowledgeScopeTestKB,
		)
		repository := &knowledgeScopeRepositoryFake{reader: reader}
		resolver := newKnowledgeScopeTestResolver(t, repository, KnowledgeScopeLimits{
			MaxSelectors:         10,
			MaxResolvedFolderIDs: 1,
		})
		folderScopes := []types.FolderScopeRequest{{
			KnowledgeBaseID: knowledgeScopeTestKB,
			FolderIDs: []string{
				knowledgeScopeTestFolderA,
				knowledgeScopeTestFolderD,
			},
			IncludeDescendants: knowledgeScopeTestBool(false),
		}}

		scope, err := resolver.Resolve(
			context.Background(),
			knowledgeScopeTestInput(&folderScopes),
		)

		assert.Nil(t, scope)
		require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)
		require.Len(t, repository.snapshotCalls, 1)
		require.Len(t, reader.listCalls, 1)
		assert.Empty(t, reader.subtreeCalls)
	})
}

func TestKnowledgeScopeResolverSkipsSubtreeWhenDirectIDsConsumeRemainingBudget(
	t *testing.T,
) {
	reader := knowledgeScopeTestReaderForTarget(
		knowledgeScopeTestTenant,
		knowledgeScopeTestKB,
		knowledgeScopeTestFolderD,
	)
	repository := &knowledgeScopeRepositoryFake{reader: reader}
	resolver := newKnowledgeScopeTestResolver(t, repository, KnowledgeScopeLimits{
		MaxSelectors:         10,
		MaxResolvedFolderIDs: 1,
	})
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID:    knowledgeScopeTestKB,
			FolderIDs:          []string{knowledgeScopeTestFolderA},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
		{
			KnowledgeBaseID: knowledgeScopeTestKB,
			FolderIDs:       []string{knowledgeScopeTestFolderD},
		},
	}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)
	assert.ErrorContains(t, err, "resolved folder limit exceeded")
	require.Len(t, repository.snapshotCalls, 1)
	assert.Empty(t, reader.subtreeCalls)
}

func TestKnowledgeScopeResolverDoesNotPublishDirectPartialScopeWhenSubtreeBudgetIsZero(
	t *testing.T,
) {
	reader := knowledgeScopeTestReaderForTarget(
		knowledgeScopeTestTenant,
		knowledgeScopeTestKB,
		knowledgeScopeTestFolderA,
	)
	repository := &knowledgeScopeRepositoryFake{reader: reader}
	resolver := newKnowledgeScopeTestResolver(t, repository, KnowledgeScopeLimits{
		MaxSelectors:         10,
		MaxResolvedFolderIDs: 1,
	})
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID:    knowledgeScopeTestKB,
			FolderIDs:          []string{types.KnowledgeFolderRootID},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
		{
			KnowledgeBaseID: knowledgeScopeTestKB,
			FolderIDs:       []string{knowledgeScopeTestFolderA},
		},
	}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)
	assert.ErrorContains(t, err, "resolved folder limit exceeded")
	require.Len(t, repository.snapshotCalls, 1)
	assert.Empty(t, reader.subtreeCalls)
}

func TestKnowledgeScopeResolverPassesRemainingPlusOneToSubtree(t *testing.T) {
	reader := knowledgeScopeTestReaderForTarget(
		knowledgeScopeTestTenant,
		knowledgeScopeTestKB,
		knowledgeScopeTestFolderA,
		knowledgeScopeTestFolderB,
	)
	repository := &knowledgeScopeRepositoryFake{reader: reader}
	resolver := newKnowledgeScopeTestResolver(t, repository, KnowledgeScopeLimits{
		MaxSelectors:         10,
		MaxResolvedFolderIDs: 3,
	})
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID:    knowledgeScopeTestKB,
			FolderIDs:          []string{types.KnowledgeFolderRootID},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
		{
			KnowledgeBaseID: knowledgeScopeTestKB,
			FolderIDs:       []string{knowledgeScopeTestFolderA},
		},
	}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{
			types.KnowledgeFolderRootID,
			knowledgeScopeTestFolderA,
			knowledgeScopeTestFolderB,
		},
		knowledgeScopeTestFilter(t, scope).FolderIDs(),
	)
	require.Len(t, reader.subtreeCalls, 1)
	assert.Equal(t, 3, reader.subtreeCalls[0].limit)
}

func TestKnowledgeScopeResolverDoesNotResetBudgetPerKnowledgeBase(t *testing.T) {
	firstReader := knowledgeScopeTestReaderForTarget(
		knowledgeScopeTestTenant,
		knowledgeScopeTestKB,
	)
	secondReader := knowledgeScopeTestReaderForTarget(
		knowledgeScopeTestTenant,
		knowledgeScopeTestOtherKB,
		knowledgeScopeTestFolderA,
		knowledgeScopeTestFolderB,
	)
	repository := &knowledgeScopeRepositoryFake{
		readers: map[string]interfaces.KnowledgeFolderScopeReader{
			knowledgeScopeTestKB:      firstReader,
			knowledgeScopeTestOtherKB: secondReader,
		},
	}
	resolver := newKnowledgeScopeTestResolver(t, repository, KnowledgeScopeLimits{
		MaxSelectors:         10,
		MaxResolvedFolderIDs: 2,
	})
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID:    knowledgeScopeTestKB,
			FolderIDs:          []string{knowledgeScopeTestFolderA},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
		{
			KnowledgeBaseID: knowledgeScopeTestOtherKB,
			FolderIDs:       []string{knowledgeScopeTestFolderA},
		},
	}
	input := types.KnowledgeScopeResolveInput{
		Request: &types.KnowledgeScopeRequest{FolderScopes: &folderScopes},
		AuthorizedTargets: []types.AuthorizedKnowledgeScopeTarget{
			knowledgeScopeTestAuthorizedTarget(),
			knowledgeScopeTestAuthorizedTargetFor(
				knowledgeScopeTestTenant,
				knowledgeScopeTestOtherKB,
			),
		},
	}

	scope, err := resolver.Resolve(context.Background(), input)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)
	require.Len(t, secondReader.subtreeCalls, 1)
	assert.Equal(t, 2, secondReader.subtreeCalls[0].limit)
}

func TestKnowledgeScopeResolverRootRecursiveDoesNotConsumeBudget(t *testing.T) {
	repository := &knowledgeScopeRepositoryFake{}
	resolver := newKnowledgeScopeTestResolver(t, repository, KnowledgeScopeLimits{
		MaxSelectors:         10,
		MaxResolvedFolderIDs: 1,
	})
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID: knowledgeScopeTestKB,
			FolderIDs:       []string{types.KnowledgeFolderRootID},
		},
		{
			KnowledgeBaseID:    knowledgeScopeTestOtherKB,
			FolderIDs:          []string{types.KnowledgeFolderRootID},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
	}
	input := types.KnowledgeScopeResolveInput{
		Request: &types.KnowledgeScopeRequest{FolderScopes: &folderScopes},
		AuthorizedTargets: []types.AuthorizedKnowledgeScopeTarget{
			knowledgeScopeTestAuthorizedTarget(),
			knowledgeScopeTestAuthorizedTargetFor(
				knowledgeScopeTestTenant,
				knowledgeScopeTestOtherKB,
			),
		},
	}

	scope, err := resolver.Resolve(context.Background(), input)

	require.NoError(t, err)
	assert.False(
		t,
		knowledgeScopeTestFilterForKB(
			t,
			scope,
			knowledgeScopeTestKB,
		).Enabled(),
	)
	assert.Equal(
		t,
		[]string{types.KnowledgeFolderRootID},
		knowledgeScopeTestFilterForKB(
			t,
			scope,
			knowledgeScopeTestOtherKB,
		).FolderIDs(),
	)
	assert.Empty(t, repository.snapshotCalls)
}

func TestKnowledgeScopeResolverRootDirectConsumesOneBudget(t *testing.T) {
	reader := knowledgeScopeTestReaderForTarget(
		knowledgeScopeTestTenant,
		knowledgeScopeTestKB,
		knowledgeScopeTestFolderA,
	)
	repository := &knowledgeScopeRepositoryFake{reader: reader}
	resolver := newKnowledgeScopeTestResolver(t, repository, KnowledgeScopeLimits{
		MaxSelectors:         10,
		MaxResolvedFolderIDs: 2,
	})
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID:    knowledgeScopeTestKB,
			FolderIDs:          []string{types.KnowledgeFolderRootID},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
		{
			KnowledgeBaseID: knowledgeScopeTestKB,
			FolderIDs:       []string{knowledgeScopeTestFolderA},
		},
	}

	scope, err := resolver.Resolve(
		context.Background(),
		knowledgeScopeTestInput(&folderScopes),
	)

	require.NoError(t, err)
	assert.Equal(
		t,
		[]string{
			types.KnowledgeFolderRootID,
			knowledgeScopeTestFolderA,
		},
		knowledgeScopeTestFilter(t, scope).FolderIDs(),
	)
	require.Len(t, reader.subtreeCalls, 1)
	assert.Equal(t, 2, reader.subtreeCalls[0].limit)
}

func TestKnowledgeScopeResolverReturnsNoPartialScopeOnGlobalLimit(t *testing.T) {
	firstReader := knowledgeScopeTestReaderForTarget(
		knowledgeScopeTestTenant,
		knowledgeScopeTestKB,
		knowledgeScopeTestFolderA,
		knowledgeScopeTestFolderB,
	)
	repository := &knowledgeScopeRepositoryFake{
		readers: map[string]interfaces.KnowledgeFolderScopeReader{
			knowledgeScopeTestKB: firstReader,
		},
	}
	resolver := newKnowledgeScopeTestResolver(t, repository, KnowledgeScopeLimits{
		MaxSelectors:         10,
		MaxResolvedFolderIDs: 2,
	})
	folderScopes := []types.FolderScopeRequest{
		{
			KnowledgeBaseID: knowledgeScopeTestKB,
			FolderIDs:       []string{knowledgeScopeTestFolderA},
		},
		{
			KnowledgeBaseID:    knowledgeScopeTestOtherKB,
			FolderIDs:          []string{types.KnowledgeFolderRootID},
			IncludeDescendants: knowledgeScopeTestBool(false),
		},
	}
	input := types.KnowledgeScopeResolveInput{
		Request: &types.KnowledgeScopeRequest{FolderScopes: &folderScopes},
		AuthorizedTargets: []types.AuthorizedKnowledgeScopeTarget{
			knowledgeScopeTestAuthorizedTarget(),
			knowledgeScopeTestAuthorizedTargetFor(
				knowledgeScopeTestTenant,
				knowledgeScopeTestOtherKB,
			),
		},
	}

	scope, err := resolver.Resolve(context.Background(), input)

	assert.Nil(t, scope)
	require.ErrorIs(t, err, types.ErrInvalidKnowledgeScopeRequest)
	require.Len(t, repository.snapshotCalls, 1)
	assert.Equal(
		t,
		knowledgeScopeTestKB,
		repository.snapshotCalls[0].knowledgeBaseID,
	)
}
