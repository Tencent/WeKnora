package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type schemaExampleChat struct {
	graphReplyChat
	messages []chat.Message
}

func (c *schemaExampleChat) Chat(_ context.Context, messages []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	c.messages = append([]chat.Message(nil), messages...)
	return c.resp, nil
}

type schemaExampleGraphRepo struct {
	interfaces.RetrieveGraphRepository
	namespace types.NameSpace
	graphs    []*types.GraphData
}

func (r *schemaExampleGraphRepo) AddGraph(_ context.Context, namespace types.NameSpace, graphs []*types.GraphData) error {
	r.namespace, r.graphs = namespace, graphs
	return nil
}

type schemaExampleKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
}

func (r schemaExampleKnowledgeRepo) GetKnowledgeByIDOnly(_ context.Context, _ string) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func TestChunkExtractHandle_EmptyRelationOverrideReachesPrompt(t *testing.T) {
	schema := &types.GraphSchema{
		Enabled:     true,
		EntityTypes: []types.GraphEntityType{{Name: "Person"}},
	}
	knowledge := &types.Knowledge{ID: "doc1"}
	require.NoError(t, knowledge.SetProcessOverrides(&types.KnowledgeProcessOverrides{
		ExtractConfig: &types.ExtractConfig{
			Enabled: true, Schema: schema, Text: "Example person is mentioned.",
			Nodes:     []*types.GraphNode{{Name: "Example person", EntityType: "Person"}},
			Relations: []*types.GraphRelation{},
		},
	}))
	model := &schemaExampleChat{graphReplyChat: graphReplyChat{resp: &types.ChatResponse{
		Content: `{"entities":[{"id":"e1","name":"Actual person","type":"Person"}],"relations":[]}`, FinishReason: "stop",
	}}}
	repo := &schemaExampleGraphRepo{}
	svc := &ChunkExtractService{
		template:      &types.PromptTemplateStructured{Description: "legacy prompt"},
		modelService:  &stubModelService{chatModel: model},
		knowledgeRepo: schemaExampleKnowledgeRepo{knowledge: knowledge},
		knowledgeBaseRepo: graphKBRepo{kb: &types.KnowledgeBase{
			ID: "kb1",
			ExtractConfig: &types.ExtractConfig{
				Enabled: true,
				Schema: &types.GraphSchema{
					Enabled:       true,
					EntityTypes:   []types.GraphEntityType{{Name: "Person"}, {Name: "Company"}},
					RelationTypes: []types.GraphRelationType{{Name: "WORKS_AT", SourceType: "Person", TargetType: "Company"}},
				},
				Text: "Alex works at Acme.",
				Nodes: []*types.GraphNode{
					{Name: "Alex", EntityType: "Person"}, {Name: "Acme", EntityType: "Company"},
				},
				Relations: []*types.GraphRelation{
					{Node1: "Alex", Node2: "Acme", Type: "WORKS_AT", SourceType: "Person", TargetType: "Company"},
				},
			},
		}},
		chunkRepo: graphChunkRepo{chunk: &types.Chunk{
			ID: "c1", KnowledgeBaseID: "kb1", KnowledgeID: "doc1", Content: "Actual person is mentioned.",
		}},
		graphEngine: repo,
	}
	// Legacy task payloads resolve document overrides through the chunk's KnowledgeID.
	payload, err := json.Marshal(types.ExtractChunkPayload{TenantID: 1, ChunkID: "c1", ModelID: "m1"})
	require.NoError(t, err)
	require.NoError(t, svc.Handle(context.Background(), asynq.NewTask(types.TypeChunkExtract, payload)))
	require.Len(t, model.messages, 4, "the entity-only override sample must survive persistence and reach the prompt")
	require.Equal(t, "Example person is mentioned.", model.messages[1].Content)
	validator, err := types.CompileGraphJSONSchema(schema.ExtractionJSONSchema())
	require.NoError(t, err)
	sample, err := schema.ParseExtraction(model.messages[2].Content, validator)
	require.NoError(t, err)
	require.Len(t, sample.Node, 1)
	require.Equal(t, "Example person", sample.Node[0].Name)
	require.Empty(t, sample.Relation)
	require.Equal(t, types.NameSpace{KnowledgeBase: "kb1", Knowledge: "doc1"}, repo.namespace)
	require.Len(t, repo.graphs, 1)
	require.Len(t, repo.graphs[0].Node, 1)
	require.Equal(t, "Actual person", repo.graphs[0].Node[0].Name)
	require.Empty(t, repo.graphs[0].Relation)
}

func TestChunkExtractHandle_SchemaExamplesReachPrompt(t *testing.T) {
	schema := &types.GraphSchema{
		Enabled:       true,
		EntityTypes:   []types.GraphEntityType{{Name: "Person"}, {Name: "Company"}},
		RelationTypes: []types.GraphRelationType{{Name: "WORKS_AT", SourceType: "Person", TargetType: "Company"}},
	}
	for _, tc := range []struct {
		name       string
		entityType string
		wantSample bool
	}{
		{"valid typed sample", "Person", true},
		{"old untyped sample", "", false},
		{"sample from previous schema", "Unknown", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &schemaExampleChat{graphReplyChat: graphReplyChat{resp: &types.ChatResponse{
				Content: `{"entities":[{"id":"e1","name":"Actual person","type":"Person"}],"relations":[]}`, FinishReason: "stop",
			}}}
			repo := &schemaExampleGraphRepo{}
			svc := &ChunkExtractService{
				template:     &types.PromptTemplateStructured{Description: "legacy prompt"},
				modelService: &stubModelService{chatModel: model},
				knowledgeBaseRepo: graphKBRepo{kb: &types.KnowledgeBase{
					ID: "kb1",
					ExtractConfig: &types.ExtractConfig{
						Enabled: true, Schema: schema, Text: "Alex works at Alex.",
						Nodes: []*types.GraphNode{
							{Name: "Alex", EntityType: tc.entityType},
							{Name: "Alex", EntityType: "Company"},
						},
						Relations: []*types.GraphRelation{
							{Node1: "Alex", Node2: "Alex", Type: "WORKS_AT", SourceType: "Person", TargetType: "Company"},
						},
					},
				}},
				chunkRepo: graphChunkRepo{chunk: &types.Chunk{
					ID: "c1", KnowledgeBaseID: "kb1", KnowledgeID: "doc1", Content: "Actual person is mentioned.",
				}},
				graphEngine: repo,
			}
			payload, err := json.Marshal(types.ExtractChunkPayload{TenantID: 1, ChunkID: "c1", ModelID: "m1"})
			require.NoError(t, err)
			require.NoError(t, svc.Handle(context.Background(), asynq.NewTask(types.TypeChunkExtract, payload)))
			if tc.wantSample {
				require.Len(t, model.messages, 4)
				require.Equal(t, "user", model.messages[1].Role)
				require.Equal(t, "Alex works at Alex.", model.messages[1].Content)
				require.Equal(t, "assistant", model.messages[2].Role)
				validator, err := types.CompileGraphJSONSchema(schema.ExtractionJSONSchema())
				require.NoError(t, err)
				sample, err := schema.ParseExtraction(model.messages[2].Content, validator)
				require.NoError(t, err)
				require.Len(t, sample.Node, 2)
				require.Equal(t, "Person", sample.Relation[0].SourceType)
				require.Equal(t, "Company", sample.Relation[0].TargetType)
			} else {
				require.Len(t, model.messages, 2, "incompatible examples must not enter the prompt")
			}
			require.Equal(t, "Actual person is mentioned.", model.messages[len(model.messages)-1].Content)
			require.Equal(t, types.NameSpace{KnowledgeBase: "kb1", Knowledge: "doc1"}, repo.namespace)
			require.Len(t, repo.graphs, 1)
			require.Len(t, repo.graphs[0].Node, 1, "few-shot entities must not be persisted as document entities")
			require.Equal(t, "Actual person", repo.graphs[0].Node[0].Name)
			require.Equal(t, []string{"c1"}, repo.graphs[0].Node[0].Chunks)
		})
	}
}
