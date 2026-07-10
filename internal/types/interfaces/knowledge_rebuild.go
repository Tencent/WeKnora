package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type KnowledgeRebuildRunRepository interface {
	Start(ctx context.Context, run *types.KnowledgeRebuildRun) error
	Get(ctx context.Context, tenantID uint64, runID string) (*types.KnowledgeRebuildRun, error)
	IsCurrent(ctx context.Context, tenantID uint64, knowledgeID, runID string, attempt int) (bool, error)
	BindAttempt(ctx context.Context, tenantID uint64, runID string, attempt int) error
	SetStatus(ctx context.Context, tenantID uint64, runID, status, errorMessage string) error
	RecordParseResult(ctx context.Context, tenantID uint64, runID, cacheKey string, cacheHit, success, terminal bool, errorMessage string) error
	ReplaceChunkResults(ctx context.Context, tenantID uint64, runID string, results []*types.KnowledgeRebuildChunkResult) error
	UpsertChunkResults(ctx context.Context, tenantID uint64, runID string, results []*types.KnowledgeRebuildChunkResult) error
	ListChunkResults(ctx context.Context, tenantID uint64, runID string, classifications []string, chunkTypes []types.ChunkType) ([]*types.KnowledgeRebuildChunkResult, error)
	BeginImages(ctx context.Context, tenantID uint64, runID string, total int) error
	RecordImageResult(ctx context.Context, tenantID uint64, runID string, imageIndex int, ocrCacheKey, captionCacheKey string, ocrCacheHit, captionCacheHit, success bool, errorMessage string) (bool, error)
	BeginArtifacts(ctx context.Context, tenantID uint64, runID string, total int, summaryRequired, wikiReduceRequired bool) error
	FinalizeArtifact(ctx context.Context, tenantID uint64, runID, knowledgeID, stage, artifactKey string, success bool, errorMessage string) (bool, error)
	MarkStaleCleanupComplete(ctx context.Context, tenantID uint64, runID string) error
	MarkWikiReduceEnqueued(ctx context.Context, tenantID uint64, runID string) error
	FinalizeCommit(ctx context.Context, tenantID uint64, runID, knowledgeID string) (bool, error)
	FinalizeWiki(ctx context.Context, tenantID uint64, runID, knowledgeID string, success bool, errorMessage string) (bool, error)
	FailRun(ctx context.Context, tenantID uint64, runID, knowledgeID, errorMessage string) error
}
