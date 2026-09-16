package handler

import (
	"errors"
	"net/http"

	publicdata "github.com/Tencent/WeKnora/dataset/public"
	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/gin-gonic/gin"
)

type evaluationDatasetCatalogLimits struct {
	config.EvaluationDatasetLimits
	MaxNameChars int   `json:"max_name_chars"`
	MaxIDChars   int   `json:"max_id_chars"`
	MaxGrade     int64 `json:"max_grade"`
}

// ListCatalog godoc
// @Summary      列出公开评测数据集
// @Description  返回服务内嵌资料的来源、许可、规模及当前导入限制
// @Tags         评估
// @Produce      json
// @Success      200 {object} map[string]interface{} "公开目录与实际限制"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/datasets/catalog [get]
func (h *EvaluationDatasetHandler) ListCatalog(c *gin.Context) {
	if _, ok := evaluationHandlerTenantID(c); !ok {
		return
	}
	items, err := publicdata.List()
	if err != nil {
		_ = c.Error(apperrors.NewInternalServerError("Public evaluation catalog is unavailable").
			WithDetails(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"items": items,
		"limits": evaluationDatasetCatalogLimits{
			EvaluationDatasetLimits: h.limits, MaxNameChars: 255, MaxIDChars: 128, MaxGrade: 2147483647,
		},
	}})
}

// GetCatalogItem godoc
// @Summary      读取公开评测数据集内容
// @Description  校验内嵌资料摘要后返回可导入内容和来源清单
// @Tags         评估
// @Produce      json
// @Param        id path string true "公开数据集 ID"
// @Success      200 {object} map[string]interface{} "内容与来源清单"
// @Failure      404 {object} map[string]interface{} "未知公开数据集"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/datasets/catalog/{id} [get]
func (h *EvaluationDatasetHandler) GetCatalogItem(c *gin.Context) {
	if _, ok := evaluationHandlerTenantID(c); !ok {
		return
	}
	bundle, err := publicdata.Get(c.Param("id"))
	if errors.Is(err, publicdata.ErrNotFound) {
		_ = c.Error(apperrors.NewNotFoundError("Public evaluation dataset not found"))
		return
	}
	if err != nil {
		_ = c.Error(apperrors.NewInternalServerError("Public evaluation dataset is unavailable").
			WithDetails(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": bundle})
}
