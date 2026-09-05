package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// DocumentPreviewService manages persistent preview generation and retrieval.
type DocumentPreviewService interface {
	Get(context.Context, uint64, string, bool) (*types.DocumentPreviewResult, error)
	Wake(context.Context, uint64, string)
	Handle(context.Context, *asynq.Task) error
	Run(context.Context)
}
