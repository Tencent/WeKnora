package embedding

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// Opt-in: loads an existing model with synthetic text, without pulling models
// or writing memory/index data. The model may remain resident for 30 minutes.
func TestLiveOllamaEmbeddingWarmBudget(t *testing.T) {
	base := os.Getenv("WEKNORA_OLLAMA_TEST_URL")
	model := os.Getenv("WEKNORA_OLLAMA_TEST_MODEL")
	if base == "" || model == "" {
		t.Skip("set WEKNORA_OLLAMA_TEST_URL and WEKNORA_OLLAMA_TEST_MODEL for a live warm-budget check")
	}
	t.Setenv("OLLAMA_BASE_URL", base)
	svc, err := ollama.GetOllamaService()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	available, err := svc.IsModelAvailable(ctx, model)
	require.NoError(t, err)
	require.True(t, available, "test must not pull a missing model")
	e, err := NewEmbedder(Config{
		Source: types.ModelSourceLocal, ModelName: model, TruncatePromptTokens: 511,
		ExtraConfig: map[string]string{"keep_alive": "30m"},
	}, nil, svc)
	require.NoError(t, err)
	start := time.Now()
	vector, err := e.Embed(ctx, "Synthetic embedding warm-up")
	require.NoError(t, err)
	require.NotEmpty(t, vector)
	t.Logf("warm-up: %s; dimensions=%d", time.Since(start), len(vector))
	for i := 0; i < 5; i++ {
		queryCtx, queryCancel := context.WithTimeout(types.WithEmbedQuery(context.Background()), 2*time.Second)
		start := time.Now()
		got, err := e.Embed(queryCtx, "合成诊断文本：查询图纸编号。")
		elapsed := time.Since(start)
		queryCancel()
		require.NoError(t, err, "warm request %d exceeded the memory recall budget", i+1)
		require.Len(t, got, len(vector))
		t.Logf("warm request %d: %s", i+1, elapsed)
	}
}
