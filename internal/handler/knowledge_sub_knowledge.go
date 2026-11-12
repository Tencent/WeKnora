package handler

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// GetSubKnowledge reads one bounded page from the anchor's authorized KB.
// The routing guard and the handler both enforce the normal knowledge read
// grant, including API-key KB allow-lists and organization-share tenant scope.
func (h *KnowledgeHandler) GetSubKnowledge(c *gin.Context) {
	size, budget := 10, 512*1024
	for key, target := range map[string]*int{"page_size": &size, "max_content_bytes": &budget} {
		if raw, exists := c.GetQuery(key); exists {
			n, err := strconv.Atoi(raw)
			if err != nil {
				_ = c.Error(werrors.NewBadRequestError("invalid sub_knowledge pagination"))
				return
			}
			*target = n
		}
	}
	if size < 1 || size > 50 || budget < 1024 || budget > 2*1024*1024 {
		_ = c.Error(werrors.NewBadRequestError("page_size must be 1-50; max_content_bytes must be 1024-2097152"))
		return
	}
	id := c.Param("id")
	anchor, ctx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleViewer)
	if err != nil {
		_ = c.Error(err)
		return
	}
	service, ok := h.kgService.(interfaces.SubKnowledgeService)
	if !ok {
		_ = c.Error(werrors.NewInternalServerError("sub_knowledge service unavailable"))
		return
	}
	result, err := service.GetSubKnowledge(ctx, id, anchor.KnowledgeBaseID, c.Query("cursor"), size)
	if err != nil {
		_ = c.Error(err)
		return
	}
	scope, scoped := types.TenantAPIKeyScopeFromContext(ctx)
	canServeFiles := !scoped || scope.FullAccess ||
		(!scope.IsKnowledgeBaseRestricted() && scope.HasCapability(types.APIKeyCapabilityRetrieve))
	prepareSubKnowledgeResponse(result, budget, c.Request.URL.Query(), canServeFiles)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// The byte budget limits Markdown in this response, not the model's context.
// Truncated items remain in the page with an authorized full-content route.
func prepareSubKnowledgeResponse(
	group *types.SubKnowledgeGroup,
	budget int,
	resourceQuery url.Values,
	canServeFiles bool,
) {
	for i := range group.Items {
		m := &group.Items[i]
		m.ContentURL = subKnowledgeResourceURL(
			"/api/v1/knowledge/"+url.PathEscape(m.KnowledgeID)+"/preview",
			resourceQuery,
		)
		m.RetrievalReady = m.EnableStatus == "enabled" &&
			(m.ParseStatus == types.ParseStatusCompleted || m.ParseStatus == types.ParseStatusFinalizing)
		if m.ImagesStatus == "" {
			m.ImagesStatus = "STORED_REFERENCES_ONLY_COVERAGE_UNVERIFIED"
		}
		if m.ParseStatus != types.ParseStatusCompleted && m.ParseStatus != types.ParseStatusFinalizing {
			m.Images = nil
			m.ImagesStatus = "INDEX_PENDING_OR_FAILED"
		}
		if len(m.Content) > budget {
			prefix := m.Content[:budget]
			for !utf8.ValidString(prefix) {
				prefix = prefix[:len(prefix)-1]
			}
			m.Content = prefix
			m.ContentTruncated = true
		}
		budget -= len(m.Content)
		m.ImageReferences = []types.SubKnowledgeImage{}
		for _, image := range m.Images {
			u, err := url.Parse(image.URL)
			if err != nil {
				continue
			}
			switch strings.ToLower(u.Scheme) {
			case "http", "https":
				m.ImageReferences = append(
					m.ImageReferences,
					types.SubKnowledgeImage{URL: image.URL, Access: "external"},
				)
			case "local", "resource", "minio", "cos", "oss", "s3", "tos", "obs", "ks3":
				if !canServeFiles {
					m.ImageReferences = append(
						m.ImageReferences,
						types.SubKnowledgeImage{Access: "unavailable_for_api_key_scope"},
					)
					continue
				}
				path := "/api/v1/knowledge-bases/" + url.PathEscape(
					group.KnowledgeBaseID,
				) + "/files?file_path=" + url.QueryEscape(
					image.URL,
				)
				m.ImageReferences = append(
					m.ImageReferences,
					types.SubKnowledgeImage{
						URL:    subKnowledgeResourceURL(path, resourceQuery),
						Access: "kb_proxy_requires_binding",
					},
				)
			}
		}
		// Do not expose the full internal ImageInfo. The existing file proxy URL
		// carries a locator and still performs a live binding check on use.
		m.Images = nil
	}
}

func subKnowledgeResourceURL(path string, resourceQuery url.Values) string {
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

// SetSubKnowledge adds or updates a relation on an existing Knowledge of any
// type. The source identity is immutable; parent/reference/order may change.
func (h *KnowledgeHandler) SetSubKnowledge(c *gin.Context) {
	var payload struct {
		SubKnowledge *types.SubKnowledgeInput `json:"sub_knowledge" binding:"required"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		_ = c.Error(werrors.NewBadRequestError("sub_knowledge is required"))
		return
	}
	anchor, ctx, err := h.resolveKnowledgeAndValidateKBAccess(c, c.Param("id"), types.OrgRoleEditor)
	if err != nil {
		_ = c.Error(err)
		return
	}
	service, ok := h.kgService.(interfaces.SubKnowledgeService)
	if !ok {
		_ = c.Error(werrors.NewInternalServerError("sub_knowledge service unavailable"))
		return
	}
	if err := service.SetSubKnowledge(ctx, anchor.ID, anchor.KnowledgeBaseID, payload.SubKnowledge); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(
		http.StatusOK,
		gin.H{"success": true, "data": gin.H{"knowledge_id": anchor.ID, "sub_knowledge": payload.SubKnowledge}},
	)
}
