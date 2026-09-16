package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type benchmarkChat struct {
	mu    sync.Mutex
	calls int
}

func (b *benchmarkChat) GetModelID() string   { return "benchmark-model" }
func (b *benchmarkChat) GetModelName() string { return "Benchmark Model" }

func (b *benchmarkChat) Chat(
	ctx context.Context,
	messages []chat.Message,
	options *chat.ChatOptions,
) (*types.ChatResponse, error) {
	b.mu.Lock()
	b.calls++
	callNumber := b.calls
	b.mu.Unlock()
	usage := types.TokenUsage{PromptTokens: 1200, CompletionTokens: 20, TotalTokens: 1220}
	if callNumber <= wikiCacheBenchmarkRepetitions {
		usage.SetPromptCacheUsage(0, 0, 1200, true)
	} else {
		usage.SetPromptCacheUsage(1000, 0, 200, true)
	}
	purpose, prefix := types.LLMCallMetadataFromContext(ctx)
	types.DispatchLLMCallObservation(ctx, types.LLMCallObservation{
		ModelType: types.ModelTypeKnowledgeQA, ModelID: b.GetModelID(), ModelName: b.GetModelName(),
		Purpose: purpose, PromptPrefixFingerprint: prefix,
		RequestFingerprint: chat.RequestFingerprint(ctx, messages, options),
		Usage:              usage, DurationMS: int64(callNumber * 10), Success: true,
	})
	return &types.ChatResponse{Content: "SUMMARY: benchmark\n# benchmark", Usage: usage}, nil
}

func (b *benchmarkChat) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func TestWikiCacheBenchmarkProducesStrictPromptFreeEvidence(t *testing.T) {
	t.Setenv("WEKNORA_MODEL_CALL_FINGERPRINT_KEY", "benchmark-secret")
	t.Setenv("WEKNORA_BUILD_COMMIT", "benchmark-commit")
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "benchmark.db")), &gorm.Config{})
	require.NoError(t, err)
	model := &benchmarkChat{}
	service := &EvaluationService{
		modelService:      &stubModelService{chatModel: model},
		evaluationStorage: newEvaluationStorage(db),
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	report, err := service.WikiCacheBenchmark(ctx, model.GetModelID())
	require.NoError(t, err)
	require.True(t, report.StrictValidation.Passed)
	require.Len(t, report.Cold.Calls, wikiCacheBenchmarkRepetitions)
	require.Len(t, report.Warm.Calls, wikiCacheBenchmarkRepetitions)
	require.Zero(t, report.Cold.Usage.CacheReadTokens)
	require.Greater(t, report.Warm.Usage.CacheReadTokens, 0)
	require.NotEmpty(t, report.ReportSHA256)
	for index := range report.Cold.Calls {
		require.Equal(t,
			report.Cold.Calls[index].RequestFingerprint,
			report.Warm.Calls[index].RequestFingerprint,
		)
		require.Len(t, report.Cold.Calls[index].RequestFingerprint, 64)
	}
	encoded, err := json.Marshal(report)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "RHINO-CACHE-2026 controlled Wiki benchmark")
	require.NotContains(t, string(encoded), "SOURCE GROUNDING")
}

func TestWikiCacheBenchmarkRequiresFingerprintKey(t *testing.T) {
	t.Setenv("WEKNORA_MODEL_CALL_FINGERPRINT_KEY", "")
	service := &EvaluationService{evaluationStorage: newEvaluationStorage(nil)}
	_, err := service.WikiCacheBenchmark(context.Background(), "model")
	require.EqualError(t, err, "WEKNORA_MODEL_CALL_FINGERPRINT_KEY must be configured")
}
