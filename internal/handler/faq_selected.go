package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// FAQSelectedFieldsRequest changes only explicitly named entries and fields.
type FAQSelectedFieldsRequest struct {
	// Explicit entry sequence IDs, at most 20; never selects an entire tag.
	EntryIDs []int64 `json:"entry_ids" binding:"required,min=1,max=20,dive,gt=0" extensions:"x-agent,x-agent-maximum=20"`
	// Whether the selected entries participate in retrieval.
	IsEnabled *bool `json:"is_enabled" extensions:"x-agent"`
	// Whether the selected entries are recommended.
	IsRecommended *bool `json:"is_recommended" extensions:"x-agent"`
	// Existing tag sequence ID; zero clears the tag.
	TagID *int64 `json:"tag_id" binding:"omitempty,min=0" minimum:"0" extensions:"x-agent=tag_seq_id"`
}

// UpdateSelectedFields godoc
// @Summary Update selected FAQ flags and tags
// @Tags FAQ管理
// @Accept json
// @Produce json
// @Param id path string true "Knowledge base ID" extensions(x-agent=knowledge_base_id)
// @Param request body handler.FAQSelectedFieldsRequest true "Selected entries and fields"
// @Success 200 {object} handler.SuccessResponse
// @x-agent {"id":"faq.update_selected_fields","risk":"write","target":"knowledge_base.get","label":"name","items":{"param":"entry_ids","target":"faq.get_entry","as":"entry_id","label":"standard_question"}}
// @x-agent-hint "Changes only supplied fields on up to 20 explicit entries. Supports is_enabled, is_recommended and tag_seq_id (0 removes tag); leaves questions and answers intact."
// @Router /knowledge-bases/{id}/faq/selected/fields [put]
func (h *FAQHandler) UpdateSelectedFields(c *gin.Context) {
	var req FAQSelectedFieldsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("Invalid selected FAQ fields"))
		return
	}
	if req.IsEnabled == nil && req.IsRecommended == nil && req.TagID == nil {
		_ = c.Error(errors.NewBadRequestError("Choose at least one field"))
		return
	}
	update := types.FAQEntryFieldsUpdate{IsEnabled: req.IsEnabled, IsRecommended: req.IsRecommended, TagID: req.TagID}
	batch := &types.FAQEntryFieldsBatchUpdate{ByID: make(map[int64]types.FAQEntryFieldsUpdate, len(req.EntryIDs))}
	for _, id := range req.EntryIDs {
		batch.ByID[id] = update
	}
	if err := h.knowledgeService.UpdateFAQEntryFieldsBatch(c.Request.Context(), c.Param("id"), batch); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// FAQExportArtifact describes an authenticated on-demand export, never a public URL.
type FAQExportArtifact struct {
	KnowledgeBaseID string `json:"knowledge_base_id" extensions:"x-agent"`
	Format          string `json:"format" extensions:"x-agent"`
	FileName        string `json:"file_name" extensions:"x-agent"`
}

// ExportArtifact godoc
// @Summary Prepare an authenticated FAQ export
// @Produce json
// @Param id path string true "FAQ knowledge base ID" extensions(x-agent=knowledge_base_id)
// @Param format query string false "Export format" Enums(json,csv) default(json) extensions(x-agent)
// @Success 200 {object} handler.DataResponse{data=handler.FAQExportArtifact}
// @x-agent {"id":"faq.export","risk":"read"}
// @x-agent-hint "Shows an authenticated download button. Exports the current FAQ entries when clicked, rechecking KB access then. JSON can be reimported with faq.import_selected; no public sharing link is created."
// @Router /knowledge-bases/{id}/faq/export-artifact [get]
func (h *FAQHandler) ExportArtifact(c *gin.Context) {
	format := c.DefaultQuery("format", "json")
	if format != "json" && format != "csv" {
		_ = c.Error(errors.NewBadRequestError("Choose json or csv"))
		return
	}
	kb, err := h.kbService.GetKnowledgeBaseByID(c.Request.Context(), c.Param("id"))
	if err != nil || kb == nil || kb.Type != types.KnowledgeBaseTypeFAQ {
		_ = c.Error(errors.NewNotFoundError("FAQ knowledge base not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": FAQExportArtifact{
		KnowledgeBaseID: kb.ID, Format: format, FileName: "faq_export." + format,
	}})
}
