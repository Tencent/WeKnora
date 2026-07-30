package interfaces

import (
	"context"
	"io"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

type TemporaryDocumentRepository interface {
	Create(ctx context.Context, document *types.TemporaryDocument) error
	GetByID(ctx context.Context, tenantID uint64, documentID string) (*types.TemporaryDocument, error)
	GetScoped(ctx context.Context, tenantID uint64, sessionID, documentID string) (*types.TemporaryDocument, error)
	ListScoped(ctx context.Context, tenantID uint64, sessionID string) ([]*types.TemporaryDocument, error)
	MarkProcessing(ctx context.Context, tenantID uint64, documentID string, startedAt time.Time) error
	MarkReady(ctx context.Context, tenantID uint64, documentID, content string, chunks, imageRefs, metadata types.JSON, tokenCount, chunkCount int, readyAt time.Time) error
	MarkFailed(ctx context.Context, tenantID uint64, documentID, message string) error
	DeleteScoped(ctx context.Context, tenantID uint64, sessionID, documentID string) error
	ListExpired(ctx context.Context, before time.Time, limit int) ([]*types.TemporaryDocument, error)
}

type TemporaryDocumentService interface {
	Create(ctx context.Context, tenantID uint64, sessionID, fileName, mimeType string, fileSize int64, reader io.Reader, options types.TemporaryDocumentCreateOptions) (*types.TemporaryDocument, error)
	Get(ctx context.Context, tenantID uint64, sessionID, documentID string) (*types.TemporaryDocument, error)
	OpenFile(ctx context.Context, tenantID uint64, sessionID, documentID string) (io.ReadCloser, string, error)
	List(ctx context.Context, tenantID uint64, sessionID string) ([]*types.TemporaryDocument, error)
	Delete(ctx context.Context, tenantID uint64, sessionID, documentID string) error
	ResolveForPrompt(ctx context.Context, tenantID uint64, sessionID string, documentIDs []string, query string) (*types.TemporaryDocumentPromptResult, error)
	Process(ctx context.Context, task *asynq.Task) error
	CleanupExpired(ctx context.Context) error
}
