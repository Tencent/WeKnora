package handler

import (
	"context"
	"net/url"
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
		child.ContentURL = knowledgeResourceURL(child.ContentURL, c.Request.URL.Query())
		byID[child.AnchorKnowledgeID] = append(byID[child.AnchorKnowledgeID], child)
	}
	for _, k := range rows {
		if k != nil {
			k.Children = byID[k.ID]
		}
	}
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

// knowledgeResourceURL keeps the resource-access query parameters on child
// preview links so the existing file proxy can authorize them.
func knowledgeResourceURL(path string, resourceQuery url.Values) string {
	u, _ := url.Parse(path)
	q := u.Query()
	for _, key := range []string{"agent_id", types.AgentSourceTenantIDParam} {
		if value := resourceQuery.Get(key); value != "" {
			q.Set(key, value)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
