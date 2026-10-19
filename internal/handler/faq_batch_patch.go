package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// FAQBatchPatchRequest applies the same partial change to a bounded list of entries.
// Unlike the legacy map-based endpoint, this shape also fits the operation catalog.
type FAQBatchPatchRequest struct {
	// Explicit FAQ entry seq_ids from faq.list_entries. At most 20 per call.
	EntryIDs []int64 `json:"entry_ids"                binding:"required,min=1,max=20" extensions:"x-agent,x-agent-maximum=20"`
	// Whether these entries can be retrieved; omitted keeps the current state.
	IsEnabled *bool `json:"is_enabled,omitempty"                                     extensions:"x-agent"`
	// Whether these entries are recommended; omitted keeps the current state.
	IsRecommended *bool `json:"is_recommended,omitempty"                                 extensions:"x-agent"`
	// Tag seq_id from knowledge_base.list_tags; 0 clears tags, omitted keeps them.
	TagID *int64 `json:"tag_id,omitempty"                                         extensions:"x-agent=tag_seq_id"         minimum:"0"`
}

func (req FAQBatchPatchRequest) updatePlan() (*types.FAQEntryFieldsBatchUpdate, error) {
	if len(req.EntryIDs) == 0 || len(req.EntryIDs) > 20 {
		return nil, errors.NewBadRequestError("entry_ids must contain 1-20 entries")
	}
	if req.IsEnabled == nil && req.IsRecommended == nil && req.TagID == nil {
		return nil, errors.NewBadRequestError("at least one field must be provided")
	}
	if req.TagID != nil && *req.TagID < 0 {
		return nil, errors.NewBadRequestError("tag_id must be non-negative; use 0 to clear tags")
	}
	plan := &types.FAQEntryFieldsBatchUpdate{ByID: make(map[int64]types.FAQEntryFieldsUpdate, len(req.EntryIDs))}
	for _, id := range req.EntryIDs {
		if id <= 0 {
			return nil, errors.NewBadRequestError("entry_ids must be positive seq_ids")
		}
		if _, ok := plan.ByID[id]; ok {
			return nil, errors.NewBadRequestError("entry_ids must not contain duplicates")
		}
		plan.ByID[id] = types.FAQEntryFieldsUpdate{
			IsEnabled:     req.IsEnabled,
			IsRecommended: req.IsRecommended,
			TagID:         req.TagID,
		}
	}
	return plan, nil
}

// PatchEntriesBatch godoc
// @Summary 批量修改 FAQ 状态和标签
// @Description 对明确指定的 FAQ 条目应用相同的部分更新，保留问题、答案及未指定字段
// @Tags FAQ管理
// @Accept json
// @Produce json
// @Param id path string true "知识库ID" extensions(x-agent=knowledge_base_id)
// @Param request body FAQBatchPatchRequest true "批量修改请求"
// @Success 200 {object} handler.SuccessResponse
// @Failure 400 {object} errors.AppError
// @Security Bearer
// @Security ApiKeyAuth
// @x-agent {"id":"faq.batch_update","risk":"write","target":"knowledge_base.get","label":"name","items":{"param":"entry_ids","target":"faq.get_entry","as":"entry_id","label":"standard_question"}}
// @x-agent-hint "Set is_enabled, is_recommended or tag_seq_id for 1-20 explicit FAQ entry seq_ids; at least one field is required. Omitted fields and all questions/answers stay unchanged. tag_seq_id comes from knowledge_base.list_tags (tag_seq_id); 0 removes the tag. Select entries with faq.list_entries first; for a whole tag or knowledge base paginate the list and process batches of up to 20. Confirmation lists every question."
// @Router /knowledge-bases/{id}/faq/entries [patch]
func (h *FAQHandler) PatchEntriesBatch(c *gin.Context) {
	var req FAQBatchPatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("Invalid FAQ batch patch").WithDetails(err.Error()))
		return
	}
	plan, err := req.updatePlan()
	if err != nil {
		_ = c.Error(err)
		return
	}
	if err := h.knowledgeService.UpdateFAQEntryFieldsBatch(c.Request.Context(), c.Param("id"), plan); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
