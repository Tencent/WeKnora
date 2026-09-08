package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestEvaluationSnapshotV11MetadataAndSecretExclusion(t *testing.T) {
	model := &types.Model{
		ID: "embedding-id", Name: "embedding-name", Type: types.ModelTypeEmbedding,
		Source: types.ModelSourceOpenAI,
		Parameters: types.ModelParameters{
			Provider: "openai", InterfaceType: "openai", APIKey: "must-not-leak",
			AppSecret: "must-not-leak-either", BaseURL: "https://credential-bearing.invalid",
			CustomHeaders: map[string]string{"Authorization": "secret"},
			ExtraConfig:   map[string]string{"secret": "hidden"},
			EmbeddingParameters: types.EmbeddingParameters{
				Dimension: 1024, TruncatePromptTokens: 8192, SupportsDimensionOverride: true,
			},
		},
	}
	safe, err := configuredModelSnapshot(model, true)
	require.NoError(t, err)
	models := types.EvaluationModelsSnapshot{
		EmbeddingModelID: model.ID, ChatModelID: "chat-id", SummaryModelID: "summary-id",
		Embedding: safe,
		Chat: &types.EvaluationConfiguredModelSnapshot{
			ID: "chat-id", Name: "chat", Type: "knowledge_qa", Source: "openai",
		},
		Summary: &types.EvaluationConfiguredModelSnapshot{
			ID: "summary-id", Name: "summary", Type: "knowledge_qa", Source: "openai",
		},
	}
	detail := &types.EvaluationDetail{
		Task: &types.EvaluationTask{DatasetID: "benchmark_v1"},
		Params: &types.ChatManage{PipelineRequest: types.PipelineRequest{
			ChatModelID: "chat-id", EmbeddingTopK: 30, RerankTopK: 20,
		}},
	}
	snapshot := evaluationSnapshot(
		detail,
		types.EvaluationDatasetIdentity{
			DatasetID: "benchmark_v1", DatasetSemanticSHA256: strings.Repeat("a", 64),
			CorpusCount: 32, QuestionCount: 15, QrelsCount: 15, AnswerCount: 15,
		},
		nil, models, "postgres", types.EvaluationTokenizerSnapshot{Name: "jieba", DictionaryMode: "builtin"}, 3,
	)

	require.Equal(t, types.BenchmarkContractVersionV11, snapshot.BenchmarkContractVersion)
	require.Equal(t, 32, snapshot.Dataset.CorpusCount)
	require.Equal(t, 15, snapshot.Dataset.QuestionCount)
	require.Equal(t, "pre_chunked_passages", snapshot.Dataset.CorpusMode)
	require.False(t, snapshot.Dataset.ChunkingApplied)
	require.Equal(t, 3, snapshot.Execution.WorkerLimit)
	require.Equal(t, 1024, snapshot.Models.Embedding.Embedding.Dimension)
	require.Equal(t, 8192, snapshot.Models.Embedding.Embedding.TruncatePromptTokens)

	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range []string{
		"must-not-leak", "api_key", "app_secret", "authorization", "custom_headers",
		"extra_config", "credential-bearing.invalid",
	} {
		require.NotContains(t, lower, forbidden)
	}
}

func TestEndpointFingerprintNormalizationAndSecretExclusion(t *testing.T) {
	fingerprint := func(endpoint string) string {
		t.Helper()
		model := &types.Model{
			ID: "remote", Name: "model", Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote,
			Parameters: types.ModelParameters{BaseURL: endpoint, APIKey: "api-secret"},
		}
		snapshot, err := configuredModelSnapshot(model, false)
		require.NoError(t, err)
		require.Len(t, snapshot.EndpointFingerprint, 64)
		encoded, err := json.Marshal(snapshot)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "api-secret")
		require.NotContains(t, string(encoded), "query-secret")
		require.NotContains(t, string(encoded), "user-secret")
		return snapshot.EndpointFingerprint
	}

	canonical := fingerprint("https://EXAMPLE.com:443/api/v1/")
	require.Equal(t, canonical, fingerprint("https://example.com/api/v1"))
	require.Equal(
		t, canonical,
		fingerprint("https://user-secret:password@example.com/api/v1?token=query-secret#fragment"),
	)
	require.NotEqual(t, canonical, fingerprint("https://other.example.com/api/v1"))
	require.NotEqual(t, canonical, fingerprint("https://example.com/api/v2"))
}

func TestConfiguredModelSnapshotRemoteEndpointAndSharedModelIdentity(t *testing.T) {
	model := &types.Model{
		ID: "shared", Name: "shared-name", Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{Provider: "openai"},
	}
	chat, err := configuredModelSnapshot(model, false)
	require.NoError(t, err)
	summary, err := configuredModelSnapshot(model, false)
	require.NoError(t, err)
	require.NotEmpty(t, chat.EndpointFingerprint)
	require.Equal(t, chat.EndpointFingerprint, summary.EndpointFingerprint)

	local := *model
	local.Source = types.ModelSourceLocal
	localSnapshot, err := configuredModelSnapshot(&local, false)
	require.NoError(t, err)
	require.Empty(t, localSnapshot.EndpointFingerprint)
}

func TestEvaluationTokenizerSnapshotBuiltinAndCustomFingerprint(t *testing.T) {
	t.Setenv("JIEBA_DICT_DIR", "")
	builtin, err := evaluationTokenizerSnapshot()
	require.NoError(t, err)
	require.Equal(t, "jieba", builtin.Name)
	require.Equal(t, "builtin", builtin.DictionaryMode)
	require.Empty(t, builtin.DictionaryFingerprint)

	writeDictionaries := func(directory, suffix string) {
		t.Helper()
		for _, name := range jiebaDictionaryFiles {
			require.NoError(t, os.WriteFile(filepath.Join(directory, name), []byte(name+suffix), 0o600))
		}
	}
	firstDir, secondDir := t.TempDir(), t.TempDir()
	writeDictionaries(firstDir, "-same")
	writeDictionaries(secondDir, "-same")

	t.Setenv("JIEBA_DICT_DIR", firstDir)
	first, err := evaluationTokenizerSnapshot()
	require.NoError(t, err)
	require.Equal(t, "custom", first.DictionaryMode)
	require.Len(t, first.DictionaryFingerprint, 64)

	t.Setenv("JIEBA_DICT_DIR", secondDir)
	second, err := evaluationTokenizerSnapshot()
	require.NoError(t, err)
	require.Equal(t, first.DictionaryFingerprint, second.DictionaryFingerprint,
		"absolute dictionary path must not affect identity")

	require.NoError(t, os.WriteFile(filepath.Join(secondDir, jiebaDictionaryFiles[0]), []byte("changed"), 0o600))
	changed, err := evaluationTokenizerSnapshot()
	require.NoError(t, err)
	require.NotEqual(t, first.DictionaryFingerprint, changed.DictionaryFingerprint)
}
