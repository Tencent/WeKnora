package interfaces

import (
	"context"
	"io"
	"mime/multipart"

	"github.com/Tencent/WeKnora/internal/types"
)

// KnowledgeFileVersionRepository is implemented by the persistent knowledge
// repository. Source switching and archival must commit in one transaction.
type KnowledgeFileVersionRepository interface {
	ReplaceKnowledgeSource(context.Context, *types.Knowledge, map[string]interface{}) error
	RestoreKnowledgeSource(context.Context, *types.Knowledge, string, map[string]interface{}) error
	ListKnowledgeFileVersions(context.Context, uint64, string, int, int) ([]*types.KnowledgeFileVersion, int64, error)
	GetKnowledgeFileVersion(context.Context, uint64, string, int) (*types.KnowledgeFileVersion, error)
	DeleteKnowledgeFileVersions(context.Context, uint64, string) error
}

// KnowledgeFileVersionService exposes file history, uploads and downloads.
type KnowledgeFileVersionService interface {
	ListKnowledgeFileVersions(context.Context, string, int, int) (*types.KnowledgeFileVersionList, error)
	GetKnowledgeFileVersion(context.Context, string, int) (io.ReadCloser, string, error)
	UploadKnowledgeFileVersion(context.Context, string, *multipart.FileHeader, int) (*types.Knowledge, error)
}
