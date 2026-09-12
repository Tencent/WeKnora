package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func evaluationExperimentInputFixture() *EvaluationExperimentInput {
	seed := 0
	return &EvaluationExperimentInput{
		Dataset: &types.EvaluationDatasetSnapshot{
			DatasetID:        "default",
			DatasetVersionID: "version-1",
			VersionNumber:    1,
			ArtifactSHA256:   strings.Repeat("a", 64),
			ContentSHA256:    strings.Repeat("b", 64),
		},
		SourceKnowledgeBaseID: "kb-source-1",
		EmbeddingModel: &types.Model{
			ID: "embedding-1", Name: "bge-m3", Type: types.ModelTypeEmbedding,
			Source:    types.ModelSourceLocal,
			UpdatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			Parameters: types.ModelParameters{
				Provider: "ollama", InterfaceType: "ollama",
				EmbeddingParameters: types.EmbeddingParameters{Dimension: 1024},
			},
		},
		ChatModel: &types.Model{
			ID: "chat-1", Name: "qwen3", Type: types.ModelTypeKnowledgeQA,
			Source:    types.ModelSourceOpenAI,
			UpdatedAt: time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
			Parameters: types.ModelParameters{
				BaseURL: "https://user:pw@api.example.test/v1", APIKey: "sk-hidden",
				Provider: "openai", InterfaceType: "openai",
			},
		},
		Params: &types.ChatManage{
			PipelineRequest: types.PipelineRequest{
				VectorThreshold: 0.7, KeywordThreshold: 0.5, EmbeddingTopK: 10,
				RerankTopK: 5, RerankThreshold: 0.6, ChatModelID: "chat-1",
				SummaryConfig: types.SummaryConfig{
					MaxTokens: 2048, Temperature: 0.7, TopP: 0.9, Seed: seed,
				},
			},
		},
		SeedProvided: true,
		SeedSupport:  types.EvaluationSeedSupportUnavailable,
		DBDriver:     "postgres",
	}
}

func TestBuildEvaluationExperimentSnapshotFreezesManifest(t *testing.T) {
	snapshot, hash, err := BuildEvaluationExperimentSnapshot(evaluationExperimentInputFixture())
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	assert.Equal(t, types.EvaluationExperimentSchemaVersion, snapshot.SchemaVersion)
	assert.Len(t, hash, 64)

	assert.Equal(t, "version-1", snapshot.Dataset.DatasetVersionID)
	require.NotNil(t, snapshot.SourceKnowledgeBaseID)
	assert.Equal(t, "kb-source-1", *snapshot.SourceKnowledgeBaseID)

	require.NotNil(t, snapshot.Models.Chat)
	assert.Equal(t, "chat-1", snapshot.Models.Chat.ID)
	input := evaluationExperimentInputFixture()
	assert.Equal(t, types.EvaluationModelConfigSHA256(input.ChatModel), snapshot.Models.Chat.ConfigSHA256)
	require.NotNil(t, snapshot.Models.Embedding)
	assert.Equal(t, types.EvaluationModelConfigSHA256(input.EmbeddingModel),
		snapshot.Models.Embedding.ConfigSHA256)

	// The manifest carries the frozen metric plan with twelve entries.
	require.NotNil(t, snapshot.MetricPlan)
	assert.Len(t, snapshot.MetricPlan.Metrics, 12)
	assert.True(t, strings.HasPrefix(snapshot.MetricPlan.PlanHash, "sha256:"))
	for _, spec := range snapshot.MetricPlan.Metrics {
		assert.Contains(t, spec.InstanceID, "@")
		assert.Contains(t, spec.InstanceID, "#sha256:")
	}

	// Seed visibility: provided but not applied by the provider yet.
	require.NotNil(t, snapshot.Configuration.Generation.Seed)
	assert.Equal(t, 0, *snapshot.Configuration.Generation.Seed)
	assert.True(t, snapshot.Configuration.Generation.SeedProvided)
	assert.Equal(t, types.EvaluationSeedSupportUnavailable, snapshot.Configuration.Generation.SeedSupport)
	assert.Equal(t, types.EvaluationReproducibilityPartial, snapshot.Reproducibility.Level)
	assert.NotEmpty(t, snapshot.Reproducibility.Warnings)
}

func TestEvaluationExperimentHashReflectsAnyParameterDifference(t *testing.T) {
	_, baseline, err := BuildEvaluationExperimentSnapshot(evaluationExperimentInputFixture())
	require.NoError(t, err)

	cases := []struct {
		name   string
		mutate func(*EvaluationExperimentInput)
	}{
		{"dataset version", func(in *EvaluationExperimentInput) { in.Dataset.DatasetVersionID = "version-2" }},
		{"source knowledge base", func(in *EvaluationExperimentInput) { in.SourceKnowledgeBaseID = "kb-other" }},
		{"missing source knowledge base", func(in *EvaluationExperimentInput) { in.SourceKnowledgeBaseID = "" }},
		{"chat model", func(in *EvaluationExperimentInput) { in.ChatModel.ID = "chat-2" }},
		{"chat model config", func(in *EvaluationExperimentInput) { in.ChatModel.Parameters.Provider = "generic" }},
		{"embedding model", func(in *EvaluationExperimentInput) {
			in.EmbeddingModel.Parameters.EmbeddingParameters.Dimension = 512
		}},
		{"retrieval topk", func(in *EvaluationExperimentInput) { in.Params.EmbeddingTopK = 20 }},
		{"rerank threshold", func(in *EvaluationExperimentInput) { in.Params.RerankThreshold = 0.5 }},
		{"temperature", func(in *EvaluationExperimentInput) { in.Params.SummaryConfig.Temperature = 0.1 }},
		{"seed value", func(in *EvaluationExperimentInput) { in.Params.SummaryConfig.Seed = 42 }},
		{"seed provided", func(in *EvaluationExperimentInput) { in.SeedProvided = false }},
		{"seed support", func(in *EvaluationExperimentInput) { in.SeedSupport = types.EvaluationSeedSupportApplied }},
		{"db driver", func(in *EvaluationExperimentInput) { in.DBDriver = "sqlite" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := evaluationExperimentInputFixture()
			tc.mutate(input)
			_, got, err := BuildEvaluationExperimentSnapshot(input)
			require.NoError(t, err)
			assert.NotEqual(t, baseline, got, "experiment hash must change after %s", tc.name)
		})
	}
}

func TestEvaluationExperimentCanonicalJSONIsDeterministic(t *testing.T) {
	snapshot, hash, err := BuildEvaluationExperimentSnapshot(evaluationExperimentInputFixture())
	require.NoError(t, err)
	first, err := snapshot.CanonicalJSON()
	require.NoError(t, err)
	second, err := snapshot.Clone().CanonicalJSON()
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second))

	recomputed, err := snapshot.SHA256()
	require.NoError(t, err)
	assert.Equal(t, hash, recomputed)
}

func TestAssertEvaluationExperimentSecretFree(t *testing.T) {
	snapshot, _, err := BuildEvaluationExperimentSnapshot(evaluationExperimentInputFixture())
	require.NoError(t, err)
	require.NoError(t, AssertEvaluationExperimentSecretFree(snapshot))

	canonical, err := snapshot.CanonicalJSON()
	require.NoError(t, err)
	for _, secret := range []string{"sk-hidden", "user:pw", `"api_key"`, `"custom_headers"`} {
		assert.NotContains(t, string(canonical), secret)
	}

	// A snapshot missing a required model role fails the invariant check.
	missingChat := snapshot.Clone()
	missingChat.Models.Chat = nil
	require.Error(t, AssertEvaluationExperimentSecretFree(missingChat))

	// An empty-string source knowledge base must be null instead.
	emptySource := snapshot.Clone()
	empty := ""
	emptySource.SourceKnowledgeBaseID = &empty
	require.Error(t, AssertEvaluationExperimentSecretFree(emptySource))
}

func TestSelectEvaluationDefaultModelIsDeterministic(t *testing.T) {
	models := []*types.Model{
		{ID: "chat-b", Type: types.ModelTypeKnowledgeQA},
		{ID: "chat-a", Type: types.ModelTypeKnowledgeQA},
		{ID: "embedding-1", Type: types.ModelTypeEmbedding, IsDefault: true},
		{ID: "embedding-0", Type: types.ModelTypeEmbedding},
	}
	// Selection does not depend on the input order.
	for i := 0; i < 5; i++ {
		chat := SelectEvaluationDefaultModel(models, types.ModelTypeKnowledgeQA)
		require.NotNil(t, chat)
		assert.Equal(t, "chat-a", chat.ID)
		embedding := SelectEvaluationDefaultModel(models, types.ModelTypeEmbedding)
		require.NotNil(t, embedding)
		assert.Equal(t, "embedding-1", embedding.ID)
		models[0], models[1] = models[1], models[0]
	}
	assert.Nil(t, SelectEvaluationDefaultModel(models, types.ModelTypeRerank))
}

func TestVerifyEvaluationModelFingerprintDetectsDrift(t *testing.T) {
	model := &types.Model{
		ID: "chat-1", Name: "qwen3", Type: types.ModelTypeKnowledgeQA,
		Parameters: types.ModelParameters{Provider: "openai", InterfaceType: "openai"},
	}
	frozen := types.EvaluationModelSnapshotFrom(model)

	require.NoError(t, VerifyEvaluationModelFingerprint(context.Background(), "chat", frozen, model))

	drifted := &types.Model{
		ID: "chat-1", Name: "qwen3", Type: types.ModelTypeKnowledgeQA,
		Parameters: types.ModelParameters{Provider: "generic", InterfaceType: "openai"},
	}
	require.Error(t, VerifyEvaluationModelFingerprint(context.Background(), "chat", frozen, drifted))

	require.Error(t, VerifyEvaluationModelFingerprint(context.Background(), "chat", frozen, nil))
	require.NoError(t, VerifyEvaluationModelFingerprint(context.Background(), "chat", nil, nil))
}

func TestPersistEvaluationExperimentRoundTrip(t *testing.T) {
	snapshot, hash, err := BuildEvaluationExperimentSnapshot(evaluationExperimentInputFixture())
	require.NoError(t, err)

	entity := &types.EvaluationTaskEntity{}
	require.NoError(t, persistEvaluationExperiment(entity, snapshot, hash))
	require.NotNil(t, entity.DatasetVersionID)
	assert.Equal(t, "version-1", *entity.DatasetVersionID)
	require.NotNil(t, entity.DatasetContentSHA256)
	require.NotNil(t, entity.ExperimentSHA256)
	assert.Equal(t, hash, *entity.ExperimentSHA256)
	require.NotEmpty(t, entity.ExperimentSnapshot)

	// entityToDetail restores the manifest and marks provenance complete.
	restored, complete, err := decodeEvaluationExperiment(entity)
	require.NoError(t, err)
	require.True(t, complete)
	require.NotNil(t, restored)
	assert.Equal(t, "version-1", restored.Dataset.DatasetVersionID)
	require.NotNil(t, restored.SourceKnowledgeBaseID)
	assert.Equal(t, "kb-source-1", *restored.SourceKnowledgeBaseID)
	assert.Len(t, restored.MetricPlan.Metrics, 12)
}

func TestDecodeEvaluationExperimentNullProvenance(t *testing.T) {
	// Pre-M3 tasks have no snapshot columns; the wire reports experiment=null
	// and provenance_complete=false instead of an empty fabricated object.
	entity := &types.EvaluationTaskEntity{}
	experiment, complete, err := decodeEvaluationExperiment(entity)
	require.NoError(t, err)
	assert.Nil(t, experiment)
	assert.False(t, complete)

	entity.ExperimentSnapshot = types.JSON(`{"schema_version":1}`)
	experiment, complete, err = decodeEvaluationExperiment(entity)
	require.NoError(t, err)
	require.NotNil(t, experiment)
	assert.False(t, complete, "snapshot without dataset binding and hash is not complete provenance")
}

func TestSeedSupportForChatModelClassification(t *testing.T) {
	cases := []struct {
		name  string
		model *types.Model
		want  string
	}{
		{"openai", &types.Model{
			Source:     types.ModelSourceOpenAI,
			Parameters: types.ModelParameters{Provider: "openai"},
		}, types.EvaluationSeedSupportApplied},
		{"ollama", &types.Model{
			Source:     types.ModelSourceLocal,
			Parameters: types.ModelParameters{Provider: "ollama"},
		}, types.EvaluationSeedSupportApplied},
		{"anthropic", &types.Model{
			Source:     types.ModelSourceOpenAI,
			Parameters: types.ModelParameters{Provider: "anthropic"},
		}, types.EvaluationSeedSupportUnsupported},
		{"nil model", nil, types.EvaluationSeedSupportUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, seedSupportForChatModel(tc.model))
		})
	}
}

func TestBuildEvaluationExperimentRecordsAppliedSeed(t *testing.T) {
	input := evaluationExperimentInputFixture()
	input.SeedSupport = types.EvaluationSeedSupportApplied
	snapshot, _, err := BuildEvaluationExperimentSnapshot(input)
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationSeedSupportApplied, snapshot.Configuration.Generation.SeedSupport)
	require.NotNil(t, snapshot.Configuration.Generation.Seed)

	// Applied seed means auditable reproducibility without seed warnings.
	assert.Equal(t, types.EvaluationReproducibilityAuditable, snapshot.Reproducibility.Level)
	for _, warning := range snapshot.Reproducibility.Warnings {
		assert.NotContains(t, warning, "seed")
	}

	// Unrequested seed records the not_requested state and a null seed.
	unrequested := evaluationExperimentInputFixture()
	unrequested.SeedProvided = false
	unrequested.SeedSupport = types.EvaluationSeedSupportNotRequested
	unrequestedSnapshot, _, err := BuildEvaluationExperimentSnapshot(unrequested)
	require.NoError(t, err)
	assert.Nil(t, unrequestedSnapshot.Configuration.Generation.Seed)
	assert.False(t, unrequestedSnapshot.Configuration.Generation.SeedProvided)
	assert.Equal(t, types.EvaluationSeedSupportNotRequested,
		unrequestedSnapshot.Configuration.Generation.SeedSupport)
}
