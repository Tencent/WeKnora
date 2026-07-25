package handler

import (
	stderrors "errors"
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// UserResourceFavoriteHandler exposes the per-user starred-resource API.
//
// Authorization model: a user can only manipulate *their own* favorites in
// the tenant they're currently scoped into (TenantIDContextKey from the
// auth middleware). Cross-user or cross-tenant access is not allowed —
// callers cannot pass a user_id or tenant_id query param. This matches
// the data-model rationale: favorites are personal navigation aids, not
// shareable resources, so there's no value in admin-style listing.
type UserResourceFavoriteHandler struct {
	service interfaces.UserResourceFavoriteService
}

func NewUserResourceFavoriteHandler(svc interfaces.UserResourceFavoriteService) *UserResourceFavoriteHandler {
	return &UserResourceFavoriteHandler{service: svc}
}

// favoriteContext resolves the (userID, tenantID) pair the handler will
// scope all queries to. Centralised so the three endpoints stay short
// and consistent in their error shape.
func favoriteContext(c *gin.Context) (string, uint64, bool) {
	uidVal, ok := c.Get(types.UserIDContextKey.String())
	if !ok {
		c.Error(apperrors.NewUnauthorizedError("user ID not found"))
		return "", 0, false
	}
	userID, _ := uidVal.(string)
	if userID == "" {
		c.Error(apperrors.NewUnauthorizedError("user ID not found"))
		return "", 0, false
	}
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(apperrors.NewUnauthorizedError("workspace ID not found"))
		return "", 0, false
	}
	return userID, tenantID, true
}

// ListFavorites godoc
// @Summary      List my favorites
// @Description  Lists this user's starred resources in the current workspace for a given type
// @Tags         User
// @Param        type  query     string  true  "Resource type (kb | agent)"
// @Success      200   {object}  map[string]interface{}
// @Router       /user/favorites [get]
func (h *UserResourceFavoriteHandler) ListFavorites(c *gin.Context) {
	ctx := c.Request.Context()
	userID, tenantID, ok := favoriteContext(c)
	if !ok {
		return
	}
	resourceType := c.Query("type")

	list, err := h.service.List(ctx, userID, tenantID, resourceType)
	if err != nil {
		if stderrors.Is(err, service.ErrFavoriteInvalidType) {
			c.Error(apperrors.NewBadRequestError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// AddFavoriteRequest is the body for POST /user/favorites — kept symmetric
// with the DELETE path parameters so the frontend can build a single API
// helper that switches verb on toggle.
type AddFavoriteRequest struct {
	ResourceType string `json:"type"`
	ResourceID   string `json:"id"`
}

// AddFavorite godoc
// @Summary      Star a resource
// @Tags         User
// @Param        body  body      AddFavoriteRequest  true  "Type + id"
// @Success      200   {object}  map[string]interface{}
// @Router       /user/favorites [post]
func (h *UserResourceFavoriteHandler) AddFavorite(c *gin.Context) {
	ctx := c.Request.Context()
	userID, tenantID, ok := favoriteContext(c)
	if !ok {
		return
	}
	var req AddFavoriteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid request body").WithDetails(err.Error()))
		return
	}
	if err := h.service.Add(ctx, userID, tenantID, req.ResourceType, req.ResourceID); err != nil {
		if stderrors.Is(err, service.ErrFavoriteInvalidType) || stderrors.Is(err, service.ErrFavoriteEmptyID) {
			c.Error(apperrors.NewBadRequestError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RemoveFavorite godoc
// @Summary      Unstar a resource
// @Tags         User
// @Param        type  path      string  true  "Resource type"
// @Param        id    path      string  true  "Resource id"
// @Success      200   {object}  map[string]interface{}
// @Router       /user/favorites/{type}/{id} [delete]
func (h *UserResourceFavoriteHandler) RemoveFavorite(c *gin.Context) {
	ctx := c.Request.Context()
	userID, tenantID, ok := favoriteContext(c)
	if !ok {
		return
	}
	resourceType := c.Param("type")
	resourceID := c.Param("id")

	if err := h.service.Remove(ctx, userID, tenantID, resourceType, resourceID); err != nil {
		if stderrors.Is(err, service.ErrFavoriteInvalidType) || stderrors.Is(err, service.ErrFavoriteEmptyID) {
			c.Error(apperrors.NewBadRequestError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
