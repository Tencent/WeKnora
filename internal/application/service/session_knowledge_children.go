package service

import (
	"context"
	"sort"
	"strconv"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// The verified search targets, not a caller-supplied tenant, bind expansions.
func (s *sessionService) expandSearchKnowledgeChildren(
	ctx context.Context,
	targets types.SearchTargets,
	hits []*types.SearchResult,
	opts *types.KnowledgeSearchOptions,
) ([]types.KnowledgeChild, error) {
	tree, ok := s.knowledgeService.(interfaces.KnowledgeTreeService)
	if !ok {
		return nil, apperrors.NewServiceUnavailableError("knowledge tree unavailable")
	}
	limit := ""
	if opts.ChildrenLimit != 0 {
		limit = strconv.Itoa(opts.ChildrenLimit)
	}
	o, err := types.ParseKnowledgeChildrenOptions(strconv.Itoa(opts.ChildrenDepth), limit)
	if err != nil {
		return nil, apperrors.NewBadRequestError(err.Error())
	}
	seen := map[string]bool{}
	var ids []string
	for _, hit := range hits {
		if hit != nil && hit.KnowledgeID != "" && !seen[hit.KnowledgeID] {
			seen[hit.KnowledgeID] = true
			ids = append(ids, hit.KnowledgeID)
		}
	}
	byTenant := map[uint64]map[string]bool{}
	for _, target := range targets {
		if target != nil && target.TenantID != 0 {
			if byTenant[target.TenantID] == nil {
				byTenant[target.TenantID] = map[string]bool{}
			}
			byTenant[target.TenantID][target.KnowledgeBaseID] = true
		}
	}
	var roots []types.KnowledgeTreeRoot
	for tenant, kbs := range byTenant {
		rows, e := s.knowledgeService.GetKnowledgeBatch(types.WithExecutionTenant(ctx, tenant), tenant, ids)
		if e != nil {
			return nil, e
		}
		for _, k := range rows {
			if k != nil && seen[k.ID] && k.TenantID == tenant && kbs[k.KnowledgeBaseID] {
				roots = append(
					roots,
					types.KnowledgeTreeRoot{TenantID: tenant, KBID: k.KnowledgeBaseID, KnowledgeID: k.ID},
				)
			}
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		a, b := roots[i], roots[j]
		if a.TenantID != b.TenantID {
			return a.TenantID < b.TenantID
		}
		if a.KBID != b.KBID {
			return a.KBID < b.KBID
		}
		return a.KnowledgeID < b.KnowledgeID
	})
	children, err := tree.KnowledgeChildren(ctx, roots, o)
	if err != nil {
		return nil, err
	}
	for i := range children {
		children[i].AlreadyHit = seen[children[i].KnowledgeID]
	}
	return children, nil
}
