package service

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type usageReranker struct {
	inner    rerank.Reranker
	usage    interfaces.ModelUsageRepository
	tenantID uint64
}

func modelUsageEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("TOPIC3_MODEL_USAGE_ENABLED")))
	return value != "0" && value != "false" && value != "off"
}

func wrapRerankUsage(inner rerank.Reranker, usage interfaces.ModelUsageRepository, tenantID uint64) rerank.Reranker {
	if inner == nil || usage == nil || tenantID == 0 || !modelUsageEnabled() {
		return inner
	}
	return &usageReranker{inner: inner, usage: usage, tenantID: tenantID}
}

func (r *usageReranker) Rerank(ctx context.Context, query string, documents []string) ([]rerank.RankResult, error) {
	started := time.Now()
	results, err := r.inner.Rerank(ctx, query, documents)
	status := "success"
	errorMessage := ""
	if err != nil {
		status = "failed"
		errorMessage = err.Error()
		if len(errorMessage) > 512 {
			errorMessage = errorMessage[:512]
		}
	}
	purpose, fingerprint := types.LLMCallMetadataFromContext(ctx)
	_ = r.usage.Create(context.WithoutCancel(ctx), &types.ModelUsage{
		TenantID: r.tenantID, Model: r.GetModelName(), CallType: "rerank", ActualCalls: 1,
		Purpose: purpose, PromptFingerprint: fingerprint, InputCount: len(documents),
		CacheStatus: types.PromptCacheStatusUnsupported, Status: status,
		ErrorMessage: errorMessage, DurationMS: time.Since(started).Milliseconds(),
	})
	return results, err
}

func (r *usageReranker) GetModelName() string { return r.inner.GetModelName() }
func (r *usageReranker) GetModelID() string   { return r.inner.GetModelID() }
