package interfaces

import (
	"context"
)

type WikiGraphCacheKey struct {
	ContentHash   string
	ChatModelID   string
	PromptVersion string
}

type WikiGraphCache interface {
	Get(ctx context.Context, key WikiGraphCacheKey) ([]byte, bool, error)
	Set(ctx context.Context, key WikiGraphCacheKey, val []byte) error
}
type WikiDocMapCacheKey struct {
	ContentHash   string
	Granularity   string
	ChatModelID   string
	PromptVersion string
}

type WikiDocMapCache interface {
	Get(ctx context.Context, key WikiDocMapCacheKey) ([]byte, bool, error)
	Set(ctx context.Context, key WikiDocMapCacheKey, val []byte) error
}
