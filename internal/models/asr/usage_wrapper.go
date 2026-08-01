package asr

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/modelusage"
	"github.com/Tencent/WeKnora/internal/types"
)

// usageASR records every Transcribe call as a model usage event. ASR billing
// is not token-based, so token counters stay zero and usage_source is
// not_applicable.
type usageASR struct {
	inner    ASR
	source   types.ModelSource
	provider string
}

func (u *usageASR) GetModelName() string { return u.inner.GetModelName() }
func (u *usageASR) GetModelID() string   { return u.inner.GetModelID() }

func (u *usageASR) Transcribe(ctx context.Context, audioBytes []byte, fileName string) (*TranscriptionResult, error) {
	start := time.Now()
	result, err := u.inner.Transcribe(ctx, audioBytes, fileName)
	modelusage.Record(ctx, types.ModelUsageEvent{
		ModelID:     u.inner.GetModelID(),
		ModelName:   u.inner.GetModelName(),
		ModelType:   types.ModelTypeASR,
		ModelSource: u.source,
		Provider:    u.provider,
		RequestKind: "asr.transcribe",
		UsageSource: types.ModelUsageSourceNotApplicable,
		InputItems:  1,
		DurationMs:  time.Since(start).Milliseconds(),
		Success:     err == nil,
		ErrorType:   modelusage.ErrorType(err),
	})
	return result, err
}

// wrapASRUsage applies the usage-recording decorator.
func wrapASRUsage(a ASR, err error, config *Config) (ASR, error) {
	if err != nil || a == nil {
		return a, err
	}
	source := types.ModelSource("")
	provider := ""
	if config != nil {
		source = config.Source
		provider = config.Provider
	}
	return &usageASR{inner: a, source: source, provider: provider}, nil
}
