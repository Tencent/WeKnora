package chatpipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreparedPipelineLogFieldsContainOnlySafeSummary(t *testing.T) {
	t.Parallel()

	ctx := langfuse.WithPreparedKnowledgeScope(
		context.Background(),
		"1234567890abcdef",
	)
	fields := preparedPipelineLogFields(ctx, map[string]interface{}{
		"tenant_id":             uint64(73),
		"folder_ids":            []string{"folder-private"},
		"knowledge_ids":         []string{"knowledge-private"},
		"query":                 "query-private",
		"raw_tool_args":         "tool-private",
		"error":                 "SELECT private FROM internal_table",
		"result_count":          3,
		"folder_filter_enabled": true,
	})
	encoded, err := json.Marshal(fields)
	require.NoError(t, err)
	payload := string(encoded)
	for _, secret := range []string{
		"73",
		"folder-private",
		"knowledge-private",
		"query-private",
		"tool-private",
		"SELECT private",
		"internal_table",
	} {
		assert.NotContains(t, payload, secret)
	}
	assert.Equal(t, 3, fields["result_count"])
	assert.Equal(t, true, fields["folder_filter_enabled"])
	assert.Equal(t, "1234567890ab", fields["scope_hash_prefix"])
}

type knowledgeScopeRuntimeKBServiceStub struct {
	interfaces.KnowledgeBaseService

	mu                            sync.Mutex
	calls                         int
	knowledgeBaseID               string
	searchParams                  types.SearchParams
	searchParamsByKnowledgeBaseID map[string]types.SearchParams
	embeddingModelKeys            map[string]string
	hybridErrors                  map[string]error
	hybridResults                 map[string][]*types.SearchResult
}

type knowledgeScopeRuntimeGraphRepositoryStub struct {
	interfaces.RetrieveGraphRepository

	calls int
	err   error
}

type knowledgeScopeRuntimeWebServiceStub struct {
	interfaces.WebSearchService

	calls int
}

type knowledgeScopeRuntimeTenantServiceStub struct {
	interfaces.TenantService
}

func (s *knowledgeScopeRuntimeWebServiceStub) Search(
	context.Context,
	string,
	*types.WebSearchConfig,
	string,
) ([]*types.WebSearchResult, error) {
	s.calls++
	return []*types.WebSearchResult{{Title: "web result"}}, nil
}

func (s *knowledgeScopeRuntimeGraphRepositoryStub) SearchNode(
	context.Context,
	types.NameSpace,
	[]string,
) (*types.GraphData, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &types.GraphData{}, nil
}

func (s *knowledgeScopeRuntimeKBServiceStub) GetKnowledgeBasesByIDsOnly(
	context.Context,
	[]string,
) ([]*types.KnowledgeBase, error) {
	s.calls++
	return nil, nil
}

func (s *knowledgeScopeRuntimeKBServiceStub) ResolveEmbeddingModelKeys(
	context.Context,
	[]*types.KnowledgeBase,
) map[string]string {
	s.calls++
	return s.embeddingModelKeys
}

func (s *knowledgeScopeRuntimeKBServiceStub) GetQueryEmbedding(
	context.Context,
	string,
	string,
) ([]float32, error) {
	s.calls++
	return nil, nil
}

func (s *knowledgeScopeRuntimeKBServiceStub) HybridSearch(
	_ context.Context,
	knowledgeBaseID string,
	params types.SearchParams,
) ([]*types.SearchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.knowledgeBaseID = knowledgeBaseID
	s.searchParams = params
	if s.searchParamsByKnowledgeBaseID == nil {
		s.searchParamsByKnowledgeBaseID = make(
			map[string]types.SearchParams,
		)
	}
	s.searchParamsByKnowledgeBaseID[knowledgeBaseID] = params
	if err := s.hybridErrors[knowledgeBaseID]; err != nil {
		return nil, err
	}
	if results, ok := s.hybridResults[knowledgeBaseID]; ok {
		return results, nil
	}
	return []*types.SearchResult{{ID: "result-1"}}, nil
}

func (s *knowledgeScopeRuntimeKBServiceStub) searchParamsForKnowledgeBase(
	knowledgeBaseID string,
) (types.SearchParams, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	params, ok := s.searchParamsByKnowledgeBaseID[knowledgeBaseID]
	return params, ok
}

func TestSearchSingleTargetProjectsPreparedScopeIntoSearchParams(t *testing.T) {
	t.Parallel()

	filter, err := types.NewResolvedFolderFilter(
		true,
		[]string{"10000000-0000-4000-8000-000000000001"},
	)
	require.NoError(t, err)
	target := &types.SearchTarget{
		Type:               types.SearchTargetTypeKnowledge,
		KnowledgeBaseID:    "kb-1",
		TenantID:           7,
		SourceTenantID:     41,
		KnowledgeIDs:       []string{"knowledge-1"},
		TagIDs:             []string{"tag-physical"},
		ScopeTagIDs:        []string{"tag-logical"},
		FolderFilter:       filter,
		ExecutionScopeHash: "execution-hash",
	}
	serviceStub := &knowledgeScopeRuntimeKBServiceStub{}
	plugin := &PluginSearch{knowledgeBaseService: serviceStub}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			EmbeddingTopK:    12,
			VectorThreshold:  0.6,
			KeywordThreshold: 0.4,
		},
	}
	var (
		mu      sync.Mutex
		results []*types.SearchResult
	)

	plugin.searchSingleTarget(
		context.Background(),
		chatManage,
		target,
		"prepared query",
		[]float32{0.1, 0.2},
		&mu,
		&results,
	)

	assert.Equal(t, 1, serviceStub.calls)
	assert.Equal(t, "kb-1", serviceStub.knowledgeBaseID)
	assert.Equal(t, "prepared query", serviceStub.searchParams.QueryText)
	assert.Equal(t, []float32{0.1, 0.2}, serviceStub.searchParams.QueryEmbedding)
	assert.Equal(t, []string{"knowledge-1"}, serviceStub.searchParams.KnowledgeIDs)
	assert.Equal(t, []string{"tag-physical"}, serviceStub.searchParams.TagIDs)
	assert.Equal(t, []string{"tag-logical"}, serviceStub.searchParams.ScopeTagIDs)
	assert.Equal(t, uint64(41), serviceStub.searchParams.SourceTenantID)
	assert.True(t, serviceStub.searchParams.FolderFilter.Enabled())
	assert.Equal(
		t,
		[]string{"10000000-0000-4000-8000-000000000001"},
		serviceStub.searchParams.FolderFilter.FolderIDs(),
	)
	assert.Equal(t, "execution-hash", serviceStub.searchParams.ExecutionScopeHash)
	require.Len(t, results, 1)

	serviceStub.searchParams.QueryEmbedding[0] = 9
	serviceStub.searchParams.KnowledgeIDs[0] = "mutated-knowledge"
	serviceStub.searchParams.TagIDs[0] = "mutated-physical-tag"
	serviceStub.searchParams.ScopeTagIDs[0] = "mutated-logical-tag"
	assert.Equal(t, []string{"knowledge-1"}, target.KnowledgeIDs)
	assert.Equal(t, []string{"tag-physical"}, target.TagIDs)
	assert.Equal(t, []string{"tag-logical"}, target.ScopeTagIDs)
}

func TestSearchByTargetsMixedFolderAndFullKBScopeIsOrderIndependent(
	t *testing.T,
) {
	filter, err := types.NewResolvedFolderFilter(
		true,
		[]string{"10000000-0000-4000-8000-000000000001"},
	)
	require.NoError(t, err)
	folderTarget := &types.SearchTarget{
		Type:               types.SearchTargetTypeKnowledge,
		KnowledgeBaseID:    "kb-folder",
		SourceTenantID:     41,
		KnowledgeIDs:       []string{"knowledge-folder"},
		FolderFilter:       filter,
		ExecutionScopeHash: "execution-hash",
	}
	fullKBTarget := &types.SearchTarget{
		Type:               types.SearchTargetTypeKnowledgeBase,
		KnowledgeBaseID:    "kb-full",
		SourceTenantID:     41,
		ExecutionScopeHash: "execution-hash",
	}
	type scopeSemantics struct {
		folderKnowledgeIDs []string
		folderIDs          []string
		folderEnabled      bool
		fullKBIDs          []string
		fullKnowledgeIDs   []string
		fullFolderEnabled  bool
		folderScopeHash    string
		fullScopeHash      string
	}
	var gotByOrder []scopeSemantics
	testCases := []struct {
		name    string
		targets types.SearchTargets
	}{
		{
			name:    "folder before full knowledge base",
			targets: types.SearchTargets{folderTarget, fullKBTarget},
		},
		{
			name:    "full knowledge base before folder",
			targets: types.SearchTargets{fullKBTarget, folderTarget},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			serviceStub := &knowledgeScopeRuntimeKBServiceStub{
				embeddingModelKeys: map[string]string{
					"kb-folder": "shared-model",
					"kb-full":   "shared-model",
				},
				hybridResults: map[string][]*types.SearchResult{
					"kb-folder": {{ID: "folder-result"}},
					"kb-full":   {{ID: "full-result"}},
				},
			}
			plugin := &PluginSearch{knowledgeBaseService: serviceStub}
			chatManage := &types.ChatManage{
				PipelineRequest: types.PipelineRequest{
					SearchTargets:      testCase.targets,
					ExecutionScopeHash: "execution-hash",
					EmbeddingTopK:      3,
					VectorThreshold:    0.6,
					KeywordThreshold:   0.4,
				},
				PipelineState: types.PipelineState{
					RewriteQuery: "prepared query",
				},
			}

			results, err := plugin.searchByTargets(
				context.Background(),
				chatManage,
			)
			require.NoError(t, err)
			require.Len(t, results, 2)
			assert.ElementsMatch(
				t,
				[]string{"folder-result", "full-result"},
				[]string{results[0].ID, results[1].ID},
			)

			folderParams, ok := serviceStub.searchParamsForKnowledgeBase(
				"kb-folder",
			)
			require.True(t, ok)
			fullParams, ok := serviceStub.searchParamsForKnowledgeBase(
				"kb-full",
			)
			require.True(t, ok)

			assert.Equal(
				t,
				[]string{"knowledge-folder"},
				folderParams.KnowledgeIDs,
			)
			assert.True(t, folderParams.FolderFilter.Enabled())
			assert.Equal(
				t,
				[]string{"10000000-0000-4000-8000-000000000001"},
				folderParams.FolderFilter.FolderIDs(),
			)
			assert.Equal(
				t,
				[]string{"kb-full"},
				fullParams.KnowledgeBaseIDs,
			)
			assert.Empty(t, fullParams.KnowledgeIDs)
			assert.False(t, fullParams.FolderFilter.Enabled())
			assert.Equal(t, uint64(41), folderParams.SourceTenantID)
			assert.Equal(t, uint64(41), fullParams.SourceTenantID)
			assert.Equal(
				t,
				"execution-hash",
				folderParams.ExecutionScopeHash,
			)
			assert.Equal(
				t,
				"execution-hash",
				fullParams.ExecutionScopeHash,
			)

			gotByOrder = append(gotByOrder, scopeSemantics{
				folderKnowledgeIDs: folderParams.KnowledgeIDs,
				folderIDs:          folderParams.FolderFilter.FolderIDs(),
				folderEnabled:      folderParams.FolderFilter.Enabled(),
				fullKBIDs:          fullParams.KnowledgeBaseIDs,
				fullKnowledgeIDs:   fullParams.KnowledgeIDs,
				fullFolderEnabled:  fullParams.FolderFilter.Enabled(),
				folderScopeHash:    folderParams.ExecutionScopeHash,
				fullScopeHash:      fullParams.ExecutionScopeHash,
			})
		})
	}

	require.Len(t, gotByOrder, 2)
	assert.Equal(t, gotByOrder[0], gotByOrder[1])
}

func TestPluginSearchEnabledEmptySkipsKnowledgeServices(t *testing.T) {
	t.Parallel()

	filter, err := types.NewResolvedFolderFilter(true, nil)
	require.NoError(t, err)
	serviceStub := &knowledgeScopeRuntimeKBServiceStub{}
	plugin := &PluginSearch{knowledgeBaseService: serviceStub}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SearchTargets: types.SearchTargets{
				{
					Type:            types.SearchTargetTypeKnowledgeBase,
					KnowledgeBaseID: "kb-1",
					FolderFilter:    filter,
				},
			},
		},
		PipelineState: types.PipelineState{
			RewriteQuery: "query",
		},
	}

	gotErr := plugin.OnEvent(
		context.Background(),
		types.CHUNK_SEARCH,
		chatManage,
		func() *PluginError {
			t.Fatal("enabled-empty retrieval must not continue as a successful hit")
			return nil
		},
	)

	assert.Same(t, ErrSearchNothing, gotErr)
	assert.Equal(t, 0, serviceStub.calls)
	assert.Empty(t, chatManage.SearchResult)
}

func TestPluginSearchExplicitEmptyWithoutTargetsSkipsWebSearch(t *testing.T) {
	t.Parallel()

	executionScope, err := types.NewKnowledgeScope(nil)
	require.NoError(t, err)
	knowledgeStub := &knowledgeScopeRuntimeKBServiceStub{}
	webStub := &knowledgeScopeRuntimeWebServiceStub{}
	plugin := &PluginSearch{
		knowledgeBaseService: knowledgeStub,
		webSearchService:     webStub,
		tenantService:        &knowledgeScopeRuntimeTenantServiceStub{},
	}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			ExecutionScope:           executionScope,
			ExecutionScopeHash:       "execution-hash",
			RetrievalExplicitlyEmpty: true,
			WebSearchEnabled:         true,
			WebSearchProviderID:      "provider-1",
		},
		PipelineState: types.PipelineState{
			RewriteQuery: "query",
		},
	}

	gotErr := plugin.OnEvent(
		context.Background(),
		types.CHUNK_SEARCH,
		chatManage,
		func() *PluginError {
			t.Fatal("enabled-empty retrieval must not continue")
			return nil
		},
	)

	assert.Same(t, ErrSearchNothing, gotErr)
	assert.Equal(t, 0, knowledgeStub.calls)
	assert.Equal(t, 0, webStub.calls)
	assert.Empty(t, chatManage.SearchResult)
}

func TestPluginSearchDisabledScopePreservesWebSearch(t *testing.T) {
	t.Parallel()

	webStub := &knowledgeScopeRuntimeWebServiceStub{}
	plugin := &PluginSearch{
		webSearchService: webStub,
		tenantService:    &knowledgeScopeRuntimeTenantServiceStub{},
	}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			WebSearchEnabled:    true,
			WebSearchProviderID: "provider-1",
		},
		PipelineState: types.PipelineState{
			RewriteQuery: "query",
		},
	}

	gotErr := plugin.OnEvent(
		context.Background(),
		types.CHUNK_SEARCH,
		chatManage,
		func() *PluginError { return nil },
	)

	require.Nil(t, gotErr)
	assert.Equal(t, 1, webStub.calls)
}

func TestRemoveDuplicateResultsUsesCallerContext(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	testLogger := logrus.New()
	testLogger.SetOutput(&output)
	testLogger.SetLevel(logrus.DebugLevel)
	ctx := context.WithValue(
		context.Background(),
		types.LoggerContextKey,
		logrus.NewEntry(testLogger).WithField(
			"request_marker",
			"caller-context",
		),
	)

	results := removeDuplicateResults(ctx, []*types.SearchResult{
		{ID: "duplicate", Content: "same content"},
		{ID: "duplicate", Content: "same content"},
	})

	require.Len(t, results, 1)
	assert.Contains(t, output.String(), "request_marker=caller-context")
}

func TestPluginSearchEnabledNonEmptyExecutesPreparedFolderTarget(
	t *testing.T,
) {
	t.Parallel()

	filter, err := types.NewResolvedFolderFilter(
		true,
		[]string{"10000000-0000-4000-8000-000000000001"},
	)
	require.NoError(t, err)
	serviceStub := &knowledgeScopeRuntimeKBServiceStub{}
	plugin := &PluginSearch{knowledgeBaseService: serviceStub}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SearchTargets: types.SearchTargets{
				{
					Type:               types.SearchTargetTypeKnowledge,
					KnowledgeBaseID:    "kb-1",
					SourceTenantID:     41,
					KnowledgeIDs:       []string{"knowledge-1"},
					FolderFilter:       filter,
					ExecutionScopeHash: "execution-hash",
				},
			},
			ExecutionScopeHash: "execution-hash",
			EmbeddingTopK:      3,
		},
		PipelineState: types.PipelineState{
			RewriteQuery: "prepared query",
		},
	}
	nextCalls := 0

	gotErr := plugin.OnEvent(
		context.Background(),
		types.CHUNK_SEARCH,
		chatManage,
		func() *PluginError {
			nextCalls++
			return nil
		},
	)

	require.Nil(t, gotErr)
	assert.Equal(t, 1, nextCalls)
	assert.Equal(t, "kb-1", serviceStub.knowledgeBaseID)
	assert.Equal(t, []string{"knowledge-1"}, serviceStub.searchParams.KnowledgeIDs)
	assert.True(t, serviceStub.searchParams.FolderFilter.Enabled())
	assert.Equal(
		t,
		[]string{"10000000-0000-4000-8000-000000000001"},
		serviceStub.searchParams.FolderFilter.FolderIDs(),
	)
	assert.Equal(t, "execution-hash", serviceStub.searchParams.ExecutionScopeHash)
	require.Len(t, chatManage.SearchResult, 1)
}

func TestRunQueryExpansionPreservesPreparedFolderTarget(t *testing.T) {
	t.Parallel()

	filter, err := types.NewResolvedFolderFilter(
		true,
		[]string{"10000000-0000-4000-8000-000000000001"},
	)
	require.NoError(t, err)
	serviceStub := &knowledgeScopeRuntimeKBServiceStub{}
	plugin := &PluginSearch{knowledgeBaseService: serviceStub}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: "what is alpha beta",
			SearchTargets: types.SearchTargets{{
				Type:               types.SearchTargetTypeKnowledge,
				KnowledgeBaseID:    "kb-1",
				SourceTenantID:     41,
				KnowledgeIDs:       []string{"knowledge-1"},
				TagIDs:             []string{"tag-physical"},
				ScopeTagIDs:        []string{"tag-logical"},
				FolderFilter:       filter,
				ExecutionScopeHash: "execution-hash",
			}},
			ExecutionScopeHash: "execution-hash",
			EmbeddingTopK:      2,
			RerankTopK:         1,
			VectorThreshold:    0.6,
			KeywordThreshold:   0.4,
		},
		PipelineState: types.PipelineState{
			RewriteQuery: "what is alpha beta",
		},
	}

	results, err := plugin.runQueryExpansion(
		context.Background(),
		chatManage,
	)

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "alpha beta", serviceStub.searchParams.QueryText)
	assert.Equal(t, []string{"knowledge-1"}, serviceStub.searchParams.KnowledgeIDs)
	assert.Equal(t, []string{"tag-physical"}, serviceStub.searchParams.TagIDs)
	assert.Equal(t, []string{"tag-logical"}, serviceStub.searchParams.ScopeTagIDs)
	assert.Equal(t, uint64(41), serviceStub.searchParams.SourceTenantID)
	assert.True(t, serviceStub.searchParams.FolderFilter.Enabled())
	assert.Equal(
		t,
		[]string{"10000000-0000-4000-8000-000000000001"},
		serviceStub.searchParams.FolderFilter.FolderIDs(),
	)
	assert.Equal(t, "execution-hash", serviceStub.searchParams.ExecutionScopeHash)
}

func TestPluginSearchParallelExecutesPreparedFolderTarget(t *testing.T) {
	t.Parallel()

	filter, err := types.NewResolvedFolderFilter(
		true,
		[]string{"10000000-0000-4000-8000-000000000001"},
	)
	require.NoError(t, err)
	serviceStub := &knowledgeScopeRuntimeKBServiceStub{}
	plugin := &PluginSearchParallel{
		searchPlugin: &PluginSearch{knowledgeBaseService: serviceStub},
	}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SearchTargets: types.SearchTargets{{
				Type:               types.SearchTargetTypeKnowledge,
				KnowledgeBaseID:    "kb-1",
				SourceTenantID:     41,
				KnowledgeIDs:       []string{"knowledge-1"},
				FolderFilter:       filter,
				ExecutionScopeHash: "execution-hash",
			}},
			ExecutionScopeHash: "execution-hash",
			EmbeddingTopK:      3,
		},
		PipelineState: types.PipelineState{
			RewriteQuery: "prepared query",
		},
	}
	nextCalls := 0

	gotErr := plugin.OnEvent(
		context.Background(),
		types.CHUNK_SEARCH_PARALLEL,
		chatManage,
		func() *PluginError {
			nextCalls++
			return nil
		},
	)

	require.Nil(t, gotErr)
	assert.Equal(t, 1, nextCalls)
	assert.Equal(t, "kb-1", serviceStub.knowledgeBaseID)
	assert.Equal(t, []string{"knowledge-1"}, serviceStub.searchParams.KnowledgeIDs)
	assert.True(t, serviceStub.searchParams.FolderFilter.Enabled())
	assert.Equal(t, "execution-hash", serviceStub.searchParams.ExecutionScopeHash)
	require.Len(t, chatManage.SearchResult, 1)
}

func TestExecutionScopeAllowsEntitySearchOnlyForWholeKnowledgeBases(t *testing.T) {
	t.Parallel()

	wholeKnowledgeBaseTarget, err := types.NewKnowledgeScopeTarget(
		"kb-1",
		41,
		nil,
		nil,
		nil,
		types.ResolvedFolderFilter{},
	)
	require.NoError(t, err)
	wholeKnowledgeBaseScope, err := types.NewKnowledgeScope(
		[]types.KnowledgeScopeTarget{wholeKnowledgeBaseTarget},
	)
	require.NoError(t, err)

	constrainedTarget, err := types.NewKnowledgeScopeTarget(
		"kb-1",
		41,
		[]string{"knowledge-1"},
		nil,
		nil,
		types.ResolvedFolderFilter{},
	)
	require.NoError(t, err)
	constrainedScope, err := types.NewKnowledgeScope(
		[]types.KnowledgeScopeTarget{constrainedTarget},
	)
	require.NoError(t, err)

	emptyFilter, err := types.NewResolvedFolderFilter(true, nil)
	require.NoError(t, err)
	emptyTarget, err := types.NewKnowledgeScopeTarget(
		"kb-1",
		41,
		nil,
		nil,
		nil,
		emptyFilter,
	)
	require.NoError(t, err)
	emptyScope, err := types.NewKnowledgeScope(
		[]types.KnowledgeScopeTarget{emptyTarget},
	)
	require.NoError(t, err)

	assert.True(t, executionScopeAllowsEntitySearch(nil, 0))
	assert.True(t, executionScopeAllowsEntitySearch(
		wholeKnowledgeBaseScope,
		41,
	))
	assert.False(t, executionScopeAllowsEntitySearch(
		wholeKnowledgeBaseScope,
		7,
	))
	assert.False(t, executionScopeAllowsEntitySearch(constrainedScope, 41))
	assert.False(t, executionScopeAllowsEntitySearch(emptyScope, 41))
}

func TestConstrainEntitySearchProjectionRemovesOutOfScopeTargets(t *testing.T) {
	t.Parallel()

	target, err := types.NewKnowledgeScopeTarget(
		"kb-1",
		41,
		nil,
		nil,
		nil,
		types.ResolvedFolderFilter{},
	)
	require.NoError(t, err)
	scope, err := types.NewKnowledgeScope(
		[]types.KnowledgeScopeTarget{target},
	)
	require.NoError(t, err)
	chatManage := &types.ChatManage{
		PipelineState: types.PipelineState{
			EntityKBIDs: []string{"kb-1", "kb-2"},
			EntityKnowledge: map[string]string{
				"knowledge-1": "kb-1",
				"knowledge-2": "kb-2",
			},
		},
	}

	constrainEntitySearchProjection(chatManage, scope)

	assert.Equal(t, []string{"kb-1"}, chatManage.EntityKBIDs)
	assert.Equal(
		t,
		map[string]string{"knowledge-1": "kb-1"},
		chatManage.EntityKnowledge,
	)
}

func TestPreparedEntitySearchFailsClosedWithoutContinuing(t *testing.T) {
	t.Parallel()

	privateErr := errors.New("private graph repository error")
	graphRepository := &knowledgeScopeRuntimeGraphRepositoryStub{
		err: privateErr,
	}
	plugin := &PluginSearchEntity{graphRepo: graphRepository}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			ExecutionScopeHash: "execution-hash",
		},
		PipelineState: types.PipelineState{
			Entity:      []string{"entity"},
			EntityKBIDs: []string{"kb-1"},
		},
	}
	nextCalls := 0

	pluginErr := plugin.OnEvent(
		context.Background(),
		types.ENTITY_SEARCH,
		chatManage,
		func() *PluginError {
			nextCalls++
			return nil
		},
	)

	require.NotNil(t, pluginErr)
	assert.Equal(t, ErrSearch.ErrorType, pluginErr.ErrorType)
	assert.ErrorIs(t, pluginErr.Err, privateErr)
	assert.Equal(t, 1, graphRepository.calls)
	assert.Equal(t, 0, nextCalls)
	assert.Nil(t, chatManage.GraphResult)
	assert.Empty(t, chatManage.SearchResult)
}

func TestFilterHistoryByExecutionScopeFailsClosed(t *testing.T) {
	t.Parallel()

	emptyFilter, err := types.NewResolvedFolderFilter(true, nil)
	require.NoError(t, err)
	emptyTarget, err := types.NewKnowledgeScopeTarget(
		"kb-1",
		41,
		nil,
		nil,
		nil,
		emptyFilter,
	)
	require.NoError(t, err)
	emptyScope, err := types.NewKnowledgeScope(
		[]types.KnowledgeScopeTarget{emptyTarget},
	)
	require.NoError(t, err)

	results := []*types.SearchResult{
		{
			ID:              "chunk-1",
			KnowledgeID:     "knowledge-1",
			KnowledgeBaseID: "kb-1",
		},
	}
	assert.Empty(t, filterHistoryByExecutionScope(results, emptyScope))

	knowledgeTarget, err := types.NewKnowledgeScopeTarget(
		"kb-1",
		41,
		[]string{"knowledge-1"},
		nil,
		nil,
		types.ResolvedFolderFilter{},
	)
	require.NoError(t, err)
	knowledgeScope, err := types.NewKnowledgeScope(
		[]types.KnowledgeScopeTarget{knowledgeTarget},
	)
	require.NoError(t, err)

	filtered := filterHistoryByExecutionScope(
		[]*types.SearchResult{
			{
				ID:              "chunk-allowed",
				KnowledgeID:     "knowledge-1",
				KnowledgeBaseID: "kb-1",
			},
			{
				ID:              "chunk-other-knowledge",
				KnowledgeID:     "knowledge-2",
				KnowledgeBaseID: "kb-1",
			},
			{
				ID:              "chunk-other-kb",
				KnowledgeID:     "knowledge-1",
				KnowledgeBaseID: "kb-2",
			},
		},
		knowledgeScope,
	)

	require.Len(t, filtered, 1)
	assert.Equal(t, "chunk-allowed", filtered[0].ID)

	taggedKnowledgeTarget, err := types.NewKnowledgeScopeTarget(
		"kb-1",
		41,
		[]string{"knowledge-1"},
		[]string{"tag-1"},
		[]string{"tag-1"},
		types.ResolvedFolderFilter{},
	)
	require.NoError(t, err)
	taggedKnowledgeScope, err := types.NewKnowledgeScope(
		[]types.KnowledgeScopeTarget{taggedKnowledgeTarget},
	)
	require.NoError(t, err)
	assert.Empty(
		t,
		filterHistoryByExecutionScope(results, taggedKnowledgeScope),
	)
}

func TestSearchByTargetsPreparedFailureReturnsNoPartialResults(t *testing.T) {
	t.Parallel()

	privateErr := errors.New("private retriever error")
	serviceStub := &knowledgeScopeRuntimeKBServiceStub{
		hybridErrors: map[string]error{
			"kb-2": privateErr,
		},
		hybridResults: map[string][]*types.SearchResult{
			"kb-1": {{ID: "must-not-be-returned"}},
		},
	}
	plugin := &PluginSearch{knowledgeBaseService: serviceStub}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SearchTargets: types.SearchTargets{
				{
					Type:               types.SearchTargetTypeKnowledge,
					KnowledgeBaseID:    "kb-1",
					SourceTenantID:     41,
					KnowledgeIDs:       []string{"knowledge-1"},
					ExecutionScopeHash: "execution-hash",
				},
				{
					Type:               types.SearchTargetTypeKnowledge,
					KnowledgeBaseID:    "kb-2",
					SourceTenantID:     41,
					KnowledgeIDs:       []string{"knowledge-2"},
					ExecutionScopeHash: "execution-hash",
				},
			},
			ExecutionScopeHash: "execution-hash",
		},
		PipelineState: types.PipelineState{
			RewriteQuery: "query",
		},
	}

	results, err := plugin.searchByTargets(context.Background(), chatManage)

	assert.Nil(t, results)
	assert.ErrorIs(t, err, privateErr)
}

func TestSearchByTargetsPreparedPreservesContextError(t *testing.T) {
	t.Parallel()

	serviceStub := &knowledgeScopeRuntimeKBServiceStub{
		hybridErrors: map[string]error{
			"kb-1": context.DeadlineExceeded,
		},
	}
	plugin := &PluginSearch{knowledgeBaseService: serviceStub}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SearchTargets: types.SearchTargets{
				{
					Type:               types.SearchTargetTypeKnowledge,
					KnowledgeBaseID:    "kb-1",
					SourceTenantID:     41,
					KnowledgeIDs:       []string{"knowledge-1"},
					ExecutionScopeHash: "execution-hash",
				},
			},
			ExecutionScopeHash: "execution-hash",
		},
	}

	results, err := plugin.searchByTargets(context.Background(), chatManage)

	assert.Nil(t, results)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
