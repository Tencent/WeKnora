package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func validFinalProfile() finalProfile {
	return finalProfile{
		SchemaVersion: 1, BenchmarkID: "benchmark_v1", BenchmarkVersion: "v1.1",
		Dataset: profileDataset{"dataset-sha", 32, 15, 15, 15},
		Models: profileModels{
			Embedding: profileModel{"text-embedding-v4", "Embedding", "remote", "generic", 1024},
			Chat:      profileModel{"deepseek-v4-pro", "KnowledgeQA", "remote", "generic", 0},
			Rerank:    profileModel{"qwen3-rerank", "Rerank", "remote", "aliyun", 0},
		},
		Runtime: profileRuntime{
			WorkerLimit: 27, CacheMode: "off",
			Retrieval:  profileRetrieval{0.2, 0.3, 30, 30, 0.3, "postgres"},
			Generation: profileGeneration{5, 16384, 0, 1, 0, 0, 0, 0, 0.3, 0, 2048},
		},
	}
}

func validPreflightEnvironment() preflightEnvironment {
	credential := "unit-test-credential-must-never-be-serialized"
	model := func(id, name string, typ types.ModelType, provider string) *types.Model {
		return &types.Model{
			ID: id, Name: name, Type: typ,
			Source: types.ModelSourceRemote, Status: types.ModelStatusActive,
			Parameters: types.ModelParameters{Provider: provider, APIKey: credential},
		}
	}
	embedding := model("embedding-id", "text-embedding-v4", types.ModelTypeEmbedding, "generic")
	embedding.Parameters.EmbeddingParameters.Dimension = 1024
	return preflightEnvironment{
		CommitSHA: "abc123",
		Dataset: types.EvaluationDatasetIdentity{
			DatasetID: "benchmark_v1", DatasetSemanticSHA256: "dataset-sha",
			CorpusCount: 32, QuestionCount: 15, QrelsCount: 15, AnswerCount: 15,
		},
		Models: []*types.Model{
			embedding,
			model("chat-id", "deepseek-v4-pro", types.ModelTypeKnowledgeQA, "generic"),
			model("rerank-id", "qwen3-rerank", types.ModelTypeRerank, "aliyun"),
		},
		Config: &appconfig.Config{Conversation: &appconfig.ConversationConfig{
			MaxRounds: 5, VectorThreshold: 0.2, KeywordThreshold: 0.3,
			EmbeddingTopK: 30, RerankTopK: 30, RerankThreshold: 0.3,
			Summary: &appconfig.SummaryConfig{
				MaxInputChars: 16384, RepeatPenalty: 1,
				Temperature: 0.3, MaxCompletionTokens: 2048,
			},
		}},
		RetrieveDriver: "postgres", WorkerLimit: 27,
	}
}

func TestFinalPreflightValidProfilePasses(t *testing.T) {
	client := httpDoerFunc(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "/health", r.URL.Path)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status":"ok"}`))}, nil
	})
	require.NoError(t, checkBackendWithClient(context.Background(), "http://backend", client))
	selected, err := validatePreflight(validFinalProfile(), validPreflightEnvironment())
	require.NoError(t, err)
	require.Equal(t, selectedModels{"embedding-id", "chat-id", "rerank-id"}, selected)
}

func TestCommittedFinalProfileLoads(t *testing.T) {
	profile, err := loadFinalProfile(filepath.Join("..", "..", "config", "benchmark", "final_v1.json"))
	require.NoError(t, err)
	require.Equal(t, "benchmark_v1", profile.BenchmarkID)
	require.Equal(t, "56fd363d797ee4c1524a5a1a2517b3b30ce955229c37784cf730c0d1dc47fd0d", profile.Dataset.SemanticSHA256)
	require.Equal(t, 27, profile.Runtime.WorkerLimit)
}

func TestFinalPreflightRejectsWrongModelsClearly(t *testing.T) {
	tests := []struct {
		name               string
		index              int
		replacement, label string
	}{
		{"embedding", 0, "bge-m3", "configured embedding model"},
		{"chat", 1, "other-chat", "configured chat/summary model"},
		{"rerank", 2, "other-rerank", "configured rerank model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validPreflightEnvironment()
			env.Models[tt.index].Name = tt.replacement
			_, err := validatePreflight(validFinalProfile(), env)
			require.ErrorContains(t, err, tt.label)
			require.ErrorContains(t, err, "cannot be compared")
		})
	}
}

func TestFinalPreflightRejectsDatasetSHAMismatch(t *testing.T) {
	env := validPreflightEnvironment()
	env.Dataset.DatasetSemanticSHA256 = "wrong"
	_, err := validatePreflight(validFinalProfile(), env)
	require.ErrorContains(t, err, "semantic SHA mismatch")
}

func TestFinalPreflightRejectsDatasetCountMismatch(t *testing.T) {
	env := validPreflightEnvironment()
	env.Dataset.CorpusCount = 31
	_, err := validatePreflight(validFinalProfile(), env)
	require.ErrorContains(t, err, "count mismatch")
}

func TestFinalPreflightRejectsExtraAutomaticSelectionRows(t *testing.T) {
	tests := []struct {
		name  string
		model *types.Model
	}{
		{"inactive embedding", &types.Model{
			ID: "historical-embedding", Name: "old-embedding", Type: types.ModelTypeEmbedding,
			Source: types.ModelSourceRemote, Status: types.ModelStatusDownloadFailed,
		}},
		{"extra KnowledgeQA", &types.Model{
			ID: "extra-chat", Name: "other-chat", Type: types.ModelTypeKnowledgeQA,
			Source: types.ModelSourceRemote, Status: types.ModelStatusActive,
			Parameters: types.ModelParameters{APIKey: "test"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validPreflightEnvironment()
			env.Models = append(env.Models, tt.model)
			_, err := validatePreflight(validFinalProfile(), env)
			require.ErrorContains(t, err, "selection is ambiguous")
		})
	}
}

func TestCustomPreflightAcceptsNonFinalModels(t *testing.T) {
	env := validPreflightEnvironment()
	env.Models[0].Name = "bge-m3"
	env.Models[0].Source = types.ModelSourceLocal
	env.Models[0].Parameters.APIKey = ""
	env.Models[1].Name = "another-chat"
	env.Models[2].Name = "another-rerank"
	selected, err := validateCustomPreflight(validFinalProfile(), env)
	require.NoError(t, err)
	require.Equal(t, selectedModels{"embedding-id", "chat-id", "rerank-id"}, selected)
}

func TestFinalPreflightRejectsUnavailableBackend(t *testing.T) {
	client := httpDoerFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") })
	err := checkBackendWithClient(context.Background(), "http://backend", client)
	require.ErrorContains(t, err, "backend unavailable")
}

type httpDoerFunc func(*http.Request) (*http.Response, error)

func (f httpDoerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestFinalPreflightRejectsUnwritableOutputPath(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))
	err := checkOutputWritable(filepath.Join(blocker, "result.json"))
	require.ErrorContains(t, err, "cannot be written")
}

func TestFinalArtifactDoesNotLeakCredentials(t *testing.T) {
	secret := "unit-test-credential-must-never-be-serialized"
	result := validBenchmarkResult()
	for _, mode := range []benchmarkExecutionMode{executionModeStrict, executionModeCustom} {
		artifact, err := buildBenchmarkArtifact(mode, validFinalProfile(), "abc123", time.Unix(0, 0), result)
		require.NoError(t, err)
		dir := t.TempDir()
		require.NoError(t, writeFinalArtifacts(dir, artifact))
		jsonData, err := os.ReadFile(filepath.Join(dir, "result.json"))
		require.NoError(t, err)
		mdData, err := os.ReadFile(filepath.Join(dir, "result.md"))
		require.NoError(t, err)
		combined := string(jsonData) + string(mdData)
		require.NotContains(t, combined, secret)
		require.False(t, strings.Contains(strings.ToLower(combined), "api_key"))
		require.False(t, strings.Contains(strings.ToLower(combined), "authorization"))
	}
}

func validBenchmarkResult() *types.BenchmarkResult {
	value := 1.0
	return &types.BenchmarkResult{
		BenchmarkVersion: "v1.1",
		Run:              types.BenchmarkRunSummary{EvaluationRunID: "run-1", Status: types.EvaluationStatueSuccess},
		Config: types.EvaluationConfigSnapshotV1{
			Dataset: types.EvaluationDatasetSnapshot{
				DatasetID: "benchmark_v1", DatasetSemanticSHA256: "dataset-sha",
				CorpusCount: 32, QuestionCount: 15, QrelsCount: 15, AnswerCount: 15,
			},
			Retrieval: types.EvaluationRetrievalSnapshot{
				VectorThreshold: 0.2, KeywordThreshold: 0.3,
				EmbeddingTopK: 30, RerankTopK: 30, RerankThreshold: 0.3,
				RetrieveDriver: "postgres",
			},
			Models: types.EvaluationModelsSnapshot{
				Embedding: &types.EvaluationConfiguredModelSnapshot{
					Name: "text-embedding-v4", Type: "Embedding", Source: "remote", Provider: "generic",
					Embedding: &types.EvaluationEmbeddingSnapshot{Dimension: 1024},
				},
				Chat: &types.EvaluationConfiguredModelSnapshot{
					ID: "chat-id", Name: "deepseek-v4-pro",
					Type: "KnowledgeQA", Source: "remote", Provider: "generic",
				},
				Summary: &types.EvaluationConfiguredModelSnapshot{
					ID: "chat-id", Name: "deepseek-v4-pro",
					Type: "KnowledgeQA", Source: "remote", Provider: "generic",
				},
				Rerank: &types.EvaluationConfiguredModelSnapshot{
					Name: "qwen3-rerank", Type: "Rerank", Source: "remote", Provider: "aliyun",
				},
			},
			Generation: types.EvaluationGenerationSnapshot{
				MaxRounds: 5,
				SummaryConfig: types.SummaryConfig{
					RepeatPenalty: 1, Temperature: 0.3, MaxCompletionTokens: 2048,
				},
			},
			Execution: types.EvaluationExecutionSnapshot{WorkerLimit: 27},
		},
		Quality: types.BenchmarkQuality{
			State: types.BenchmarkQualityStateComplete,
			Retrieval: &types.BenchmarkRetrievalQuality{
				Precision: &value, Recall: &value, NDCG3: &value, NDCG10: &value, MRR: &value, MAP: &value,
			},
			Answer: &types.BenchmarkAnswerQuality{
				BLEU1: &value, BLEU2: &value, BLEU4: &value, ROUGE1: &value, ROUGE2: &value, ROUGEL: &value,
			},
		},
		ModelFacts:      &types.EvaluationModelUsageAggregate{},
		Reproducibility: types.BenchmarkReproducibilityComplete,
	}
}

func TestBenchmarkArtifactFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*types.BenchmarkResult)
		want   string
	}{
		{"run status", func(r *types.BenchmarkResult) {
			r.Run.Status = types.EvaluationStatueRunning
		}, "run is not successful"},
		{"quality state", func(r *types.BenchmarkResult) {
			r.Quality.State = types.BenchmarkQualityStatePending
		}, "quality is not complete"},
		{"retrieval missing", func(r *types.BenchmarkResult) { r.Quality.Retrieval = nil }, "retrieval metrics are missing"},
		{"retrieval metric missing", func(r *types.BenchmarkResult) {
			r.Quality.Retrieval.MAP = nil
		}, "retrieval metric MAP is missing"},
		{"answer missing", func(r *types.BenchmarkResult) { r.Quality.Answer = nil }, "answer metrics are missing"},
		{"answer metric missing", func(r *types.BenchmarkResult) {
			r.Quality.Answer.ROUGEL = nil
		}, "answer metric ROUGE-L is missing"},
		{"reproducibility", func(r *types.BenchmarkResult) {
			r.Reproducibility = types.BenchmarkReproducibilityLegacyUnknown
		}, "reproducibility is not complete"},
		{"model facts", func(r *types.BenchmarkResult) { r.ModelFacts = nil }, "no model usage facts"},
	}
	for _, mode := range []benchmarkExecutionMode{executionModeStrict, executionModeCustom} {
		for _, tt := range tests {
			t.Run(string(mode)+"/"+tt.name, func(t *testing.T) {
				result := validBenchmarkResult()
				tt.mutate(result)
				_, err := buildBenchmarkArtifact(mode, validFinalProfile(), "abc123", time.Unix(0, 0), result)
				require.ErrorContains(t, err, tt.want)
			})
		}
	}
}

func TestCustomArtifactIsExplicitlyNonComparable(t *testing.T) {
	result := validBenchmarkResult()
	result.Config.Models.Embedding.Name = "bge-m3"
	result.Config.Models.Chat.Name = "another-chat"
	result.Config.Models.Summary.Name = "another-chat"
	result.Config.Models.Rerank.Name = "another-rerank"
	artifact, err := buildBenchmarkArtifact(executionModeCustom, validFinalProfile(), "abc123", time.Unix(0, 0), result)
	require.NoError(t, err)
	require.Equal(t, executionModeCustom, artifact.ExecutionMode)
	require.False(t, artifact.ComparableToFinalBaseline)
	jsonData, err := json.Marshal(artifact)
	require.NoError(t, err)
	require.JSONEq(
		t, `{"execution_mode":"custom","comparable_to_final_baseline":false}`,
		extractComparabilityJSON(t, jsonData),
	)
	markdown := renderFinalMarkdown(artifact)
	require.Contains(t, markdown, "Custom Verification Result")
	require.Contains(t, markdown, "WARNING:")
	require.Contains(t, markdown, "NOT directly comparable")
}

func TestStrictArtifactIsComparable(t *testing.T) {
	artifact, err := buildFinalArtifact(validFinalProfile(), "abc123", time.Unix(0, 0), validBenchmarkResult())
	require.NoError(t, err)
	require.Equal(t, executionModeStrict, artifact.ExecutionMode)
	require.True(t, artifact.ComparableToFinalBaseline)
	require.Contains(t, renderFinalMarkdown(artifact), "Comparable to published baseline: YES")
}

func TestMarkdownPreservesMissingLatencyAsNA(t *testing.T) {
	artifact, err := buildFinalArtifact(validFinalProfile(), "abc123", time.Unix(0, 0), validBenchmarkResult())
	require.NoError(t, err)
	jsonData, err := json.Marshal(artifact)
	require.NoError(t, err)
	require.Contains(t, string(jsonData), `"avg_ms":null`)
	markdown := renderFinalMarkdown(artifact)
	require.Contains(t, markdown, "Average model latency: n/a")
	require.NotContains(t, markdown, "0.00 ms")
}

func extractComparabilityJSON(t *testing.T, data []byte) string {
	t.Helper()
	var value map[string]any
	require.NoError(t, json.Unmarshal(data, &value))
	selected := map[string]any{
		"execution_mode":               value["execution_mode"],
		"comparable_to_final_baseline": value["comparable_to_final_baseline"],
	}
	result, err := json.Marshal(selected)
	require.NoError(t, err)
	return string(result)
}
