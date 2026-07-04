package interfaces

import (
	"context"
)

type VLMCacheKey struct {
	ImageHash     string
	ModelID       string
	PromptVersion string
}

type VLMCacheComputeFunc func() (string, error)

type VlmCacheRepo interface {
	GetOrCompute(ctx context.Context, key VLMCacheKey, computeFn VLMCacheComputeFunc) (string, bool, error)
}
