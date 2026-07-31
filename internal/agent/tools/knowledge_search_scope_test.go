package tools

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests exercise the tool boundary directly. They do not assert that
// folder-scoped QA is available through the upstream runtime gates.

const (
	testFolderA       = "11111111-1111-4111-8111-111111111111"
	testFolderMutated = "22222222-2222-4222-8222-222222222222"
	testFolderFirst   = "33333333-3333-4333-8333-333333333333"
	testFolderSecond  = "44444444-4444-4444-8444-444444444444"
)

type capturedHybridSearchCall struct {
	primaryKBID string
	params      types.SearchParams
}

type capturingKnowledgeBaseService struct {
	interfaces.KnowledgeBaseService

	mu             sync.Mutex
	calls          []capturedHybridSearchCall
	embeddingKBIDs []string
	kbs            map[string]*types.KnowledgeBase
	modelKeys      map[string]string
	search         func(string, types.SearchParams) ([]*types.SearchResult, error)
}

func newCapturingKnowledgeBaseService(targets types.SearchTargets) *capturingKnowledgeBaseService {
	service := &capturingKnowledgeBaseService{
		kbs:       make(map[string]*types.KnowledgeBase, len(targets)),
		modelKeys: make(map[string]string, len(targets)),
	}
	for _, target := range targets {
		if target == nil {
			continue
		}
		service.kbs[target.KnowledgeBaseID] = &types.KnowledgeBase{
			ID:               target.KnowledgeBaseID,
			Type:             types.KnowledgeBaseTypeDocument,
			TenantID:         target.EffectiveSourceTenantID(),
			IndexingStrategy: types.DefaultIndexingStrategy(),
		}
		service.modelKeys[target.KnowledgeBaseID] = "shared-model"
	}
	return service
}

func (s *capturingKnowledgeBaseService) GetKnowledgeBaseByID(
	_ context.Context,
	id string,
) (*types.KnowledgeBase, error) {
	return s.kbs[id], nil
}

func (s *capturingKnowledgeBaseService) GetKnowledgeBasesByIDsOnly(
	_ context.Context,
	ids []string,
) ([]*types.KnowledgeBase, error) {
	kbs := make([]*types.KnowledgeBase, 0, len(ids))
	for _, id := range ids {
		if kb := s.kbs[id]; kb != nil {
			kbs = append(kbs, kb)
		}
	}
	return kbs, nil
}

func (s *capturingKnowledgeBaseService) ResolveEmbeddingModelKeys(
	_ context.Context,
	kbs []*types.KnowledgeBase,
) map[string]string {
	keys := make(map[string]string, len(kbs))
	for _, kb := range kbs {
		if kb != nil {
			if modelKey, ok := s.modelKeys[kb.ID]; ok {
				keys[kb.ID] = modelKey
			}
		}
	}
	return keys
}

func (s *capturingKnowledgeBaseService) GetQueryEmbedding(
	_ context.Context,
	kbID string,
	_ string,
) ([]float32, error) {
	s.mu.Lock()
	s.embeddingKBIDs = append(s.embeddingKBIDs, kbID)
	s.mu.Unlock()
	return []float32{0.1, 0.2}, nil
}

func (s *capturingKnowledgeBaseService) HybridSearch(
	_ context.Context,
	id string,
	params types.SearchParams,
) ([]*types.SearchResult, error) {
	s.mu.Lock()
	s.calls = append(s.calls, capturedHybridSearchCall{
		primaryKBID: id,
		params:      params,
	})
	s.mu.Unlock()

	if s.search != nil {
		return s.search(id, params)
	}
	return successfulKnowledgeSearchResults(id), nil
}

func (s *capturingKnowledgeBaseService) callsSnapshot() []capturedHybridSearchCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]capturedHybridSearchCall(nil), s.calls...)
}

func (s *capturingKnowledgeBaseService) embeddingCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.embeddingKBIDs)
}

func (s *capturingKnowledgeBaseService) embeddingKBIDsSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.embeddingKBIDs...)
}

type knowledgeSearchChunkRepository struct {
	interfaces.ChunkRepository
}

func (*knowledgeSearchChunkRepository) ListPagedChunksByKnowledgeID(
	context.Context,
	uint64,
	string,
	*types.Pagination,
	[]types.ChunkType,
	string,
	string,
	string,
	string,
	string,
) ([]*types.Chunk, int64, error) {
	return nil, 1, nil
}

func (*knowledgeSearchChunkRepository) ListChunksByParentIDs(
	context.Context,
	uint64,
	[]string,
) ([]*types.Chunk, error) {
	return nil, nil
}

type knowledgeSearchChunkService struct {
	interfaces.ChunkService
	repo interfaces.ChunkRepository
}

func (s *knowledgeSearchChunkService) GetRepository() interfaces.ChunkRepository {
	return s.repo
}

func successfulKnowledgeSearchResults(kbID string) []*types.SearchResult {
	return []*types.SearchResult{{
		ID:              "chunk-" + kbID,
		Content:         "successful scoped content",
		KnowledgeID:     "knowledge-" + kbID,
		KnowledgeBaseID: kbID,
		KnowledgeTitle:  "Successful document",
		ChunkIndex:      1,
		ChunkType:       string(types.ChunkTypeText),
		Score:           0.9,
		ImageInfo:       "[]",
	}}
}

func executeKnowledgeSearch(
	t *testing.T,
	service *capturingKnowledgeBaseService,
	targets types.SearchTargets,
) (*types.ToolResult, error) {
	t.Helper()
	tool := NewKnowledgeSearchTool(
		service,
		nil,
		&knowledgeSearchChunkService{repo: &knowledgeSearchChunkRepository{}},
		targets,
		nil,
		nil,
		nil,
	)
	args, err := json.Marshal(KnowledgeSearchInput{
		Queries: []string{"scope propagation"},
	})
	require.NoError(t, err)
	return tool.Execute(context.Background(), args)
}

func mustResolvedFolderFilter(
	t *testing.T,
	enabled bool,
	folderIDs []string,
) types.ResolvedFolderFilter {
	t.Helper()
	filter, err := types.NewResolvedFolderFilter(enabled, folderIDs)
	require.NoError(t, err)
	return filter
}

func requireCapturedCall(
	t *testing.T,
	calls []capturedHybridSearchCall,
	primaryKBID string,
) capturedHybridSearchCall {
	t.Helper()
	for _, call := range calls {
		if call.primaryKBID == primaryKBID {
			return call
		}
	}
	t.Fatalf("HybridSearch call for %q not found in %#v", primaryKBID, calls)
	return capturedHybridSearchCall{}
}

func TestKnowledgeSearchTool_IndividualTargetPropagatesCompleteScopeWithIndependentCopies(t *testing.T) {
	target := &types.SearchTarget{
		Type:               types.SearchTargetTypeKnowledgeBase,
		KnowledgeBaseID:    "kb-scoped",
		TenantID:           10,
		SourceTenantID:     20,
		KnowledgeIDs:       []string{"knowledge-a"},
		TagIDs:             []string{"tag-a"},
		ScopeTagIDs:        []string{"scope-tag-a"},
		FolderFilter:       mustResolvedFolderFilter(t, true, []string{testFolderA}),
		ExecutionScopeHash: "scope-hash-a",
	}
	targets := types.SearchTargets{target}
	service := newCapturingKnowledgeBaseService(targets)

	result, err := executeKnowledgeSearch(t, service, targets)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Success)

	calls := service.callsSnapshot()
	require.Len(t, calls, 1)
	call := calls[0]
	assert.Equal(t, target.KnowledgeBaseID, call.primaryKBID)
	assert.Empty(t, call.params.KnowledgeBaseIDs)
	assert.Equal(t, uint64(20), call.params.SourceTenantID)
	assert.Equal(t, "scope-hash-a", call.params.ExecutionScopeHash)
	assert.True(t, call.params.FolderFilter.Enabled())
	assert.Equal(t, []string{testFolderA}, call.params.FolderFilter.FolderIDs())
	assert.Equal(t, []string{"knowledge-a"}, call.params.KnowledgeIDs)
	assert.Equal(t, []string{"tag-a"}, call.params.TagIDs)
	assert.Equal(t, []string{"scope-tag-a"}, call.params.ScopeTagIDs)

	target.KnowledgeIDs[0] = "knowledge-mutated"
	target.TagIDs[0] = "tag-mutated"
	target.ScopeTagIDs[0] = "scope-tag-mutated"
	target.FolderFilter = mustResolvedFolderFilter(t, true, []string{testFolderMutated})
	assert.Equal(t, []string{"knowledge-a"}, call.params.KnowledgeIDs)
	assert.Equal(t, []string{"tag-a"}, call.params.TagIDs)
	assert.Equal(t, []string{"scope-tag-a"}, call.params.ScopeTagIDs)
	assert.Equal(t, []string{testFolderA}, call.params.FolderFilter.FolderIDs())
}

func TestKnowledgeSearchTool_FolderEnabledTargetUsesIndividualSearch(t *testing.T) {
	folderTarget := &types.SearchTarget{
		Type:               types.SearchTargetTypeKnowledgeBase,
		KnowledgeBaseID:    "kb-folder",
		TenantID:           42,
		SourceTenantID:     42,
		FolderFilter:       mustResolvedFolderFilter(t, true, []string{testFolderA}),
		ExecutionScopeHash: "shared-hash",
	}
	unrestrictedTarget := &types.SearchTarget{
		Type:               types.SearchTargetTypeKnowledgeBase,
		KnowledgeBaseID:    "kb-unrestricted",
		TenantID:           42,
		SourceTenantID:     42,
		ExecutionScopeHash: "shared-hash",
	}
	targets := types.SearchTargets{folderTarget, unrestrictedTarget}
	service := newCapturingKnowledgeBaseService(targets)

	result, err := executeKnowledgeSearch(t, service, targets)
	require.NoError(t, err)
	require.True(t, result.Success)

	calls := service.callsSnapshot()
	require.Len(t, calls, 2)
	folderCall := requireCapturedCall(t, calls, "kb-folder")
	unrestrictedCall := requireCapturedCall(t, calls, "kb-unrestricted")

	assert.Empty(t, folderCall.params.KnowledgeBaseIDs)
	assert.True(t, folderCall.params.FolderFilter.Enabled())
	assert.Equal(t, []string{testFolderA}, folderCall.params.FolderFilter.FolderIDs())
	assert.Equal(t, []string{"kb-unrestricted"}, unrestrictedCall.params.KnowledgeBaseIDs)
	assert.False(t, unrestrictedCall.params.FolderFilter.Enabled())
	for _, call := range calls {
		if !call.params.FolderFilter.Enabled() {
			assert.NotContains(t, call.params.KnowledgeBaseIDs, "kb-folder")
		}
	}
}

func TestKnowledgeSearchTool_RestrictedTargetsUseIndividualSearch(t *testing.T) {
	tests := []struct {
		name         string
		knowledgeIDs []string
		tagIDs       []string
		scopeTagIDs  []string
	}{
		{
			name:         "knowledge_ids",
			knowledgeIDs: []string{"knowledge-a"},
		},
		{
			name:   "tag_ids",
			tagIDs: []string{"tag-a"},
		},
		{
			name:        "scope_tag_ids",
			scopeTagIDs: []string{"scope-tag-a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := &types.SearchTarget{
				Type:               types.SearchTargetTypeKnowledgeBase,
				KnowledgeBaseID:    "kb-" + tt.name,
				TenantID:           42,
				SourceTenantID:     42,
				KnowledgeIDs:       tt.knowledgeIDs,
				TagIDs:             tt.tagIDs,
				ScopeTagIDs:        tt.scopeTagIDs,
				ExecutionScopeHash: "scope-hash",
			}
			targets := types.SearchTargets{target}
			service := newCapturingKnowledgeBaseService(targets)

			result, err := executeKnowledgeSearch(t, service, targets)
			require.NoError(t, err)
			require.True(t, result.Success)

			calls := service.callsSnapshot()
			require.Len(t, calls, 1)
			call := calls[0]
			assert.Empty(t, call.params.KnowledgeBaseIDs)
			assert.Equal(t, tt.knowledgeIDs, call.params.KnowledgeIDs)
			assert.Equal(t, tt.tagIDs, call.params.TagIDs)
			assert.Equal(t, tt.scopeTagIDs, call.params.ScopeTagIDs)
		})
	}
}

func TestKnowledgeSearchTool_CompatibleUnrestrictedTargetsAreCombined(t *testing.T) {
	targets := types.SearchTargets{
		{
			Type:               types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID:    "kb-combined-a",
			TenantID:           42,
			SourceTenantID:     42,
			ExecutionScopeHash: "shared-hash",
		},
		{
			Type:               types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID:    "kb-combined-b",
			TenantID:           42,
			SourceTenantID:     42,
			ExecutionScopeHash: "shared-hash",
		},
	}
	service := newCapturingKnowledgeBaseService(targets)

	result, err := executeKnowledgeSearch(t, service, targets)
	require.NoError(t, err)
	require.True(t, result.Success)

	calls := service.callsSnapshot()
	require.Len(t, calls, 1)
	assert.ElementsMatch(
		t,
		[]string{"kb-combined-a", "kb-combined-b"},
		calls[0].params.KnowledgeBaseIDs,
	)
	assert.Equal(t, uint64(42), calls[0].params.SourceTenantID)
	assert.Equal(t, "shared-hash", calls[0].params.ExecutionScopeHash)
	assert.False(t, calls[0].params.FolderFilter.Enabled())
	assert.Equal(t, 1, service.embeddingCallCount())
}

func TestKnowledgeSearchTool_UnknownModelIdentityTargetsAreNotCombined(t *testing.T) {
	targets := types.SearchTargets{
		{
			Type:               types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID:    "kb-model-missing",
			TenantID:           42,
			SourceTenantID:     42,
			ExecutionScopeHash: "shared-hash",
		},
		{
			Type:               types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID:    "kb-model-empty",
			TenantID:           42,
			SourceTenantID:     42,
			ExecutionScopeHash: "shared-hash",
		},
	}
	service := newCapturingKnowledgeBaseService(targets)
	delete(service.modelKeys, "kb-model-missing")
	service.modelKeys["kb-model-empty"] = ""

	result, err := executeKnowledgeSearch(t, service, targets)
	require.NoError(t, err)
	require.True(t, result.Success)

	calls := service.callsSnapshot()
	require.Len(t, calls, 2)
	missingCall := requireCapturedCall(t, calls, "kb-model-missing")
	emptyCall := requireCapturedCall(t, calls, "kb-model-empty")
	assert.Equal(t, []string{"kb-model-missing"}, missingCall.params.KnowledgeBaseIDs)
	assert.Equal(t, []string{"kb-model-empty"}, emptyCall.params.KnowledgeBaseIDs)
	assert.Equal(t, 0, service.embeddingCallCount())
}

func TestKnowledgeSearchTool_QueryEmbeddingIsReusedAcrossRetrievalScopes(t *testing.T) {
	targets := types.SearchTargets{
		{
			Type:               types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID:    "kb-embedding-scope-a",
			TenantID:           42,
			SourceTenantID:     42,
			ExecutionScopeHash: "scope-hash-a",
		},
		{
			Type:               types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID:    "kb-embedding-scope-b",
			TenantID:           42,
			SourceTenantID:     42,
			ExecutionScopeHash: "scope-hash-b",
		},
	}
	service := newCapturingKnowledgeBaseService(targets)

	result, err := executeKnowledgeSearch(t, service, targets)
	require.NoError(t, err)
	require.True(t, result.Success)

	calls := service.callsSnapshot()
	require.Len(t, calls, 2)
	callA := requireCapturedCall(t, calls, "kb-embedding-scope-a")
	callB := requireCapturedCall(t, calls, "kb-embedding-scope-b")
	assert.Equal(t, []string{"kb-embedding-scope-a"}, callA.params.KnowledgeBaseIDs)
	assert.Equal(t, []string{"kb-embedding-scope-b"}, callB.params.KnowledgeBaseIDs)
	assert.Equal(t, []float32{0.1, 0.2}, callA.params.QueryEmbedding)
	assert.Equal(t, []float32{0.1, 0.2}, callB.params.QueryEmbedding)
	assert.Equal(t, "scope-hash-a", callA.params.ExecutionScopeHash)
	assert.Equal(t, "scope-hash-b", callB.params.ExecutionScopeHash)
	assert.Equal(t, 1, service.embeddingCallCount())
}

func TestKnowledgeSearchTool_QueryEmbeddingIsNotSharedAcrossSourceTenants(t *testing.T) {
	targets := types.SearchTargets{
		{
			Type:               types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID:    "kb-source-a",
			TenantID:           7,
			SourceTenantID:     101,
			ExecutionScopeHash: "shared-hash",
		},
		{
			Type:               types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID:    "kb-source-b",
			TenantID:           7,
			SourceTenantID:     202,
			ExecutionScopeHash: "shared-hash",
		},
	}
	service := newCapturingKnowledgeBaseService(targets)

	result, err := executeKnowledgeSearch(t, service, targets)
	require.NoError(t, err)
	require.True(t, result.Success)

	calls := service.callsSnapshot()
	require.Len(t, calls, 2)
	callA := requireCapturedCall(t, calls, "kb-source-a")
	callB := requireCapturedCall(t, calls, "kb-source-b")
	assert.Equal(t, []string{"kb-source-a"}, callA.params.KnowledgeBaseIDs)
	assert.Equal(t, []string{"kb-source-b"}, callB.params.KnowledgeBaseIDs)
	assert.Equal(t, uint64(101), callA.params.SourceTenantID)
	assert.Equal(t, uint64(202), callB.params.SourceTenantID)
	assert.Equal(t, "shared-hash", callA.params.ExecutionScopeHash)
	assert.Equal(t, "shared-hash", callB.params.ExecutionScopeHash)
	assert.Equal(t, 2, service.embeddingCallCount())
	assert.ElementsMatch(
		t,
		[]string{"kb-source-a", "kb-source-b"},
		service.embeddingKBIDsSnapshot(),
	)
}

func TestKnowledgeSearchTool_UnrestrictedTargetsWithIncompatibleExecutionHashesAreNotCombined(t *testing.T) {
	tests := []struct {
		name  string
		hashA string
		hashB string
	}{
		{name: "different_nonempty_hashes", hashA: "hash-a", hashB: "hash-b"},
		{name: "empty_and_nonempty_hash", hashA: "", hashB: "hash-b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targets := types.SearchTargets{
				{
					Type:               types.SearchTargetTypeKnowledgeBase,
					KnowledgeBaseID:    "kb-hash-a",
					TenantID:           42,
					SourceTenantID:     42,
					ExecutionScopeHash: tt.hashA,
				},
				{
					Type:               types.SearchTargetTypeKnowledgeBase,
					KnowledgeBaseID:    "kb-hash-b",
					TenantID:           42,
					SourceTenantID:     42,
					ExecutionScopeHash: tt.hashB,
				},
			}
			service := newCapturingKnowledgeBaseService(targets)

			result, err := executeKnowledgeSearch(t, service, targets)
			require.NoError(t, err)
			require.True(t, result.Success)

			calls := service.callsSnapshot()
			require.Len(t, calls, 2)
			callA := requireCapturedCall(t, calls, "kb-hash-a")
			callB := requireCapturedCall(t, calls, "kb-hash-b")
			assert.Equal(t, []string{"kb-hash-a"}, callA.params.KnowledgeBaseIDs)
			assert.Equal(t, []string{"kb-hash-b"}, callB.params.KnowledgeBaseIDs)
			assert.Equal(t, tt.hashA, callA.params.ExecutionScopeHash)
			assert.Equal(t, tt.hashB, callB.params.ExecutionScopeHash)
		})
	}
}

func TestKnowledgeSearchTool_EnabledEmptyFolderTargetSkipsHybridSearch(t *testing.T) {
	targets := types.SearchTargets{{
		Type:            types.SearchTargetTypeKnowledgeBase,
		KnowledgeBaseID: "kb-empty-folder",
		TenantID:        42,
		SourceTenantID:  42,
		FolderFilter:    mustResolvedFolderFilter(t, true, nil),
	}}
	service := newCapturingKnowledgeBaseService(targets)

	result, err := executeKnowledgeSearch(t, service, targets)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, 0, result.Data["count"])
	assert.Empty(t, service.callsSnapshot())
}

func TestKnowledgeSearchTool_EnabledEmptyFolderTargetDoesNotSuppressActiveTarget(t *testing.T) {
	targets := types.SearchTargets{
		{
			Type:               types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID:    "kb-empty-folder",
			TenantID:           42,
			SourceTenantID:     42,
			FolderFilter:       mustResolvedFolderFilter(t, true, nil),
			ExecutionScopeHash: "shared-hash",
		},
		{
			Type:               types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID:    "kb-active",
			TenantID:           42,
			SourceTenantID:     42,
			ExecutionScopeHash: "shared-hash",
		},
	}
	service := newCapturingKnowledgeBaseService(targets)

	result, err := executeKnowledgeSearch(t, service, targets)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Success)
	assert.Equal(t, 1, result.Data["count"])

	calls := service.callsSnapshot()
	require.Len(t, calls, 1)
	assert.Equal(t, "kb-active", calls[0].primaryKBID)
	assert.Equal(t, []string{"kb-active"}, calls[0].params.KnowledgeBaseIDs)
	assert.NotContains(t, calls[0].params.KnowledgeBaseIDs, "kb-empty-folder")
}

func TestKnowledgeSearchTool_FolderScopedSearchErrorIsReturnedWithoutPartialSuccess(t *testing.T) {
	scopedErr := errors.New("folder-scoped retrieval failed")
	targets := types.SearchTargets{
		{
			Type:               types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID:    "kb-folder-error",
			TenantID:           42,
			SourceTenantID:     42,
			FolderFilter:       mustResolvedFolderFilter(t, true, []string{testFolderA}),
			ExecutionScopeHash: "shared-hash",
		},
		{
			Type:               types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID:    "kb-unrestricted-success",
			TenantID:           42,
			SourceTenantID:     42,
			ExecutionScopeHash: "shared-hash",
		},
	}
	service := newCapturingKnowledgeBaseService(targets)
	service.search = func(id string, _ types.SearchParams) ([]*types.SearchResult, error) {
		if id == "kb-folder-error" {
			return nil, scopedErr
		}
		return successfulKnowledgeSearchResults(id), nil
	}

	result, err := executeKnowledgeSearch(t, service, targets)
	require.ErrorIs(t, err, scopedErr)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	assert.Equal(t, "folder-scoped knowledge search failed", result.Error)
	assert.Empty(t, result.Output)
	assert.Nil(t, result.Data)
	assert.Len(t, service.callsSnapshot(), 2)
}

func TestKnowledgeSearchTool_FolderScopedErrorsUseStableTargetOrder(t *testing.T) {
	firstErr := errors.New("first folder target failed")
	secondErr := errors.New("second folder target failed")
	firstStarted := make(chan struct{})
	secondFailureCompleted := make(chan struct{})
	targets := types.SearchTargets{
		{
			Type:               types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID:    "kb-folder-first",
			TenantID:           42,
			SourceTenantID:     42,
			FolderFilter:       mustResolvedFolderFilter(t, true, []string{testFolderFirst}),
			ExecutionScopeHash: "scope-hash-first",
		},
		{
			Type:               types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID:    "kb-folder-second",
			TenantID:           42,
			SourceTenantID:     42,
			FolderFilter:       mustResolvedFolderFilter(t, true, []string{testFolderSecond}),
			ExecutionScopeHash: "scope-hash-second",
		},
	}
	service := newCapturingKnowledgeBaseService(targets)
	service.search = func(id string, _ types.SearchParams) ([]*types.SearchResult, error) {
		switch id {
		case "kb-folder-first":
			close(firstStarted)
			<-secondFailureCompleted
			return nil, firstErr
		case "kb-folder-second":
			<-firstStarted
			defer close(secondFailureCompleted)
			return nil, secondErr
		default:
			return successfulKnowledgeSearchResults(id), nil
		}
	}

	result, err := executeKnowledgeSearch(t, service, targets)
	require.ErrorIs(t, err, firstErr)
	assert.False(t, errors.Is(err, secondErr))
	require.NotNil(t, result)
	assert.False(t, result.Success)
	assert.Len(t, service.callsSnapshot(), 2)
}

func TestKnowledgeSearchTool_UnscopedSearchErrorPreservesOtherSuccessfulResults(t *testing.T) {
	legacyErr := errors.New("legacy retrieval failed")
	targets := types.SearchTargets{
		{
			Type:            types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID: "kb-legacy-error",
			TenantID:        42,
			SourceTenantID:  42,
			KnowledgeIDs:    []string{"knowledge-error"},
		},
		{
			Type:            types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID: "kb-legacy-success",
			TenantID:        42,
			SourceTenantID:  42,
		},
	}
	service := newCapturingKnowledgeBaseService(targets)
	service.search = func(id string, _ types.SearchParams) ([]*types.SearchResult, error) {
		if id == "kb-legacy-error" {
			return nil, legacyErr
		}
		return successfulKnowledgeSearchResults(id), nil
	}

	result, err := executeKnowledgeSearch(t, service, targets)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Success)
	assert.Equal(t, 1, result.Data["count"])
	assert.Len(t, service.callsSnapshot(), 2)

	formattedResults, ok := result.Data["results"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, formattedResults, 1)
	assert.Equal(t, "chunk-kb-legacy-success", formattedResults[0]["chunk_id"])
}
