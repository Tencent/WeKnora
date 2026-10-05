package embedding

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type resumeEmbedder struct {
	Embedder
	calls int
	fail  bool
}

func (e *resumeEmbedder) GetModelID() string   { return "resume-test" }
func (e *resumeEmbedder) GetModelName() string { return "test" }
func (e *resumeEmbedder) GetDimensions() int   { return 2 }
func (e *resumeEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	e.calls++
	if e.fail {
		return nil, fmt.Errorf("429")
	}
	out := make([][]float32, len(texts))
	for i, s := range texts {
		out[i] = []float32{float32(len(s)), 1}
	}
	return out, nil
}

func TestEmbeddingResumesSuccessfulTexts(t *testing.T) {
	t.Setenv("WEKNORA_PROCESSING_CHECKPOINT_DIR", t.TempDir())
	e := &resumeEmbedder{}
	w := &concurrencyEmbedder{inner: e, cacheScope: "same-model"}
	ctx := types.WithBackgroundTask(context.Background())
	if _, err := w.BatchEmbed(ctx, []string{"done"}); err != nil {
		t.Fatal(err)
	}
	e.fail = true
	if _, err := w.BatchEmbed(ctx, []string{"failed"}); err == nil {
		t.Fatal("expected failure")
	}
	e.fail = false
	got, err := w.BatchEmbed(ctx, []string{"done", "failed"})
	if err != nil || len(got) != 2 || e.calls != 3 {
		t.Fatalf("resume got=%v calls=%d err=%v", got, e.calls, err)
	}
	e.fail = true
	if _, err = w.BatchEmbed(ctx, []string{"done", "failed"}); err != nil || e.calls != 3 {
		t.Fatalf("finished batches called provider again: %v", err)
	}
}

func (e *resumeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.calls++
	return []float32{float32(len(text)), 1}, nil
}

func TestInteractiveEmbeddingBypassesBackgroundCooldown(t *testing.T) {
	e := &resumeEmbedder{}
	budget := &tokenBudget{slot: make(chan struct{}, 1), next: time.Now().Add(time.Minute)}
	budget.slot <- struct{}{}
	w := &concurrencyEmbedder{inner: e, budget: budget}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := w.Embed(ctx, "query"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.BatchEmbed(ctx, []string{"query"}); err != nil {
		t.Fatal(err)
	}
	if e.calls != 2 {
		t.Fatal("interactive calls waited on ingestion")
	}
}

func TestEmbeddingResumeTracksModelInputSettings(t *testing.T) {
	t.Setenv("WEKNORA_PROCESSING_CHECKPOINT_DIR", t.TempDir())
	config := Config{Source: "remote", ModelID: "same-model", ModelName: "embedding", Dimensions: 2}
	inner := &resumeEmbedder{}
	wrapper := &concurrencyEmbedder{inner: inner, cacheScope: embeddingCheckpointScope(config)}
	ctx := types.WithBackgroundTask(context.Background())
	if _, err := wrapper.BatchEmbed(ctx, []string{"done"}); err != nil {
		t.Fatal(err)
	}
	config.MaxConcurrency = 2
	config.APIKey = "rotated"
	wrapper.cacheScope = embeddingCheckpointScope(config)
	inner.fail = true
	if _, err := wrapper.BatchEmbed(ctx, []string{"done"}); err != nil {
		t.Fatal("credential/concurrency edit invalidated completed vectors")
	}
	config.TruncatePromptTokens = 1
	wrapper.cacheScope = embeddingCheckpointScope(config)
	if _, err := wrapper.BatchEmbed(ctx, []string{"done"}); err == nil {
		t.Fatal("input truncation edit reused an outdated vector")
	}
}
