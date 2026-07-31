package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Fan-out helper unit tests — no service dependencies
// ---------------------------------------------------------------------------

func TestParamsWithTopK_RebuildsFresh(t *testing.T) {
	t.Parallel()

	g := &storeGroup{
		BaseParams: []types.RetrieveParams{
			{Query: "q1", TopK: 10, RetrieverType: types.VectorRetrieverType},
			{Query: "q2", TopK: 20, RetrieverType: types.KeywordsRetrieverType},
		},
		TopK: 999,
	}
	out := paramsWithTopK(g)
	require.Len(t, out, 2)

	// TopK is overridden from group.TopK.
	assert.Equal(t, 999, out[0].TopK)
	assert.Equal(t, 999, out[1].TopK)

	// BaseParams is unchanged (immutable invariant).
	assert.Equal(t, 10, g.BaseParams[0].TopK)
	assert.Equal(t, 20, g.BaseParams[1].TopK)

	// Output slice does not alias BaseParams (mutating out must not touch base).
	out[0].Query = "mutated"
	assert.Equal(t, "q1", g.BaseParams[0].Query)
}

func TestParamsWithTopK_DoesNotShareReferenceFields(t *testing.T) {
	t.Parallel()

	filter, err := types.NewResolvedFolderFilter(
		true,
		[]string{"10000000-0000-4000-8000-000000000001"},
	)
	require.NoError(t, err)
	g := &storeGroup{
		BaseParams: []types.RetrieveParams{{
			Embedding:           []float32{0.1, 0.2},
			KnowledgeBaseIDs:    []string{"kb-1"},
			KnowledgeIDs:        []string{"knowledge-1"},
			TagIDs:              []string{"tag-physical"},
			ScopeTagIDs:         []string{"tag-logical"},
			FolderFilter:        filter,
			ExcludeKnowledgeIDs: []string{"excluded-knowledge"},
			ExcludeChunkIDs:     []string{"excluded-chunk"},
			AdditionalParams:    map[string]interface{}{"mode": "strict"},
		}},
		TopK: 42,
	}

	first := paramsWithTopK(g)
	second := paramsWithTopK(g)
	require.Len(t, first, 1)
	require.Len(t, second, 1)

	first[0].Embedding[0] = 9
	first[0].KnowledgeBaseIDs[0] = "mutated-kb"
	first[0].KnowledgeIDs[0] = "mutated-knowledge"
	first[0].TagIDs[0] = "mutated-physical-tag"
	first[0].ScopeTagIDs[0] = "mutated-logical-tag"
	first[0].ExcludeKnowledgeIDs[0] = "mutated-excluded-knowledge"
	first[0].ExcludeChunkIDs[0] = "mutated-excluded-chunk"
	first[0].AdditionalParams["mode"] = "mutated"

	base := g.BaseParams[0]
	assert.Equal(t, []float32{0.1, 0.2}, base.Embedding)
	assert.Equal(t, []string{"kb-1"}, base.KnowledgeBaseIDs)
	assert.Equal(t, []string{"knowledge-1"}, base.KnowledgeIDs)
	assert.Equal(t, []string{"tag-physical"}, base.TagIDs)
	assert.Equal(t, []string{"tag-logical"}, base.ScopeTagIDs)
	assert.Equal(t, []string{"excluded-knowledge"}, base.ExcludeKnowledgeIDs)
	assert.Equal(t, []string{"excluded-chunk"}, base.ExcludeChunkIDs)
	assert.Equal(t, "strict", base.AdditionalParams["mode"])

	assert.Equal(t, []float32{0.1, 0.2}, second[0].Embedding)
	assert.Equal(t, []string{"kb-1"}, second[0].KnowledgeBaseIDs)
	assert.Equal(t, []string{"knowledge-1"}, second[0].KnowledgeIDs)
	assert.Equal(t, []string{"tag-physical"}, second[0].TagIDs)
	assert.Equal(t, []string{"tag-logical"}, second[0].ScopeTagIDs)
	assert.Equal(t, []string{"excluded-knowledge"}, second[0].ExcludeKnowledgeIDs)
	assert.Equal(t, []string{"excluded-chunk"}, second[0].ExcludeChunkIDs)
	assert.Equal(t, "strict", second[0].AdditionalParams["mode"])
	assert.Equal(
		t,
		[]string{"10000000-0000-4000-8000-000000000001"},
		second[0].FolderFilter.FolderIDs(),
	)
}

func TestBuildRetrievalParamsPreservesPreparedScopeProjection(t *testing.T) {
	t.Parallel()

	fake := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support: []types.RetrieverType{
			types.VectorRetrieverType,
			types.KeywordsRetrieverType,
		},
	}
	engine := buildBoundComposite(t, fake)
	filter, err := types.NewResolvedFolderFilter(
		true,
		[]string{"10000000-0000-4000-8000-000000000001"},
	)
	require.NoError(t, err)
	params := types.SearchParams{
		QueryText:          "prepared query",
		QueryEmbedding:     []float32{0.1, 0.2},
		VectorThreshold:    0.6,
		KeywordThreshold:   0.4,
		KnowledgeIDs:       []string{"knowledge-1"},
		TagIDs:             []string{"tag-physical"},
		ScopeTagIDs:        []string{"tag-logical"},
		SourceTenantID:     41,
		FolderFilter:       filter,
		ExecutionScopeHash: "execution-hash",
	}
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         41,
		EmbeddingModelID: "model-1",
		IndexingStrategy: types.IndexingStrategy{
			VectorEnabled:  true,
			KeywordEnabled: true,
		},
	}
	ctx := context.WithValue(
		context.Background(),
		types.TenantIDContextKey,
		uint64(7),
	)

	projected, err := (&knowledgeBaseService{}).buildRetrievalParams(
		ctx,
		engine,
		kb,
		[]*types.KnowledgeBase{kb},
		params,
		25,
	)
	require.NoError(t, err)
	require.Len(t, projected, 2)

	for _, got := range projected {
		assert.Equal(t, "prepared query", got.Query)
		assert.Equal(t, []string{"kb-1"}, got.KnowledgeBaseIDs)
		assert.Equal(t, []string{"knowledge-1"}, got.KnowledgeIDs)
		assert.Equal(t, []string{"tag-physical"}, got.TagIDs)
		assert.Equal(t, []string{"tag-logical"}, got.ScopeTagIDs)
		assert.Equal(t, uint64(41), got.SourceTenantID)
		assert.True(t, got.FolderFilter.Enabled())
		assert.Equal(
			t,
			[]string{"10000000-0000-4000-8000-000000000001"},
			got.FolderFilter.FolderIDs(),
		)
		assert.Equal(t, "execution-hash", got.ExecutionScopeHash)
		assert.Equal(t, 25, got.TopK)
	}

	projected[0].KnowledgeBaseIDs[0] = "mutated-kb"
	projected[0].KnowledgeIDs[0] = "mutated-knowledge"
	projected[0].TagIDs[0] = "mutated-physical-tag"
	projected[0].ScopeTagIDs[0] = "mutated-logical-tag"
	if len(projected[0].Embedding) > 0 {
		projected[0].Embedding[0] = 9
	}
	assert.Equal(t, []float32{0.1, 0.2}, params.QueryEmbedding)
	assert.Equal(t, []string{"knowledge-1"}, params.KnowledgeIDs)
	assert.Equal(t, []string{"tag-physical"}, params.TagIDs)
	assert.Equal(t, []string{"tag-logical"}, params.ScopeTagIDs)
}

func TestHybridSearchFolderFilterInputGuardRunsBeforeDependencies(t *testing.T) {
	t.Parallel()

	enabledEmpty, err := types.NewResolvedFolderFilter(true, nil)
	require.NoError(t, err)
	results, err := (&knowledgeBaseService{}).HybridSearch(
		context.Background(),
		"kb-1",
		types.SearchParams{FolderFilter: enabledEmpty},
	)
	require.NoError(t, err)
	assert.Empty(t, results)

	enabledNonEmpty, err := types.NewResolvedFolderFilter(
		true,
		[]string{"10000000-0000-4000-8000-000000000001"},
	)
	require.NoError(t, err)
	results, err = (&knowledgeBaseService{}).HybridSearch(
		context.Background(),
		"kb-1",
		types.SearchParams{FolderFilter: enabledNonEmpty},
	)
	assert.Nil(t, results)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok, "expected AppError, got %T", err)
	assert.Equal(t, apperrors.ErrBadRequest, appErr.Code)
	assert.Equal(t, "invalid knowledge scope", appErr.Message)
	assert.NotContains(t, appErr.Message, "10000000-0000-4000-8000-000000000001")

	results, err = (&knowledgeBaseService{}).HybridSearch(
		context.Background(),
		"kb-1",
		types.SearchParams{
			FolderFilter: enabledNonEmpty,
			KnowledgeIDs: []string{"knowledge-1"},
		},
	)
	assert.Nil(t, results)
	appErr, ok = apperrors.IsAppError(err)
	require.True(t, ok, "expected AppError, got %T", err)
	assert.Equal(t, apperrors.ErrServiceUnavailable, appErr.Code)
	assert.Equal(t, knowledgeScopeUnavailableMessage, appErr.Message)

	overLimit := make(
		[]string,
		knowledgeScopeMaxMaterializedKnowledgeIDs+1,
	)
	for index := range overLimit {
		overLimit[index] = fmt.Sprintf("knowledge-%05d", index)
	}
	results, err = (&knowledgeBaseService{}).HybridSearch(
		context.Background(),
		"kb-1",
		types.SearchParams{
			FolderFilter: enabledNonEmpty,
			KnowledgeIDs: overLimit,
		},
	)
	assert.Nil(t, results)
	appErr, ok = apperrors.IsAppError(err)
	require.True(t, ok, "expected AppError, got %T", err)
	assert.Equal(t, apperrors.ErrKnowledgeScopeTooLarge, appErr.Code)
	assert.Equal(t, 400, appErr.HTTPCode)
	assert.Equal(
		t,
		fmt.Sprintf(
			"knowledge scope exceeds the %d-file per-request limit; "+
				"select a smaller folder or reduce the selected scope",
			knowledgeScopeMaxMaterializedKnowledgeIDs,
		),
		appErr.Message,
	)
}

func TestValidateFolderSearchChunks(t *testing.T) {
	t.Parallel()

	allowed := map[string]struct{}{
		"knowledge-1": {},
		"knowledge-2": {},
	}
	require.NoError(t, validateFolderSearchChunks(
		[]*types.IndexWithScore{
			{ChunkID: "chunk-1", KnowledgeID: "knowledge-1"},
			{ChunkID: "chunk-2", KnowledgeID: "knowledge-2"},
		},
		allowed,
	))
	require.NoError(t, validateFolderSearchChunks(
		[]*types.IndexWithScore{{ChunkID: "legacy-result"}},
		nil,
	))

	testCases := []struct {
		name    string
		results []*types.IndexWithScore
	}{
		{
			name:    "nil result",
			results: []*types.IndexWithScore{nil},
		},
		{
			name: "empty knowledge id",
			results: []*types.IndexWithScore{{
				ChunkID: "chunk-empty",
			}},
		},
		{
			name: "out of scope knowledge id",
			results: []*types.IndexWithScore{{
				ChunkID:     "chunk-outside",
				KnowledgeID: "knowledge-outside",
			}},
		},
		{
			name: "mixed allowed and out of scope",
			results: []*types.IndexWithScore{
				{ChunkID: "chunk-allowed", KnowledgeID: "knowledge-1"},
				{
					ChunkID:     "chunk-outside",
					KnowledgeID: "knowledge-outside",
				},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateFolderSearchChunks(testCase.results, allowed)

			appErr, ok := apperrors.IsAppError(err)
			require.True(t, ok, "expected AppError, got %T", err)
			assert.Equal(t, apperrors.ErrInternalServer, appErr.Code)
			assert.Equal(t, "folder-scoped retrieval failed", appErr.Message)
			assert.NotContains(t, appErr.Message, "knowledge-outside")
		})
	}
}

type hybridFolderGuardKnowledgeBaseRepositoryStub struct {
	interfaces.KnowledgeBaseRepository

	knowledgeBase *types.KnowledgeBase
	getByIDsCalls atomic.Int64
}

func (s *hybridFolderGuardKnowledgeBaseRepositoryStub) GetKnowledgeBaseByIDs(
	context.Context,
	[]string,
) ([]*types.KnowledgeBase, error) {
	s.getByIDsCalls.Add(1)
	return []*types.KnowledgeBase{s.knowledgeBase}, nil
}

type hybridFolderGuardKnowledgeRepositoryStub struct {
	interfaces.KnowledgeRepository

	knowledge     *types.Knowledge
	getBatchCalls atomic.Int64
}

func (s *hybridFolderGuardKnowledgeRepositoryStub) GetKnowledgeBatch(
	_ context.Context,
	_ uint64,
	ids []string,
) ([]*types.Knowledge, error) {
	s.getBatchCalls.Add(1)
	if len(ids) == 1 && ids[0] == s.knowledge.ID {
		return []*types.Knowledge{s.knowledge}, nil
	}
	return nil, nil
}

type hybridFolderGuardChunkRepositoryStub struct {
	interfaces.ChunkRepository

	chunk                *types.Chunk
	listByIDCalls        atomic.Int64
	listByIDOnlyCalls    atomic.Int64
	listByParentIDsCalls atomic.Int64
}

func (s *hybridFolderGuardChunkRepositoryStub) ListChunksByID(
	_ context.Context,
	_ uint64,
	ids []string,
) ([]*types.Chunk, error) {
	s.listByIDCalls.Add(1)
	if len(ids) == 1 && ids[0] == s.chunk.ID {
		return []*types.Chunk{s.chunk}, nil
	}
	return nil, nil
}

func (s *hybridFolderGuardChunkRepositoryStub) ListChunksByIDOnly(
	context.Context,
	[]string,
) ([]*types.Chunk, error) {
	s.listByIDOnlyCalls.Add(1)
	return nil, nil
}

func (s *hybridFolderGuardChunkRepositoryStub) ListChunksByParentIDs(
	context.Context,
	uint64,
	[]string,
) ([]*types.Chunk, error) {
	s.listByParentIDsCalls.Add(1)
	return nil, nil
}

func TestHybridSearchFolderOutputGuardRunsBeforeResultHydration(
	t *testing.T,
) {
	filter, err := types.NewResolvedFolderFilter(
		true,
		[]string{"10000000-0000-4000-8000-000000000001"},
	)
	require.NoError(t, err)
	testCases := []struct {
		name                 string
		retrievedKnowledgeID string
		retrievedChunkID     string
		wantScopeError       bool
		wantHydrationDBCalls int64
	}{
		{
			name:                 "allowed result is retained",
			retrievedKnowledgeID: "knowledge-a",
			retrievedChunkID:     "chunk-a",
			wantHydrationDBCalls: 1,
		},
		{
			name:                 "out of scope result is rejected",
			retrievedKnowledgeID: "knowledge-b",
			retrievedChunkID:     "chunk-b",
			wantScopeError:       true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			const storeID = "00000000-0000-0000-0000-000000000001"
			storeIDValue := storeID
			knowledgeBase := &types.KnowledgeBase{
				ID:               "kb-1",
				Type:             types.KnowledgeBaseTypeDocument,
				TenantID:         41,
				EmbeddingModelID: "model-1",
				VectorStoreID:    &storeIDValue,
				IndexingStrategy: types.IndexingStrategy{
					VectorEnabled: true,
				},
			}
			kbRepo := &hybridFolderGuardKnowledgeBaseRepositoryStub{
				knowledgeBase: knowledgeBase,
			}
			knowledgeRepo := &hybridFolderGuardKnowledgeRepositoryStub{
				knowledge: &types.Knowledge{
					ID:              "knowledge-a",
					TenantID:        41,
					KnowledgeBaseID: "kb-1",
					Title:           "allowed document",
				},
			}
			chunkRepo := &hybridFolderGuardChunkRepositoryStub{
				chunk: &types.Chunk{
					ID:              "chunk-a",
					TenantID:        41,
					KnowledgeID:     "knowledge-a",
					KnowledgeBaseID: "kb-1",
					Content:         "allowed content",
					ChunkType:       types.ChunkTypeText,
					ImageInfo:       "[]",
				},
			}
			engine := &fakeRetrieveEngineService{
				engineType: types.PostgresRetrieverEngineType,
				support:    []types.RetrieverType{types.VectorRetrieverType},
				canned: []*types.IndexWithScore{{
					Content:         "retrieved content",
					ChunkID:         testCase.retrievedChunkID,
					KnowledgeID:     testCase.retrievedKnowledgeID,
					KnowledgeBaseID: "kb-1",
					Score:           0.9,
					MatchType:       types.MatchTypeEmbedding,
				}},
			}
			service := &knowledgeBaseService{
				repo:      kbRepo,
				kgRepo:    knowledgeRepo,
				chunkRepo: chunkRepo,
				retrieveEngine: &fakeFanoutRegistry{
					byStore: map[string]interfaces.RetrieveEngineService{
						storeID: engine,
					},
				},
				ownership: &fakeOwnership{
					owned: map[string]uint64{storeID: 41},
				},
			}
			ctx := context.WithValue(
				context.Background(),
				types.TenantIDContextKey,
				uint64(41),
			)

			results, err := service.HybridSearch(
				ctx,
				"kb-1",
				types.SearchParams{
					QueryText:             "folder query",
					QueryEmbedding:        []float32{0.1},
					KnowledgeIDs:          []string{"knowledge-a"},
					SourceTenantID:        41,
					FolderFilter:          filter,
					ExecutionScopeHash:    "0123456789abcdef",
					MatchCount:            1,
					DisableKeywordsMatch:  true,
					SkipContextEnrichment: true,
				},
			)

			assert.Equal(t, int64(1), engine.retrieveCalls.Load())
			assert.Equal(t, int64(1), kbRepo.getByIDsCalls.Load())
			assert.Equal(
				t,
				testCase.wantHydrationDBCalls,
				knowledgeRepo.getBatchCalls.Load(),
			)
			assert.Equal(
				t,
				testCase.wantHydrationDBCalls,
				chunkRepo.listByIDCalls.Load(),
			)
			assert.Equal(t, int64(0), chunkRepo.listByIDOnlyCalls.Load())
			assert.Equal(t, int64(0), chunkRepo.listByParentIDsCalls.Load())

			if !testCase.wantScopeError {
				require.NoError(t, err)
				require.Len(t, results, 1)
				assert.Equal(t, "knowledge-a", results[0].KnowledgeID)
				return
			}

			require.Error(t, err)
			assert.Nil(t, results)
			appErr, ok := apperrors.IsAppError(err)
			require.True(t, ok, "expected AppError, got %T", err)
			assert.Equal(t, apperrors.ErrInternalServer, appErr.Code)
			assert.Equal(t, 500, appErr.HTTPCode)
			assert.Equal(
				t,
				"folder-scoped retrieval failed",
				appErr.Message,
			)
			assert.NotContains(t, appErr.Message, "knowledge-b")
		})
	}
}

func TestHasMixedEngineTypes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []*types.RetrieveResult
		want bool
	}{
		{"empty", nil, false},
		{"single", []*types.RetrieveResult{
			{RetrieverEngineType: types.PostgresRetrieverEngineType},
		}, false},
		{"two same", []*types.RetrieveResult{
			{RetrieverEngineType: types.PostgresRetrieverEngineType},
			{RetrieverEngineType: types.PostgresRetrieverEngineType},
		}, false},
		{"two different", []*types.RetrieveResult{
			{RetrieverEngineType: types.PostgresRetrieverEngineType},
			{RetrieverEngineType: types.ElasticsearchRetrieverEngineType},
		}, true},
		{"empty + nonempty engine", []*types.RetrieveResult{
			{RetrieverEngineType: ""},
			{RetrieverEngineType: types.PostgresRetrieverEngineType},
		}, true},
	}
	for _, tc := range cases {
		got := hasMixedEngineTypes(tc.in)
		if got != tc.want {
			t.Errorf("%s: want %v, got %v", tc.name, tc.want, got)
		}
	}
}

func TestIsKnownEngineType(t *testing.T) {
	t.Parallel()
	known := []types.RetrieverEngineType{
		types.PostgresRetrieverEngineType,
		types.ElasticsearchRetrieverEngineType,
		types.ElasticFaissRetrieverEngineType,
		types.MilvusRetrieverEngineType,
		types.QdrantRetrieverEngineType,
		types.WeaviateRetrieverEngineType,
		types.SQLiteRetrieverEngineType,
		types.InfinityRetrieverEngineType,
		types.TencentVectorDBRetrieverEngineType,
		types.DorisRetrieverEngineType,
	}
	for _, k := range known {
		if !isKnownEngineType(k) {
			t.Errorf("expected %s to be known", k)
		}
	}
	if isKnownEngineType("") || isKnownEngineType("nosuch") {
		t.Error("unknown engine misclassified as known")
	}
}

func TestStoreKindLabel(t *testing.T) {
	t.Parallel()
	if storeKindLabel("") != "env" {
		t.Errorf("empty storeID should be env")
	}
	if storeKindLabel("0193b8a0-1111-7000-8000-000000000001") != "bound" {
		t.Errorf("non-empty storeID should be bound")
	}
}

func TestMultiStoreRetrieveTimeout(t *testing.T) {
	// Not Parallel — mutates a process-global env var.
	t.Setenv("MULTI_STORE_RETRIEVE_TIMEOUT_SEC", "")
	os.Unsetenv("MULTI_STORE_RETRIEVE_TIMEOUT_SEC")
	if got := multiStoreRetrieveTimeout(); got != defaultMultiStoreRetrieveTimeout {
		t.Errorf("default: want %v, got %v", defaultMultiStoreRetrieveTimeout, got)
	}

	t.Setenv("MULTI_STORE_RETRIEVE_TIMEOUT_SEC", "7")
	if got := multiStoreRetrieveTimeout(); got != 7*time.Second {
		t.Errorf("env=7: want 7s, got %v", got)
	}

	t.Setenv("MULTI_STORE_RETRIEVE_TIMEOUT_SEC", "garbage")
	if got := multiStoreRetrieveTimeout(); got != defaultMultiStoreRetrieveTimeout {
		t.Errorf("parse-fail fallback: want default, got %v", got)
	}

	t.Setenv("MULTI_STORE_RETRIEVE_TIMEOUT_SEC", "-3")
	if got := multiStoreRetrieveTimeout(); got != defaultMultiStoreRetrieveTimeout {
		t.Errorf("negative fallback: want default, got %v", got)
	}
}

func TestPickPrimary(t *testing.T) {
	t.Parallel()
	kbs := []*types.KnowledgeBase{
		{ID: "kb-a"}, {ID: "kb-b"}, {ID: "kb-c"},
	}
	if pickPrimary(kbs, "kb-b").ID != "kb-b" {
		t.Errorf("hit case failed")
	}
	// Miss returns nil rather than falling back to kbs[0]; the helper's
	// godoc explains the rationale (surface caller bugs, avoid leaking
	// unintended KB metadata).
	if pickPrimary(kbs, "kb-missing") != nil {
		t.Errorf("miss should return nil")
	}
	if pickPrimary(nil, "any") != nil {
		t.Errorf("nil slice should return nil")
	}
}

func TestAllBaseParamsEmpty(t *testing.T) {
	t.Parallel()
	empty := []*storeGroup{{}, {BaseParams: nil}}
	if !allBaseParamsEmpty(empty) {
		t.Errorf("all-empty case failed")
	}
	hasSome := []*storeGroup{{BaseParams: nil}, {BaseParams: []types.RetrieveParams{{Query: "x"}}}}
	if allBaseParamsEmpty(hasSome) {
		t.Errorf("partial-nonempty case must return false")
	}
}

func TestTotalHits(t *testing.T) {
	t.Parallel()
	got := totalHits([]*types.RetrieveResult{
		{Results: []*types.IndexWithScore{{}, {}}},
		{Results: []*types.IndexWithScore{{}}},
		{Results: nil},
	})
	if got != 3 {
		t.Errorf("want 3, got %d", got)
	}
	if totalHits(nil) != 0 {
		t.Errorf("nil → 0")
	}
}

// ---------------------------------------------------------------------------
// classifyFactoryError — sentinel→typed AppError mapping
// ---------------------------------------------------------------------------

func TestClassifyFactoryError_ForbiddenMapsTo2200(t *testing.T) {
	t.Parallel()
	err := classifyFactoryError(context.Background(),
		retriever.ErrVectorStoreForbidden, 99, "0193-store")
	app, ok := apperrors.IsAppError(err)
	require.True(t, ok, "expected AppError, got %T", err)
	assert.Equal(t, apperrors.ErrVectorStoreBindingInvalid, app.Code)
	// User-facing message MUST NOT echo the store UUID.
	if strings.Contains(app.Message, "0193-store") {
		t.Fatalf("AppError message leaked store UUID: %q", app.Message)
	}
}

func TestClassifyFactoryError_NotFoundMapsTo2201(t *testing.T) {
	t.Parallel()
	err := classifyFactoryError(context.Background(),
		retriever.ErrVectorStoreNotFound, 7, "store-id-x")
	app, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrVectorStoreUnavailable, app.Code)
	if strings.Contains(app.Message, "store-id-x") {
		t.Fatalf("AppError message leaked store UUID: %q", app.Message)
	}
}

func TestClassifyFactoryError_TenantInfoMissingMaps(t *testing.T) {
	t.Parallel()
	err := classifyFactoryError(context.Background(),
		retriever.ErrTenantInfoMissing, 0, "")
	app, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrVectorStoreBindingInvalid, app.Code)
}

func TestClassifyFactoryError_GenericErrorPassesThrough(t *testing.T) {
	t.Parallel()
	raw := stderrors.New("generic infra failure")
	err := classifyFactoryError(context.Background(), raw, 1, "x")
	// Generic errors are returned as-is; the handler decides how to wrap.
	if !stderrors.Is(err, raw) {
		t.Fatalf("expected raw error passthrough, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// validateSameEmbeddingModel — uses a narrowly faked model service
// ---------------------------------------------------------------------------

// fakeModelSvcForKeys is the smallest ModelService subset needed by
// ResolveEmbeddingModelKeys → validateSameEmbeddingModel.
//
// ResolveEmbeddingModelKeys calls GetModelByID(ctx, modelID); the rest are
// panic-only — none are exercised by these tests.
type fakeModelSvcForKeys struct {
	byID map[string]*types.Model // modelID → model
	interfaces.ModelService
}

func (f *fakeModelSvcForKeys) GetModelByID(ctx context.Context, id string) (*types.Model, error) {
	if m, ok := f.byID[id]; ok {
		return m, nil
	}
	return nil, fmt.Errorf("model %s not found", id)
}

func buildModel(id, name, baseURL string) *types.Model {
	return &types.Model{
		ID:   id,
		Name: name,
		Parameters: types.ModelParameters{
			BaseURL: baseURL,
		},
	}
}

func newKBSvcForValidate(modelService interfaces.ModelService) *knowledgeBaseService {
	return &knowledgeBaseService{
		modelService: modelService,
	}
}

func TestValidateSameEmbeddingModel_SingleKB_NoOp(t *testing.T) {
	t.Parallel()
	svc := newKBSvcForValidate(&fakeModelSvcForKeys{})
	err := svc.validateSameEmbeddingModel(context.Background(), []*types.KnowledgeBase{
		{ID: "kb-1", EmbeddingModelID: "m-1", TenantID: 1},
	})
	assert.NoError(t, err)
}

func TestValidateSameEmbeddingModel_SameIdentity_OK(t *testing.T) {
	t.Parallel()
	models := map[string]*types.Model{
		"m-1": buildModel("m-1", "bge-m3", "http://embed-a"),
		"m-2": buildModel("m-2", "bge-m3", "http://embed-a"), // same identity key
	}
	svc := newKBSvcForValidate(&fakeModelSvcForKeys{byID: models})
	err := svc.validateSameEmbeddingModel(context.Background(), []*types.KnowledgeBase{
		{ID: "kb-1", EmbeddingModelID: "m-1", TenantID: 1},
		{ID: "kb-2", EmbeddingModelID: "m-2", TenantID: 2}, // cross-tenant
	})
	assert.NoError(t, err)
}

func TestValidateSameEmbeddingModel_DifferentIdentity_400(t *testing.T) {
	t.Parallel()
	models := map[string]*types.Model{
		"m-1": buildModel("m-1", "bge-m3", "http://embed-a"),
		"m-2": buildModel("m-2", "openai-3", "http://embed-b"), // different
	}
	svc := newKBSvcForValidate(&fakeModelSvcForKeys{byID: models})
	err := svc.validateSameEmbeddingModel(context.Background(), []*types.KnowledgeBase{
		{ID: "kb-1", EmbeddingModelID: "m-1", TenantID: 1},
		{ID: "kb-2", EmbeddingModelID: "m-2", TenantID: 1},
	})
	app, ok := apperrors.IsAppError(err)
	require.True(t, ok, "expected AppError, got %T", err)
	assert.Equal(t, apperrors.ErrBadRequest, app.Code)
}

func TestValidateSameEmbeddingModel_WikiOnly_Tolerated(t *testing.T) {
	t.Parallel()
	models := map[string]*types.Model{
		"m-1": buildModel("m-1", "bge-m3", "http://embed-a"),
	}
	svc := newKBSvcForValidate(&fakeModelSvcForKeys{byID: models})
	// One vector KB + one wiki-only (empty EmbeddingModelID) → tolerated.
	err := svc.validateSameEmbeddingModel(context.Background(), []*types.KnowledgeBase{
		{ID: "kb-vec", EmbeddingModelID: "m-1", TenantID: 1},
		{ID: "kb-wiki", EmbeddingModelID: "", TenantID: 1},
	})
	assert.NoError(t, err)
}

func TestValidateSameEmbeddingModel_LogInjection_Sanitized(t *testing.T) {
	t.Parallel()
	// BaseURL with CRLF — SanitizeForLog should keep us safe.
	models := map[string]*types.Model{
		"m-1": buildModel("m-1", "bge-m3", "http://a\r\nfoo"),
		"m-2": buildModel("m-2", "openai", "http://b\r\nbar"),
	}
	svc := newKBSvcForValidate(&fakeModelSvcForKeys{byID: models})
	err := svc.validateSameEmbeddingModel(context.Background(), []*types.KnowledgeBase{
		{ID: "kb-1", EmbeddingModelID: "m-1", TenantID: 1},
		{ID: "kb-2", EmbeddingModelID: "m-2", TenantID: 1},
	})
	// We only verify the 400 still happens; sanitize is exercised in the
	// hot path of WarnWithFields and is unit-tested in internal/utils.
	app, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrBadRequest, app.Code)
}

// ---------------------------------------------------------------------------
// retrieveFromStores fan-out behavior
//
// We construct CompositeRetrieveEngines through the PR2 factory so we
// exercise the real wiring rather than reaching into unexported fields.
// ---------------------------------------------------------------------------

// fakeRetrieveEngineService implements interfaces.RetrieveEngineService. It
// returns canned RetrieveResults, optionally sleeps to simulate a hung
// engine, or returns an error. Only Retrieve, EngineType, and Support are
// exercised; the rest panic.
type fakeRetrieveEngineService struct {
	engineType    types.RetrieverEngineType
	support       []types.RetrieverType
	canned        []*types.IndexWithScore
	cannedErr     error
	sleep         time.Duration
	retrieveCalls atomic.Int64
}

func (f *fakeRetrieveEngineService) EngineType() types.RetrieverEngineType {
	return f.engineType
}

func (f *fakeRetrieveEngineService) Support() []types.RetrieverType { return f.support }

func (f *fakeRetrieveEngineService) Retrieve(ctx context.Context, p types.RetrieveParams) ([]*types.RetrieveResult, error) {
	f.retrieveCalls.Add(1)
	if f.sleep > 0 {
		select {
		case <-time.After(f.sleep):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.cannedErr != nil {
		return nil, f.cannedErr
	}
	return []*types.RetrieveResult{{
		Results:             f.canned,
		RetrieverEngineType: f.engineType,
		RetrieverType:       p.RetrieverType,
	}}, nil
}

func (f *fakeRetrieveEngineService) Index(context.Context, embedding.Embedder, *types.IndexInfo, []types.RetrieverType) error {
	panic("unused")
}
func (f *fakeRetrieveEngineService) BatchIndex(context.Context, embedding.Embedder, []*types.IndexInfo, []types.RetrieverType) error {
	panic("unused")
}
func (f *fakeRetrieveEngineService) EstimateStorageSize(context.Context, embedding.Embedder, []*types.IndexInfo, []types.RetrieverType) int64 {
	panic("unused")
}
func (f *fakeRetrieveEngineService) CopyIndices(context.Context, string, map[string]string, map[string]string, string, int, string) error {
	panic("unused")
}
func (f *fakeRetrieveEngineService) DeleteByChunkIDList(context.Context, []string, int, string) error {
	panic("unused")
}
func (f *fakeRetrieveEngineService) DeleteBySourceIDList(context.Context, []string, int, string) error {
	panic("unused")
}
func (f *fakeRetrieveEngineService) DeleteByKnowledgeIDList(context.Context, []string, int, string) error {
	panic("unused")
}
func (f *fakeRetrieveEngineService) BatchUpdateChunkEnabledStatus(context.Context, map[string]bool) error {
	panic("unused")
}
func (f *fakeRetrieveEngineService) BatchUpdateChunkTagID(context.Context, map[string]string) error {
	panic("unused")
}

var _ interfaces.RetrieveEngineService = (*fakeRetrieveEngineService)(nil)

// fakeFanoutRegistry returns non-nil RetrieveEngineService instances so
// that resolveBoundEngine produces a real CompositeRetrieveEngine for
// fan-out tests. The simpler fakeRegistry used by other test files
// returns (nil, nil) from GetByStoreID, which is only useful for tests
// that never reach the composite construction step.
type fakeFanoutRegistry struct {
	byStore map[string]interfaces.RetrieveEngineService
}

func (r *fakeFanoutRegistry) Register(interfaces.RetrieveEngineService) error { return nil }
func (r *fakeFanoutRegistry) GetRetrieveEngineService(types.RetrieverEngineType) (interfaces.RetrieveEngineService, error) {
	return nil, stderrors.New("not used in fan-out tests")
}
func (r *fakeFanoutRegistry) GetAllRetrieveEngineServices() []interfaces.RetrieveEngineService {
	return nil
}
func (r *fakeFanoutRegistry) GetByStoreID(id string) (interfaces.RetrieveEngineService, error) {
	if svc, ok := r.byStore[id]; ok {
		return svc, nil
	}
	return nil, stderrors.New("store not registered")
}

func buildBoundComposite(t *testing.T, svc interfaces.RetrieveEngineService) *retriever.CompositeRetrieveEngine {
	t.Helper()
	const storeID = "00000000-0000-0000-0000-000000000001"
	reg := &fakeFanoutRegistry{byStore: map[string]interfaces.RetrieveEngineService{storeID: svc}}
	own := &fakeOwnership{owned: map[string]uint64{storeID: 1}}
	sid := storeID
	composite, err := retriever.CreateRetrieveEngineForKB(
		context.Background(), reg, own, 1, &sid)
	require.NoError(t, err)
	return composite
}

func vectorParams(query string) []types.RetrieveParams {
	return []types.RetrieveParams{{
		Query:         query,
		TopK:          50,
		RetrieverType: types.VectorRetrieverType,
	}}
}

func TestRetrieveFromStores_Empty(t *testing.T) {
	t.Parallel()
	res, err := (&knowledgeBaseService{}).retrieveFromStores(
		context.Background(), nil, retriever.EngineAwareNormalizer{})
	assert.NoError(t, err)
	assert.Nil(t, res)
}

func TestRetrieveFromStores_SingleGroupFastPath(t *testing.T) {
	t.Parallel()
	fake := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		canned: []*types.IndexWithScore{
			{ChunkID: "c1", Score: 0.8},
		},
	}
	composite := buildBoundComposite(t, fake)
	g := &storeGroup{
		StoreID:    "store-x",
		KBIDs:      []string{"kb-1"},
		Engine:     composite,
		BaseParams: vectorParams("q"),
		TopK:       50,
	}

	s := &knowledgeBaseService{}
	res, err := s.retrieveFromStores(context.Background(),
		[]*storeGroup{g}, retriever.EngineAwareNormalizer{})
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, types.PostgresRetrieverEngineType, res[0].RetrieverEngineType)
	assert.Equal(t, 1, int(fake.retrieveCalls.Load()))

	// Single-group raw scores must NOT be normalized (passthrough native scale).
	if len(res[0].Results) > 0 {
		assert.InDelta(t, 0.8, res[0].Results[0].Score, 1e-9)
	}
}

func TestRetrieveFromStores_SingleGroupFailureIsSafelyMapped(t *testing.T) {
	t.Parallel()

	sensitiveMarker := "sqlite secret path / KB-ID-secret"
	backendErr := stderrors.New(sensitiveMarker)
	fake := &fakeRetrieveEngineService{
		engineType: types.SQLiteRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		cannedErr:  backendErr,
	}
	group := &storeGroup{
		StoreID:    "store-secret",
		KBIDs:      []string{"kb-secret"},
		Engine:     buildBoundComposite(t, fake),
		BaseParams: vectorParams("q"),
		TopK:       50,
	}

	results, err := (&knowledgeBaseService{}).retrieveFromStores(
		context.Background(),
		[]*storeGroup{group},
		retriever.EngineAwareNormalizer{},
	)

	require.Error(t, err)
	require.Nil(t, results)
	require.False(t, stderrors.Is(err, backendErr))
	require.Equal(t, int64(1), fake.retrieveCalls.Load())

	app, ok := apperrors.IsAppError(err)
	require.True(t, ok, "expected typed AppError, got %T", err)
	require.Equal(t, apperrors.ErrVectorStoreUnavailable, app.Code)
	require.NotContains(t, err.Error(), sensitiveMarker)
	require.NotContains(t, app.Message, sensitiveMarker)
	require.NotContains(t, fmt.Sprint(app.Details), sensitiveMarker)
}

func TestRetrieveFromStores_SingleGroupCanceledContextIsPreserved(t *testing.T) {
	t.Parallel()

	fake := &fakeRetrieveEngineService{
		engineType: types.SQLiteRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		cannedErr:  stderrors.New("backend error"),
	}
	group := &storeGroup{
		Engine:     buildBoundComposite(t, fake),
		BaseParams: vectorParams("q"),
		TopK:       50,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results, err := (&knowledgeBaseService{}).retrieveFromStores(
		ctx,
		[]*storeGroup{group},
		retriever.EngineAwareNormalizer{},
	)

	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, results)
	require.Equal(t, int64(1), fake.retrieveCalls.Load())
}

func TestRetrieveFromStores_SingleGroupDeadlineMapping(t *testing.T) {
	tests := []struct {
		name               string
		executionScopeHash string
		wantDeadline       bool
	}{
		{
			name:         "generic scope maps deadline to unavailable",
			wantDeadline: false,
		},
		{
			name:               "prepared scope preserves deadline",
			executionScopeHash: "prepared-scope-hash",
			wantDeadline:       true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeRetrieveEngineService{
				engineType: types.SQLiteRetrieverEngineType,
				support:    []types.RetrieverType{types.VectorRetrieverType},
				cannedErr:  stderrors.New("backend error"),
			}
			params := vectorParams("q")
			params[0].ExecutionScopeHash = test.executionScopeHash
			group := &storeGroup{
				Engine:     buildBoundComposite(t, fake),
				BaseParams: params,
				TopK:       50,
			}
			ctx, cancel := context.WithDeadline(context.Background(), time.Time{})
			defer cancel()

			results, err := (&knowledgeBaseService{}).retrieveFromStores(
				ctx,
				[]*storeGroup{group},
				retriever.EngineAwareNormalizer{},
			)

			require.Error(t, err)
			require.Nil(t, results)
			if test.wantDeadline {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				return
			}
			app, ok := apperrors.IsAppError(err)
			require.True(t, ok, "expected typed AppError, got %T", err)
			require.Equal(t, apperrors.ErrVectorStoreUnavailable, app.Code)
		})
	}
}

func TestRetrieveFromStores_MultiGroupParallel_Concat(t *testing.T) {
	t.Parallel()
	fakeA := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		canned:     []*types.IndexWithScore{{ChunkID: "a1", Score: 0.6}},
	}
	fakeB := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType, // same engine type
		support:    []types.RetrieverType{types.VectorRetrieverType},
		canned:     []*types.IndexWithScore{{ChunkID: "b1", Score: 0.4}},
	}
	groups := []*storeGroup{
		{Engine: buildBoundComposite(t, fakeA), BaseParams: vectorParams("q"), TopK: 50, KBIDs: []string{"kb-a"}},
		{Engine: buildBoundComposite(t, fakeB), BaseParams: vectorParams("q"), TopK: 50, KBIDs: []string{"kb-b"}},
	}

	s := &knowledgeBaseService{}
	res, err := s.retrieveFromStores(context.Background(),
		groups, retriever.EngineAwareNormalizer{})
	require.NoError(t, err)

	// Combined hit count = 2 (a1 + b1).
	gotIDs := map[string]bool{}
	for _, rr := range res {
		for _, hit := range rr.Results {
			gotIDs[hit.ChunkID] = true
		}
	}
	assert.True(t, gotIDs["a1"])
	assert.True(t, gotIDs["b1"])

	// Same engine type across groups → normalizer NOT applied; raw scores kept.
	scoresByChunk := map[string]float64{}
	for _, rr := range res {
		for _, hit := range rr.Results {
			scoresByChunk[hit.ChunkID] = hit.Score
		}
	}
	assert.InDelta(t, 0.6, scoresByChunk["a1"], 1e-9)
	assert.InDelta(t, 0.4, scoresByChunk["b1"], 1e-9)
}

func TestRetrieveFromStores_MixedEngine_Normalizes(t *testing.T) {
	t.Parallel()
	// EngineAwareNormalizer policy in effect:
	//   - ES / ElasticFaiss / OpenSearch / Weaviate / Postgres / SQLite /
	//     Qdrant / TencentVectorDB / Doris surface non-negative cosine in
	//     [0, 1] when the value reaches the normalizer (Lucene script_score
	//     non-negative invariant for ES; k-NN plugin SpaceType.COSINESIMIL
	//     pre-translation for OpenSearch; engine-internal conversions for
	//     the rest) → passthrough via clamp01.
	//   - Milvus is the only engine in this codebase that still surfaces
	//     the raw signed cosine in [-1, 1] → cosine-shift via (score + 1) / 2.
	//
	// First sub-case below pins the ES passthrough; the Milvus sub-case
	// pins the cosine-shift path so the [-1, 1] branch stays under coverage.
	fakeES := &fakeRetrieveEngineService{
		engineType: types.ElasticsearchRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		canned:     []*types.IndexWithScore{{ChunkID: "es1", Score: 1.0}}, // top cosine
	}
	fakePG := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		canned:     []*types.IndexWithScore{{ChunkID: "pg1", Score: 0.9}},
	}
	groups := []*storeGroup{
		{Engine: buildBoundComposite(t, fakeES), BaseParams: vectorParams("q"), TopK: 50, KBIDs: []string{"kb-es"}},
		{Engine: buildBoundComposite(t, fakePG), BaseParams: vectorParams("q"), TopK: 50, KBIDs: []string{"kb-pg"}},
	}

	s := &knowledgeBaseService{}
	res, err := s.retrieveFromStores(context.Background(),
		groups, retriever.EngineAwareNormalizer{})
	require.NoError(t, err)

	scoresByChunk := map[string]float64{}
	for _, rr := range res {
		for _, hit := range rr.Results {
			scoresByChunk[hit.ChunkID] = hit.Score
		}
	}
	// ES 1.0 → clamp01(1.0) = 1.0 (passthrough); PG 0.9 → 0.9 unchanged.
	assert.InDelta(t, 1.0, scoresByChunk["es1"], 1e-9)
	assert.InDelta(t, 0.9, scoresByChunk["pg1"], 1e-9)

	// ES passthrough on a production-possible mid-range cosine: 0.3 → 0.3
	// (below PG 0.8, so PG out-ranks ES — the property the old cosine-shift
	// test asserted, restated for the passthrough policy).
	fakeES2 := &fakeRetrieveEngineService{
		engineType: types.ElasticsearchRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		canned:     []*types.IndexWithScore{{ChunkID: "es2", Score: 0.3}},
	}
	fakePG2 := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		canned:     []*types.IndexWithScore{{ChunkID: "pg2", Score: 0.8}},
	}
	groups2 := []*storeGroup{
		{Engine: buildBoundComposite(t, fakeES2), BaseParams: vectorParams("q"), TopK: 50, KBIDs: []string{"kb-es2"}},
		{Engine: buildBoundComposite(t, fakePG2), BaseParams: vectorParams("q"), TopK: 50, KBIDs: []string{"kb-pg2"}},
	}
	res2, err := s.retrieveFromStores(context.Background(), groups2, retriever.EngineAwareNormalizer{})
	require.NoError(t, err)
	scoresByChunk2 := map[string]float64{}
	for _, rr := range res2 {
		for _, hit := range rr.Results {
			scoresByChunk2[hit.ChunkID] = hit.Score
		}
	}
	assert.InDelta(t, 0.3, scoresByChunk2["es2"], 1e-9)
	assert.InDelta(t, 0.8, scoresByChunk2["pg2"], 1e-9)

	// Milvus cosine-shift coverage: raw -0.4 → (−0.4 + 1) / 2 = 0.3.
	// Milvus is now the only engine in this switch that still uses the
	// signed-cosine branch; without this case the [-1, 1] arm would be
	// uncovered by the mixed-engine integration test.
	fakeMilvus := &fakeRetrieveEngineService{
		engineType: types.MilvusRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		canned:     []*types.IndexWithScore{{ChunkID: "mv1", Score: -0.4}},
	}
	fakePG3 := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		canned:     []*types.IndexWithScore{{ChunkID: "pg3", Score: 0.8}},
	}
	groups3 := []*storeGroup{
		{Engine: buildBoundComposite(t, fakeMilvus), BaseParams: vectorParams("q"), TopK: 50, KBIDs: []string{"kb-mv"}},
		{Engine: buildBoundComposite(t, fakePG3), BaseParams: vectorParams("q"), TopK: 50, KBIDs: []string{"kb-pg3"}},
	}
	res3, err := s.retrieveFromStores(context.Background(), groups3, retriever.EngineAwareNormalizer{})
	require.NoError(t, err)
	scoresByChunk3 := map[string]float64{}
	for _, rr := range res3 {
		for _, hit := range rr.Results {
			scoresByChunk3[hit.ChunkID] = hit.Score
		}
	}
	assert.InDelta(t, 0.3, scoresByChunk3["mv1"], 1e-9)
	assert.InDelta(t, 0.8, scoresByChunk3["pg3"], 1e-9)
}

func TestRetrieveFromStores_KeywordPassthroughOnMixed(t *testing.T) {
	t.Parallel()
	// One ES vector group + one PG keyword group. Even with mixed engine
	// types, the keyword score is NOT rescaled (BM25 unbounded — RRF
	// fusion handles via rank).
	fakeES := &fakeRetrieveEngineService{
		engineType: types.ElasticsearchRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		canned:     []*types.IndexWithScore{{ChunkID: "v1", Score: 1.0}},
	}
	fakePGKW := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.KeywordsRetrieverType},
		canned:     []*types.IndexWithScore{{ChunkID: "k1", Score: 14.3}}, // BM25
	}
	groups := []*storeGroup{
		{Engine: buildBoundComposite(t, fakeES), BaseParams: vectorParams("q"), TopK: 50, KBIDs: []string{"kb-es"}},
		{Engine: buildBoundComposite(t, fakePGKW), BaseParams: []types.RetrieveParams{{Query: "q", TopK: 50, RetrieverType: types.KeywordsRetrieverType}}, TopK: 50, KBIDs: []string{"kb-pg"}},
	}
	s := &knowledgeBaseService{}
	res, err := s.retrieveFromStores(context.Background(), groups, retriever.EngineAwareNormalizer{})
	require.NoError(t, err)

	scoresByChunk := map[string]float64{}
	for _, rr := range res {
		for _, hit := range rr.Results {
			scoresByChunk[hit.ChunkID] = hit.Score
		}
	}
	assert.InDelta(t, 1.0, scoresByChunk["v1"], 1e-9)
	// Keyword score MUST survive untouched.
	assert.InDelta(t, 14.3, scoresByChunk["k1"], 1e-9)
}

func TestRetrieveFromStores_OneGroupFails_AllFail(t *testing.T) {
	t.Parallel()
	sensitiveMarker := "sqlite secret path / KB-ID-secret"

	fakeOK := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		canned:     []*types.IndexWithScore{{ChunkID: "ok1", Score: 0.5}},
	}
	fakeBad := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		cannedErr:  stderrors.New(sensitiveMarker),
	}
	groups := []*storeGroup{
		{Engine: buildBoundComposite(t, fakeOK), BaseParams: vectorParams("q"), TopK: 50, KBIDs: []string{"kb-ok"}},
		{Engine: buildBoundComposite(t, fakeBad), BaseParams: vectorParams("q"), TopK: 50, KBIDs: []string{"kb-bad"}},
	}
	s := &knowledgeBaseService{}
	results, err := s.retrieveFromStores(context.Background(), groups, retriever.EngineAwareNormalizer{})
	require.Error(t, err)
	require.Nil(t, results)

	// Generic infra failure → 2201 (Unavailable). Message MUST NOT echo
	// the raw error or any store UUID.
	app, ok := apperrors.IsAppError(err)
	require.True(t, ok, "expected typed AppError, got %T", err)
	assert.Equal(t, apperrors.ErrVectorStoreUnavailable, app.Code)
	if strings.Contains(app.Message, sensitiveMarker) {
		t.Fatalf("AppError message leaked raw err string: %q", app.Message)
	}
	require.NotContains(t, fmt.Sprint(app.Details), sensitiveMarker)
}

func TestRetrieveFromStores_PerGroupTimeout(t *testing.T) {
	// Not Parallel — mutates env. t.Setenv restores cleanly after the test.
	t.Setenv("MULTI_STORE_RETRIEVE_TIMEOUT_SEC", "1")

	fakeSlow := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		sleep:      3 * time.Second, // exceeds 1s timeout
	}
	fakeFast := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		canned:     []*types.IndexWithScore{{ChunkID: "fast1", Score: 0.7}},
	}
	groups := []*storeGroup{
		{Engine: buildBoundComposite(t, fakeSlow), BaseParams: vectorParams("q"), TopK: 50, KBIDs: []string{"kb-slow"}},
		{Engine: buildBoundComposite(t, fakeFast), BaseParams: vectorParams("q"), TopK: 50, KBIDs: []string{"kb-fast"}},
	}

	s := &knowledgeBaseService{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.retrieveFromStores(ctx, groups, retriever.EngineAwareNormalizer{})
	require.Error(t, err)
	app, ok := apperrors.IsAppError(err)
	require.True(t, ok, "expected typed AppError on timeout, got %T", err)
	assert.Equal(t, apperrors.ErrVectorStoreUnavailable, app.Code)
}

// ---------------------------------------------------------------------------
// authorizeKBAccess — per-KB authorization on multi-KB search scope
// ---------------------------------------------------------------------------

// fakeKBShareForAuth implements just enough of KBShareService for the
// authorizeKBAccess test matrix. Only HasTenantKBPermission is exercised;
// the embedded interface keeps the type assignable.
type fakeKBShareForAuth struct {
	// allowed maps kbID → tenantID → allowed. Mirrors the Plan 3 (#1303)
	// per-tenant permission model.
	allowed map[string]map[uint64]bool
	err     error
	interfaces.KBShareService
}

func (f *fakeKBShareForAuth) HasTenantKBPermission(
	_ context.Context, kbID string, callerTenantID uint64,
	_ types.TenantRole, _ types.OrgMemberRole,
) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if perTenant, ok := f.allowed[kbID]; ok {
		return perTenant[callerTenantID], nil
	}
	return false, nil
}

func ctxWithTenantForAuth(tenantID uint64) context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)
}

func TestAuthorizeKBAccess_SameTenantAllPass(t *testing.T) {
	t.Parallel()
	s := &knowledgeBaseService{kbShareService: &fakeKBShareForAuth{}}
	kbs := []*types.KnowledgeBase{
		{ID: "kb-1", TenantID: 7},
		{ID: "kb-2", TenantID: 7},
	}
	err := s.authorizeKBAccess(ctxWithTenantForAuth(7), kbs, 7)
	require.NoError(t, err)
}

func TestAuthorizeKBAccess_ForeignTenantWithShare_OK(t *testing.T) {
	t.Parallel()
	share := &fakeKBShareForAuth{
		allowed: map[string]map[uint64]bool{
			"kb-foreign": {7: true},
		},
	}
	s := &knowledgeBaseService{kbShareService: share}
	kbs := []*types.KnowledgeBase{
		{ID: "kb-own", TenantID: 7},
		{ID: "kb-foreign", TenantID: 99},
	}
	err := s.authorizeKBAccess(ctxWithTenantForAuth(7), kbs, 7)
	require.NoError(t, err)
}

func TestAuthorizeKBAccess_ForeignTenantNoShare_NotFound(t *testing.T) {
	t.Parallel()
	share := &fakeKBShareForAuth{
		allowed: map[string]map[uint64]bool{
			// kb-foreign explicitly NOT in allowed map
		},
	}
	s := &knowledgeBaseService{kbShareService: share}
	kbs := []*types.KnowledgeBase{
		{ID: "kb-foreign", TenantID: 99},
	}
	err := s.authorizeKBAccess(ctxWithTenantForAuth(7), kbs, 7)
	require.Error(t, err)
	app, ok := apperrors.IsAppError(err)
	require.True(t, ok, "expected typed AppError, got %T", err)
	assert.Equal(t, apperrors.ErrNotFound, app.Code,
		"reject must surface as NotFound to avoid leaking foreign KB existence")
}

func TestAuthorizeKBAccess_PermissionLookupError_500(t *testing.T) {
	t.Parallel()
	share := &fakeKBShareForAuth{err: stderrors.New("share infra down")}
	s := &knowledgeBaseService{kbShareService: share}
	kbs := []*types.KnowledgeBase{{ID: "kb-foreign", TenantID: 99}}
	err := s.authorizeKBAccess(ctxWithTenantForAuth(7), kbs, 7)
	require.Error(t, err)
	app, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrInternalServer, app.Code)
}

func TestAuthorizeKBAccess_EmptyKBs_OK(t *testing.T) {
	t.Parallel()
	s := &knowledgeBaseService{kbShareService: &fakeKBShareForAuth{}}
	err := s.authorizeKBAccess(ctxWithTenantForAuth(7), nil, 7)
	require.NoError(t, err)
}

// TestIterativeRetrieve_PropagatesTypedAppError pins the contract that
// a typed AppError raised inside the iterative FAQ fan-out is
// propagated upward to HybridSearch rather than being silently
// converted into a truncated chunk list. Without this guarantee, a
// per-group timeout or binding-invalid error during iteration N would
// return whatever partial chunks landed in iterations 0..N-1 with no
// error signal to the client.
func TestIterativeRetrieve_PropagatesTypedAppError(t *testing.T) {
	t.Parallel()
	bad := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		cannedErr:  stderrors.New("simulated retrieve failure"),
	}
	groups := []*storeGroup{
		{Engine: buildBoundComposite(t, bad), BaseParams: vectorParams("q"), TopK: 50, KBIDs: []string{"kb-bad"}},
		{Engine: buildBoundComposite(t, bad), BaseParams: vectorParams("q"), TopK: 50, KBIDs: []string{"kb-bad-2"}},
	}
	s := &knowledgeBaseService{}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	results, err := s.iterativeRetrieveWithDeduplication(ctx, groups, 10, "q")
	require.Error(t, err)
	assert.Nil(t, results, "results must be nil so HybridSearch returns a clean error response")
	app, ok := apperrors.IsAppError(err)
	require.True(t, ok, "expected typed AppError to propagate, got %T", err)
	assert.Equal(t, apperrors.ErrVectorStoreUnavailable, app.Code)
}

// TestApplyFAQPostProcessing_PropagatesError is the matching test at the
// applyFAQPostProcessing layer — the error path must surface upward.
func TestApplyFAQPostProcessing_PropagatesError(t *testing.T) {
	t.Parallel()
	bad := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		cannedErr:  stderrors.New("simulated retrieve failure"),
	}
	groups := []*storeGroup{
		{Engine: buildBoundComposite(t, bad), BaseParams: vectorParams("q"), TopK: 50, KBIDs: []string{"kb-bad"}},
		{Engine: buildBoundComposite(t, bad), BaseParams: vectorParams("q"), TopK: 50, KBIDs: []string{"kb-bad-2"}},
	}
	s := &knowledgeBaseService{}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	// FAQ KB + iterative-retrieval trigger (chunks < MatchCount AND
	// vectorResults == matchCount): with 1 chunk in `chunks` and 5 in
	// `vectorResults` matching matchCount=5, MatchCount=10, the iterative
	// path is taken and the propagated error must surface.
	kb := &types.KnowledgeBase{
		ID: "kb-bad", Type: types.KnowledgeBaseTypeFAQ,
		EmbeddingModelID: "m-1", TenantID: 1,
	}
	chunks := []*types.IndexWithScore{{ChunkID: "c1", Score: 0.5}}
	vectorResults := make([]*types.IndexWithScore, 5)
	for i := range vectorResults {
		vectorResults[i] = &types.IndexWithScore{ChunkID: fmt.Sprintf("v%d", i)}
	}
	params := types.SearchParams{QueryText: "q", MatchCount: 10}
	out, err := s.applyFAQPostProcessing(ctx, kb, chunks, vectorResults, groups, params, 5)
	require.Error(t, err)
	assert.Nil(t, out)
	_, ok := apperrors.IsAppError(err)
	require.True(t, ok)
}

// TestIsParentCancelled distinguishes a true client cancel
// (context.Canceled) from a parent-deadline expiry. Only the former is
// treated as "client gave up"; the latter must fall through to the
// typed unavailable error so the handler returns a stable 4xx instead
// of the raw stdlib DeadlineExceeded.
func TestIsParentCancelled(t *testing.T) {
	t.Parallel()
	bg := context.Background()
	if isParentCancelled(bg) {
		t.Errorf("background ctx must not be reported cancelled")
	}

	canc, cancel := context.WithCancel(bg)
	cancel()
	if !isParentCancelled(canc) {
		t.Errorf("explicit cancel must be reported cancelled")
	}

	dead, cancelD := context.WithTimeout(bg, 1*time.Millisecond)
	defer cancelD()
	time.Sleep(5 * time.Millisecond)
	if isParentCancelled(dead) {
		t.Errorf("parent-deadline expiry must NOT be reported as client cancel")
	}
}

// TestRetrieveFromStores_IterativePattern_NoInternalRace mirrors the
// iterative-FAQ usage contract: ONE driver goroutine mutates group.TopK
// between calls, retrieveFromStores spawns parallel internal goroutines
// each call. The race detector confirms BaseParams is never observed
// mid-mutation because paramsWithTopK builds a fresh slice per call.
//
// We use two groups to actually trigger the multi-group fan-out path
// (the single-group fast path doesn't exercise the goroutine spawn).
func TestRetrieveFromStores_IterativePattern_NoInternalRace(t *testing.T) {
	t.Parallel()
	fakeA := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		canned:     []*types.IndexWithScore{{ChunkID: "a", Score: 0.5}},
	}
	fakeB := &fakeRetrieveEngineService{
		engineType: types.PostgresRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
		canned:     []*types.IndexWithScore{{ChunkID: "b", Score: 0.3}},
	}
	groups := []*storeGroup{
		{Engine: buildBoundComposite(t, fakeA), BaseParams: vectorParams("q"), TopK: 10, KBIDs: []string{"kb-a"}},
		{Engine: buildBoundComposite(t, fakeB), BaseParams: vectorParams("q"), TopK: 10, KBIDs: []string{"kb-b"}},
	}
	s := &knowledgeBaseService{}

	// Sequential driver: bump TopK then call. -race confirms the internal
	// concurrentRetrieve goroutines see a stable BaseParams slice.
	for k := 0; k < 8; k++ {
		for _, g := range groups {
			g.TopK = 10 + k
		}
		_, err := s.retrieveFromStores(context.Background(),
			groups, retriever.EngineAwareNormalizer{})
		require.NoError(t, err)
	}

	// Verify BaseParams remained at its original TopK (mutation only on
	// storeGroup.TopK; paramsWithTopK does the override on a fresh slice).
	for _, g := range groups {
		assert.Equal(t, 50, g.BaseParams[0].TopK, "BaseParams TopK must stay immutable")
	}
}
