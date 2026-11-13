package service

import (
	"context"
	"strconv"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var _ interfaces.KnowledgeRerankExpansionService = (*knowledgeBaseService)(nil)

// ExpandKnowledgeRerankCandidates preserves initial hits and appends bounded,
// real passages from their direct parents and the parents' descendants.
func (s *knowledgeBaseService) ExpandKnowledgeRerankCandidates(
	ctx context.Context, targets types.SearchTargets, hits []*types.SearchResult, opts types.KnowledgeChildrenOptions,
) ([]*types.SearchResult, *types.KnowledgeTreeDiagnostics, error) {
	if opts.Depth == 0 || len(hits) == 0 {
		return hits, nil, nil
	}
	r, ok := s.kgRepo.(interfaces.KnowledgeRerankRepository)
	if !ok {
		return nil, nil, apperrors.NewServiceUnavailableError("knowledge tree retrieval unavailable")
	}
	allowed := map[types.KnowledgeTreeRoot]bool{}
	for _, target := range targets {
		if target != nil && target.TenantID != 0 {
			allowed[types.KnowledgeTreeRoot{TenantID: target.TenantID, KBID: target.KnowledgeBaseID}] = true
		}
	}
	var ids []string
	seen := map[string]bool{}
	for _, hit := range hits {
		if hit != nil && hit.KnowledgeID != "" && !seen[hit.KnowledgeID] {
			ids = append(ids, hit.KnowledgeID)
			seen[hit.KnowledgeID] = true
		}
	}
	rows, err := s.fetchKnowledgeDataWithShared(ctx, types.MustTenantIDFromContext(ctx), ids)
	if err != nil {
		return nil, nil, err
	}
	var roots []types.KnowledgeTreeRoot
	for _, id := range ids {
		k := rows[id]
		if k != nil && allowed[types.KnowledgeTreeRoot{TenantID: k.TenantID, KBID: k.KnowledgeBaseID}] {
			roots = append(roots, types.KnowledgeTreeRoot{
				TenantID: k.TenantID, KBID: k.KnowledgeBaseID, KnowledgeID: k.ID,
			})
		}
	}
	passages, diag, err := r.KnowledgeRerankPassages(ctx, roots, opts)
	if err != nil {
		return nil, nil, err
	}
	results := append([]*types.SearchResult(nil), hits...)
	for _, passage := range passages {
		row := s.buildSearchResult(passage.Chunk, passage.Knowledge, 0, types.MatchTypeKnowledgeTree, "")
		if row.Metadata == nil {
			row.Metadata = map[string]string{}
		}
		row.Metadata["tree_anchor_knowledge_id"] = passage.AnchorKnowledgeID
		row.Metadata["tree_parent_knowledge_id"] = passage.Knowledge.ParentKnowledgeID
		row.Metadata["tree_depth"] = strconv.Itoa(passage.Depth)
		searchutil.CaptureImageEvidence(row)
		results = append(results, row)
	}
	return results, diag, nil
}

func (s *knowledgeBaseService) hybridRerankTree(
	ctx context.Context, id string, params types.SearchParams, hits []*types.SearchResult,
) ([]*types.SearchResult, *types.KnowledgeTreeDiagnostics, error) {
	if params.ChildrenDepth == 0 {
		return hits, nil, nil
	}
	opts, err := types.ParseKnowledgeChildrenOptions(
		strconv.Itoa(params.ChildrenDepth), childrenLimitString(params.ChildrenLimit),
	)
	if err != nil {
		return nil, nil, apperrors.NewBadRequestError(err.Error())
	}
	ids := params.KnowledgeBaseIDs
	if len(ids) == 0 {
		ids = []string{id}
	}
	kbs, err := s.repo.GetKnowledgeBaseByIDs(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	if err := s.authorizeKBAccess(ctx, kbs); err != nil {
		return nil, nil, err
	}
	var targets types.SearchTargets
	for _, kb := range kbs {
		targets = append(targets, &types.SearchTarget{TenantID: kb.TenantID, KnowledgeBaseID: kb.ID})
	}
	return s.ExpandKnowledgeRerankCandidates(ctx, targets, hits, opts)
}

func childrenLimitString(limit int) string {
	if limit == 0 {
		return ""
	}
	return strconv.Itoa(limit)
}
