package vlm

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/modelusage"
	"github.com/Tencent/WeKnora/internal/types"
)

// usageVLM records every Predict call as a model usage event. VLMs don't
// return token usage, so tokens are estimated (~runes/4, same approximation
// as the langfuse wrapper) and flagged as estimated.
type usageVLM struct {
	inner    VLM
	source   types.ModelSource
	provider string
}

func (u *usageVLM) GetModelName() string { return u.inner.GetModelName() }
func (u *usageVLM) GetModelID() string   { return u.inner.GetModelID() }

func (u *usageVLM) Predict(ctx context.Context, imgBytes [][]byte, prompt string) (string, error) {
	start := time.Now()
	result, err := u.inner.Predict(ctx, imgBytes, prompt)
	promptTokens := int64(len([]rune(prompt))/4 + 1)
	completionTokens := int64(len([]rune(result)) / 4)
	modelusage.Record(ctx, types.ModelUsageEvent{
		ModelID:          u.inner.GetModelID(),
		ModelName:        u.inner.GetModelName(),
		ModelType:        types.ModelTypeVLLM,
		ModelSource:      u.source,
		Provider:         u.provider,
		RequestKind:      "vlm.predict",
		UsageSource:      types.ModelUsageSourceEstimated,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
		InputItems:       len(imgBytes),
		DurationMs:       time.Since(start).Milliseconds(),
		Success:          err == nil,
		ErrorType:        modelusage.ErrorType(err),
	})
	return result, err
}

// wrapVLMUsage applies the usage-recording decorator.
func wrapVLMUsage(v VLM, config *Config) VLM {
	if v == nil {
		return v
	}
	source := types.ModelSource("")
	provider := ""
	if config != nil {
		source = config.Source
		provider = config.Provider
	}
	return &usageVLM{inner: v, source: source, provider: provider}
}
