package chatpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func extractionTestSchema() *types.GraphSchema {
	return &types.GraphSchema{
		Enabled:       true,
		EntityTypes:   []types.GraphEntityType{{Name: "Person"}, {Name: "Company"}},
		RelationTypes: []types.GraphRelationType{{Name: "WORKS_AT", SourceType: "Person", TargetType: "Company"}},
	}
}

type schemaSequenceChat struct {
	fixedChat
	responses []*types.ChatResponse
	err       error
	calls     int
	messages  []chat.Message
}

func (m *schemaSequenceChat) Chat(
	_ context.Context, messages []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.messages = append([]chat.Message(nil), messages...)
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	if m.calls > len(m.responses) {
		return nil, nil
	}
	return m.responses[m.calls-1], nil
}

const validSchemaExtraction = `{"entities":[{"id":"e1","name":"Alex","type":"Person"},` +
	`{"id":"e2","name":"Alex","type":"Company"}],` +
	`"relations":[{"source":"e1","target":"e2","type":"WORKS_AT"}]}`

func TestSchemaExtractorValidatesAndRepairsOnce(t *testing.T) {
	invalid := strings.Replace(validSchemaExtraction, `"WORKS_AT"`, `"UNKNOWN"`, 1)
	for _, tc := range []struct {
		name      string
		responses []string
		wantError bool
	}{
		{"valid first attempt", []string{validSchemaExtraction}, false},
		{"one repair", []string{invalid, validSchemaExtraction}, false},
		{"bounded failure", []string{invalid, invalid}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &schemaSequenceChat{}
			for _, content := range tc.responses {
				model.responses = append(model.responses, &types.ChatResponse{Content: content, FinishReason: "stop"})
			}
			extractor := NewExtractor(model, &types.PromptTemplateStructured{
				Schema: extractionTestSchema(), Description: "Only named people.",
			})
			graph, err := extractor.Extract(context.Background(), "Alex works for the company Alex.")
			require.Equal(t, len(tc.responses), model.calls)
			require.Contains(t, model.messages[0].Content, `"additionalProperties":false`)
			require.Contains(t, model.messages[0].Content, "Only named people.")
			if tc.wantError {
				require.ErrorContains(t, err, "after one repair")
				require.Nil(t, graph)
				return
			}
			require.NoError(t, err)
			require.Len(t, graph.Node, 2)
			require.Equal(t, "Person", graph.Relation[0].SourceType)
			require.Equal(t, "Company", graph.Relation[0].TargetType)
			if model.calls == 2 {
				require.Len(t, model.messages, 4)
				require.Contains(t, model.messages[3].Content, "Validation error:")
			}
		})
	}
}

func TestSchemaExtractorDoesNotRepairTransportOrTruncation(t *testing.T) {
	for _, reason := range []string{"length", " Max_Tokens ", types.FinishReasonIncomplete} {
		t.Run(reason, func(t *testing.T) {
			model := &schemaSequenceChat{responses: []*types.ChatResponse{
				{Content: validSchemaExtraction, FinishReason: reason},
			}}
			extractor := NewExtractor(model, &types.PromptTemplateStructured{Schema: extractionTestSchema()})
			_, err := extractor.Extract(context.Background(), "passage")
			require.Error(t, err)
			require.Equal(t, 1, model.calls)
		})
	}
	model := &schemaSequenceChat{err: errors.New("provider unavailable")}
	extractor := NewExtractor(model, &types.PromptTemplateStructured{Schema: extractionTestSchema()})
	_, err := extractor.Extract(context.Background(), "passage")
	require.ErrorIs(t, err, model.err)
	require.Equal(t, 1, model.calls)
}

func TestSchemaExtractorUsesOnlyValidTypedExamples(t *testing.T) {
	example := types.GraphData{
		Text: "Alex works for the company Alex.",
		Node: []*types.GraphNode{
			{Name: "Alex", EntityType: "Person", Attributes: []string{"ignored"}},
			{Name: "Alex", EntityType: "Company"},
		},
		Relation: []*types.GraphRelation{
			{Node1: "Alex", Node2: "Alex", Type: "WORKS_AT", SourceType: "Person", TargetType: "Company"},
		},
	}
	for _, tc := range []struct {
		name string
		edit func(*types.GraphData)
		want bool
	}{
		{"valid typed example", func(*types.GraphData) {}, true},
		{"untyped legacy example", func(g *types.GraphData) { g.Node[0].EntityType = "" }, false},
		{"wrong endpoint direction", func(g *types.GraphData) { g.Relation[0].SourceType = "Company" }, false},
		{"unknown relation", func(g *types.GraphData) { g.Relation[0].Type = "UNKNOWN" }, false},
		{"missing entity", func(g *types.GraphData) { g.Relation[0].Node2 = "Missing" }, false},
		{"duplicate entity", func(g *types.GraphData) { g.Node = append(g.Node, g.Node[0]) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var copied types.GraphData
			raw, err := json.Marshal(example)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(raw, &copied))
			tc.edit(&copied)
			model := &schemaSequenceChat{responses: []*types.ChatResponse{
				{Content: validSchemaExtraction, FinishReason: "stop"},
			}}
			extractor := NewExtractor(model, &types.PromptTemplateStructured{
				Schema: extractionTestSchema(), Examples: []types.GraphData{copied},
			})
			_, err = extractor.Extract(context.Background(), "Current passage")
			require.NoError(t, err)
			require.Equal(t, "Current passage", model.messages[len(model.messages)-1].Content)
			if tc.want {
				require.Len(t, model.messages, 4)
				require.Equal(t, example.Text, model.messages[1].Content)
				validator, err := types.CompileGraphJSONSchema(extractionTestSchema().ExtractionJSONSchema())
				require.NoError(t, err)
				_, err = extractionTestSchema().ParseExtraction(model.messages[2].Content, validator)
				require.NoError(t, err)
				require.NotContains(t, model.messages[2].Content, "ignored")
			} else {
				require.Len(t, model.messages, 2)
			}
		})
	}
}

func TestExtractSchemaQueriesUsesOptionalDirectedHints(t *testing.T) {
	for _, tc := range []struct {
		name      string
		content   string
		wantError bool
	}{
		{"outgoing", `{"queries":[{"name":"Alex","entity_type":"Person",` +
			`"relation_type":"WORKS_AT","direction":"out","target_type":"Company"}]}`, false},
		{"incoming", `{"queries":[{"name":"Alex","entity_type":"Company",` +
			`"relation_type":"WORKS_AT","direction":"in","target_type":"Person"}]}`, false},
		{"uncertain hints", `{"queries":[{"name":"Alex","entity_type":"",` +
			`"relation_type":"","direction":"","target_type":""}]}`, false},
		{"no named seeds", `{"queries":[]}`, false},
		{"wrong endpoints", `{"queries":[{"name":"Alex","entity_type":"Company",` +
			`"relation_type":"WORKS_AT","direction":"out","target_type":"Person"}]}`, true},
		{"unknown type", `{"queries":[{"name":"Alex","entity_type":"Unknown",` +
			`"relation_type":"","direction":"","target_type":""}]}`, true},
		{"extra property", `{"queries":[{"name":"Alex","entity_type":"","relation_type":"",` +
			`"direction":"","target_type":"","cypher":"MATCH (n)"}]}`, true},
		{"missing fields", `{"queries":[{"name":"Alex"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &schemaSequenceChat{responses: []*types.ChatResponse{{Content: tc.content, FinishReason: "stop"}}}
			queries, err := ExtractSchemaQueries(
				context.Background(), model, extractionTestSchema(), "Where does Alex work?")
			require.Equal(t, 1, model.calls)
			if tc.wantError {
				require.Error(t, err)
				require.Nil(t, queries)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

type schemaKBRepo struct {
	interfaces.KnowledgeBaseRepository
}

func (*schemaKBRepo) GetKnowledgeBaseByIDs(_ context.Context, ids []string) ([]*types.KnowledgeBase, error) {
	var kbs []*types.KnowledgeBase
	for _, id := range ids {
		kbs = append(kbs, &types.KnowledgeBase{ID: id, ExtractConfig: &types.ExtractConfig{
			Enabled: true, Schema: extractionTestSchema(),
		}})
	}
	return kbs, nil
}

func TestSchemaQueryPluginReusesIdenticalDomainsWithoutLegacyCall(t *testing.T) {
	t.Setenv("NEO4J_ENABLE", "true")
	model := &schemaSequenceChat{responses: []*types.ChatResponse{{
		Content: `{"queries":[{"name":"Alex","entity_type":"Person",` +
			`"relation_type":"","direction":"","target_type":""}]}`,
		FinishReason: "stop",
	}}}
	plugin := &PluginExtractEntity{
		modelService: &stubModelService{model: model}, knowledgeBaseRepo: &schemaKBRepo{},
		template: &types.PromptTemplateStructured{Description: "legacy"},
	}
	cm := &types.ChatManage{PipelineRequest: types.PipelineRequest{
		Query: "Where does Alex work?",
		SearchTargets: types.SearchTargets{
			{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb1"},
			{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb2"},
		},
	}}
	require.Nil(t, plugin.OnEvent(context.Background(), types.QUERY_UNDERSTAND, cm, func() *PluginError { return nil }))
	require.Equal(t, 1, model.calls)
	require.Empty(t, cm.Entity)
	require.Len(t, cm.EntityQueries, 2)
	require.Equal(t, cm.EntityQueries["kb1"], cm.EntityQueries["kb2"])
}

type schemaScopeGraphRepo struct {
	*entityGraphRepo
	typedScopes []types.NameSpace
}

func (r *schemaScopeGraphRepo) SearchNodeWithSchema(_ context.Context, namespace types.NameSpace,
	_ []types.GraphQuery,
) (*types.GraphData, error) {
	r.typedScopes = append(r.typedScopes, namespace)
	return &types.GraphData{}, nil
}

func TestSchemaQueryFallbackStaysInSelectedDocument(t *testing.T) {
	scope := types.NameSpace{KnowledgeBase: "kb", Knowledge: "doc"}
	repo := &schemaScopeGraphRepo{entityGraphRepo: &entityGraphRepo{graph: &types.GraphData{}}}
	plugin := &PluginSearchEntity{graphRepo: repo}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{SearchTargets: types.SearchTargets{{
			Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb", KnowledgeIDs: []string{"doc"},
		}}},
		PipelineState: types.PipelineState{
			EntityKBIDs:   []string{"kb"},
			EntityQueries: map[string][]types.GraphQuery{"kb": {{Name: "Alex", EntityType: "Person"}}},
		},
	}
	require.Nil(t, plugin.OnEvent(context.Background(), types.ENTITY_SEARCH, cm, func() *PluginError { return nil }))
	require.Equal(t, []types.NameSpace{scope}, repo.typedScopes)
	require.Equal(t, []types.NameSpace{scope}, repo.queries)
}

func TestParallelSearchIncludesSchemaOnlySeeds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entity  []string
		queries map[string][]types.GraphQuery
		wantHit bool
	}{
		{"schema only", nil, map[string][]types.GraphQuery{"kb-1": {{Name: "Alex", EntityType: "Person"}}}, true},
		{"legacy only", []string{"Alex"}, nil, true},
		{"no seeds", nil, nil, false},
		{"empty schema seeds", nil, map[string][]types.GraphQuery{"kb-1": {}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graphRepo := &entityGraphRepo{graph: &types.GraphData{Node: []*types.GraphNode{
				{Name: "Alex", Chunks: []string{"chunk"}},
			}}}
			plugin := &PluginSearchParallel{
				searchPlugin: &PluginSearch{knowledgeBaseService: &failingSearchKnowledgeBaseService{
					err: errors.New("keyword store unavailable"),
				}},
				searchEntityPlugin: &PluginSearchEntity{
					graphRepo: graphRepo,
					chunkRepo: &expandChunkRepo{chunks: map[string]*types.Chunk{
						"chunk": entityChunk("chunk", 1, "kb-1", "doc"),
					}},
					knowledgeRepo: &entityKnowledgeRepo{knowledge: map[string]*types.Knowledge{
						"doc": {ID: "doc", KnowledgeBaseID: "kb-1", Title: "Employment"},
					}},
				},
			}
			cm := &types.ChatManage{
				PipelineRequest: types.PipelineRequest{
					SearchTargets: types.SearchTargets{{
						Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1", TenantID: 1,
					}},
					EmbeddingTopK: 10,
				},
				PipelineState: types.PipelineState{
					RewriteQuery: "Where does Alex work?",
					Entity:       tc.entity, EntityQueries: tc.queries, EntityKBIDs: []string{"kb-1"},
				},
			}
			err := plugin.OnEvent(context.Background(), types.CHUNK_SEARCH_PARALLEL, cm,
				func() *PluginError { return nil })
			if tc.wantHit {
				require.Nil(t, err)
				require.Len(t, graphRepo.queries, 1)
				require.Len(t, cm.SearchResult, 1)
				require.Equal(t, "chunk", cm.SearchResult[0].ID)
			} else {
				require.NotNil(t, err)
				require.Empty(t, graphRepo.queries)
				require.Empty(t, cm.SearchResult)
			}
		})
	}
}
