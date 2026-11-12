package interfaces

import (
	"context"
	"mime/multipart"

	"github.com/Tencent/WeKnora/internal/types"
)

// KnowledgeTreeRepository persists native parent links and bounded tree operations.
type KnowledgeTreeRepository interface {
	CreateKnowledgeWithParent(context.Context, *types.Knowledge, ...string) error
	KnowledgeChildren(
		context.Context,
		[]types.KnowledgeTreeRoot,
		types.KnowledgeChildrenOptions,
	) ([]types.KnowledgeChild, error)
	PrepareKnowledgeCascade(context.Context, uint64, string, []string, bool) ([]string, error)
	CheckKnowledgeTreeMove(context.Context, uint64, string, string) error
}

// KnowledgeTreeService expands descendants and validates tree lifecycle operations.
type KnowledgeTreeService interface {
	KnowledgeChildren(
		context.Context,
		[]types.KnowledgeTreeRoot,
		types.KnowledgeChildrenOptions,
	) ([]types.KnowledgeChild, error)
	PrepareKnowledgeCascade(context.Context, uint64, string, []string, bool) ([]string, error)
	CheckKnowledgeTreeMove(context.Context, uint64, string, string) error
}

// KnowledgeParentImportService keeps existing import adapters source-compatible.
type KnowledgeParentImportService interface {
	CreateKnowledgeFromFileWithParent(
		context.Context,
		string,
		*multipart.FileHeader,
		map[string]string,
		*bool,
		string,
		[]string,
		string,
		*types.KnowledgeProcessOverrides,
		string,
	) (*types.Knowledge, error)
	CreateKnowledgeFromURLWithParent(
		context.Context,
		string,
		string,
		string,
		string,
		*bool,
		string,
		[]string,
		string,
		*types.KnowledgeProcessOverrides,
		string,
	) (*types.Knowledge, error)
}
