package handler

import (
	"context"
	"strconv"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

func parseHTTPKnowledgeChildrenOptions(
	c *gin.Context,
	bodyDepth, bodyLimit int,
) (types.KnowledgeChildrenOptions, error) {
	limit := ""
	if bodyLimit != 0 {
		limit = strconv.Itoa(bodyLimit)
	}
	return types.ParseKnowledgeChildrenOptions(
		c.DefaultQuery("children_depth", strconv.Itoa(bodyDepth)),
		c.DefaultQuery("children_limit", limit),
	)
}

func expandKnowledgeRows(
	ctx context.Context,
	c *gin.Context,
	service interfaces.KnowledgeService,
	rows []*types.Knowledge,
) bool {
	opts, err := parseHTTPKnowledgeChildrenOptions(c, 0, 0)
	if err != nil {
		_ = c.Error(apperrors.NewBadRequestError(err.Error()))
		return false
	}
	if opts.Depth == 0 {
		return true
	}
	tree, ok := service.(interfaces.KnowledgeTreeService)
	if !ok {
		_ = c.Error(apperrors.NewServiceUnavailableError("knowledge tree unavailable"))
		return false
	}
	var roots []types.KnowledgeTreeRoot
	for _, k := range rows {
		if k != nil {
			roots = append(
				roots,
				types.KnowledgeTreeRoot{TenantID: k.TenantID, KBID: k.KnowledgeBaseID, KnowledgeID: k.ID},
			)
		}
	}
	children, err := tree.KnowledgeChildren(ctx, roots, opts)
	if err != nil {
		_ = c.Error(err)
		return false
	}
	byID := map[string][]types.KnowledgeChild{}
	for _, child := range children {
		child.ContentURL = subKnowledgeResourceURL(child.ContentURL, c.Request.URL.Query())
		byID[child.AnchorKnowledgeID] = append(byID[child.AnchorKnowledgeID], child)
	}
	for _, k := range rows {
		if k != nil {
			k.Children = byID[k.ID]
		}
	}
	return true
}

func (h *KnowledgeBaseHandler) expandHybridChildren(
	c *gin.Context,
	tenant uint64,
	kb string,
	hits []*types.SearchResult,
	response gin.H,
	opts types.KnowledgeChildrenOptions,
	searchKBs []string,
) bool {
	if opts.Depth == 0 {
		return true
	}
	tree, ok := h.knowledgeService.(interfaces.KnowledgeTreeService)
	if !ok {
		_ = c.Error(apperrors.NewServiceUnavailableError("knowledge tree unavailable"))
		return false
	}
	seen := map[string]bool{}
	allowedKBs := map[string]bool{kb: true}
	if len(searchKBs) > 0 {
		allowedKBs = map[string]bool{}
		for _, id := range searchKBs {
			allowedKBs[id] = true
		}
	}
	owners := map[string]uint64{kb: tenant}
	var roots []types.KnowledgeTreeRoot
	for _, hit := range hits {
		if hit != nil && hit.KnowledgeID != "" && !seen[hit.KnowledgeID] {
			hitKB := hit.KnowledgeBaseID
			if hitKB == "" {
				hitKB = kb
			}
			if !allowedKBs[hitKB] {
				_ = c.Error(apperrors.NewConflictError("knowledge search scope changed"))
				return false
			}
			owner, known := owners[hitKB]
			if !known {
				grant, err := resolveHandlerKBAccess(c, hitKB, h.service, h.kbShareService, h.agentShareService)
				if err != nil {
					_ = c.Error(err)
					return false
				}
				owner = grant.EffectiveTenantID
				owners[hitKB] = owner
			}
			seen[hit.KnowledgeID] = true
			roots = append(roots, types.KnowledgeTreeRoot{TenantID: owner, KBID: hitKB, KnowledgeID: hit.KnowledgeID})
		}
	}
	rows, err := tree.KnowledgeChildren(c.Request.Context(), roots, opts)
	if err != nil {
		_ = c.Error(err)
		return false
	}
	for i := range rows {
		rows[i].AlreadyHit = seen[rows[i].KnowledgeID]
		rows[i].ContentURL = subKnowledgeResourceURL(rows[i].ContentURL, c.Request.URL.Query())
	}
	if rows == nil {
		rows = []types.KnowledgeChild{}
	}
	response["expanded"] = rows
	return true
}

func cascadeChildrenOption(c *gin.Context) (bool, error) {
	raw := c.DefaultQuery("cascade_children", "false")
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, apperrors.NewBadRequestError("invalid cascade_children")
	}
	return value, nil
}
