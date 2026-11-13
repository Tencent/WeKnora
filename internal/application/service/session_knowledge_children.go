package service

import (
	"context"
	"fmt"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// expandSearchKnowledgeCandidates runs after recall and before the shared reranker.
func (s *sessionService) expandSearchKnowledgeCandidates(
	ctx context.Context, cm *types.ChatManage, opts *types.KnowledgeSearchOptions,
) (*types.KnowledgeTreeDiagnostics, error) {
	expander, ok := s.knowledgeBaseService.(interfaces.KnowledgeRerankExpansionService)
	if !ok {
		return nil, apperrors.NewServiceUnavailableError("knowledge tree retrieval unavailable")
	}
	treeOpts, err := types.ParseKnowledgeChildrenOptions(
		fmt.Sprint(opts.ChildrenDepth), childrenLimitString(opts.ChildrenLimit),
	)
	if err != nil {
		return nil, apperrors.NewBadRequestError(err.Error())
	}
	results, diag, err := expander.ExpandKnowledgeRerankCandidates(ctx, cm.SearchTargets, cm.SearchResult, treeOpts)
	if err == nil {
		cm.SearchResult = results
	}
	return diag, err
}
