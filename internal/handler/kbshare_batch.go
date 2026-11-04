package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

const (
	maxBatchSharePairs     = 100
	maxBatchShareBodyBytes = 64 << 10
)

// BatchShareKnowledgeBaseResult reports the outcome of one unique KB/org pair.
type BatchShareKnowledgeBaseResult struct {
	KBID           string                    `json:"kb_id"`
	OrganizationID string                    `json:"organization_id"`
	Status         int                       `json:"status"`
	Share          *types.KnowledgeBaseShare `json:"share,omitempty"`
	Error          *apperrors.AppError       `json:"error,omitempty"`
}

// BatchShareKnowledgeBasesResponse contains results in KB-major input order.
type BatchShareKnowledgeBasesResponse struct {
	Results []BatchShareKnowledgeBaseResult `json:"results"`
}

// BatchShareKnowledgeBases returns a handler using the route's RBAC policy.
// @Summary      批量共享知识库到组织
// @Description  去重后最多 100 个知识库与组织组合。逐项授权和执行，返回每项状态；已存在的共享更新权限。
// @Tags         知识库共享
// @Accept       json
// @Produce      json
// @Param        request body types.BatchShareKnowledgeBasesRequest true "批量共享信息"
// @Success      200 {object} handler.DataResponse{data=handler.BatchShareKnowledgeBasesResponse}
// @Failure      400 {object} apperrors.AppError
// @Failure      401 {object} apperrors.AppError
// @Failure      403 {object} apperrors.AppError
// @Failure      413 {object} apperrors.AppError
// @Security     Bearer
// @Router       /knowledge-bases/shares/batch [post]
func (h *OrganizationHandler) BatchShareKnowledgeBases(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		caller := types.CallerFromContext(ctx)
		if caller.TenantID == 0 {
			_ = c.Error(apperrors.NewUnauthorizedError("Unauthorized"))
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxBatchShareBodyBytes))
		if err != nil {
			var sizeErr *http.MaxBytesError
			if errors.As(err, &sizeErr) {
				_ = c.Error(&apperrors.AppError{
					Code: apperrors.ErrBadRequest, Message: "Batch share request exceeds 64 KiB",
					HTTPCode: http.StatusRequestEntityTooLarge,
				})
			} else {
				_ = c.Error(apperrors.NewValidationError("Invalid batch share request"))
			}
			return
		}
		var req types.BatchShareKnowledgeBasesRequest
		if err := json.Unmarshal(body, &req); err != nil {
			_ = c.Error(apperrors.NewValidationError("Invalid batch share request"))
			return
		}
		if err := binding.Validator.ValidateStruct(&req); err != nil {
			_ = c.Error(apperrors.NewValidationError("Invalid batch share request"))
			return
		}
		if !req.Permission.IsValid() {
			_ = c.Error(apperrors.NewValidationError("Invalid share permission"))
			return
		}
		kbIDs := uniqueShareIDs(req.KnowledgeBaseIDs)
		orgIDs := uniqueShareIDs(req.OrganizationIDs)
		if len(kbIDs)*len(orgIDs) > maxBatchSharePairs {
			_ = c.Error(apperrors.NewValidationError(
				"At most 100 unique knowledge base/organization pairs are allowed",
			))
			return
		}

		results := make([]BatchShareKnowledgeBaseResult, 0, len(kbIDs)*len(orgIDs))
		for _, kbID := range kbIDs {
			ownershipErr := h.batchShareOwnershipError(c, cfg, kbID)
			for _, orgID := range orgIDs {
				result := BatchShareKnowledgeBaseResult{KBID: kbID, OrganizationID: orgID, Status: http.StatusOK}
				result.Error = ownershipErr
				if result.Error == nil {
					share, err := h.shareService.ShareKnowledgeBase(
						ctx, kbID, orgID, caller.UserID, caller.TenantID, req.Permission,
					)
					if err != nil {
						logger.Errorf(ctx, "Batch knowledge base sharing failed: %v", err)
						result.Error = batchShareServiceError(err)
					} else {
						result.Share = share
					}
				}
				if result.Error != nil {
					result.Status = result.Error.HTTPCode
				}
				results = append(results, result)
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    BatchShareKnowledgeBasesResponse{Results: results},
		})
	}
}

// Body-carried IDs need the same lazy creator/Admin decision as OwnedKBOrAdmin.
// The existing sharing service separately checks the KB tenant and org role.
func (h *OrganizationHandler) batchShareOwnershipError(
	c *gin.Context, cfg *config.Config, kbID string,
) *apperrors.AppError {
	err := middleware.EvaluateOwnershipOrRole(c.Request.Context(), cfg, types.TenantRoleAdmin,
		func() (string, error) { return resolveKBCreatorByKBID(c, h.kbService, kbID) })
	switch {
	case err == nil:
		return nil
	case errors.Is(err, middleware.ErrResourceNotFound):
		return apperrors.NewNotFoundError("Knowledge base not found")
	case errors.Is(err, middleware.ErrOwnershipForbidden):
		return apperrors.NewForbiddenError("No permission to share this knowledge base")
	default:
		logger.Errorf(c.Request.Context(), "Batch sharing ownership lookup failed: %v", err)
		return apperrors.NewServiceUnavailableError("Cannot verify knowledge base ownership")
	}
}

func batchShareServiceError(err error) *apperrors.AppError {
	switch {
	case errors.Is(err, service.ErrKBNotFound), errors.Is(err, service.ErrOrgNotFound):
		return apperrors.NewNotFoundError("Knowledge base or organization not found")
	case errors.Is(err, service.ErrNotKBOwner), errors.Is(err, service.ErrTenantNotInOrg),
		errors.Is(err, service.ErrOrgRoleCannotShare):
		return apperrors.NewForbiddenError("No permission to share this knowledge base to the organization")
	default:
		return apperrors.NewInternalServerError("Failed to share knowledge base")
	}
}

func uniqueShareIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			unique = append(unique, id)
		}
	}
	return unique
}
