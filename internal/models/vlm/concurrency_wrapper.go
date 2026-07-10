package vlm

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/limiter"
	"github.com/tiktoken-go/tokenizer"
)

type quotaVLM struct {
	inner  VLM
	key    string
	limits limiter.Limits
}

func (w *quotaVLM) GetModelName() string { return w.inner.GetModelName() }
func (w *quotaVLM) GetModelID() string   { return w.inner.GetModelID() }

func (w *quotaVLM) Predict(ctx context.Context, imgBytes [][]byte, prompt string) (string, error) {
	estimate := estimateVLMRequestTokens(w.inner.GetModelName(), prompt, len(imgBytes))
	permit, err := limiter.Admit(ctx, w.key, w.limits, estimate)
	if err != nil {
		return "", err
	}
	result, err := w.inner.Predict(ctx, imgBytes, prompt)
	permit.Release()
	return result, err
}

func wrapVLMConcurrency(v VLM, config *Config, err error) (VLM, error) {
	if err != nil || v == nil {
		return v, err
	}
	return &quotaVLM{
		inner: v,
		key:   limiter.QuotaKey(config.TenantID, v.GetModelID(), config.QuotaGroup),
		limits: limiter.Limits{
			MaxConcurrency:                config.MaxConcurrency,
			RequestsPerMinute:             config.RequestsPerMinute,
			TokensPerMinute:               config.TokensPerMinute,
			InteractiveConcurrencyReserve: config.InteractiveConcurrencyReserve,
		},
	}, nil
}

func estimateVLMRequestTokens(modelName, prompt string, images int) int {
	codec, err := tokenizer.ForModel(tokenizer.Model(strings.ToLower(strings.TrimSpace(modelName))))
	if err != nil {
		codec, _ = tokenizer.Get(tokenizer.Cl100kBase)
	}
	promptTokens := 0
	if codec != nil && prompt != "" {
		if ids, _, encodeErr := codec.Encode(prompt); encodeErr == nil {
			promptTokens = len(ids)
		} else {
			promptTokens = (len([]rune(prompt)) + 2) / 3
		}
	}
	return promptTokens + images*1024 + 4096
}
