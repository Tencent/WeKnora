package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type LearningHandler struct{ service interfaces.LearningService }

func NewLearningHandler(svc interfaces.LearningService) *LearningHandler {
	return &LearningHandler{service: svc}
}

func (h *LearningHandler) GetSettings(c *gin.Context) {
	data, err := h.service.GetSettings(c.Request.Context())
	h.respond(c, data, err)
}

func (h *LearningHandler) SetEnabled(c *gin.Context) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeLearningRequest(c, &input) {
		return
	}
	if input.Enabled == nil {
		h.respond(c, nil, types.ErrLearningInvalid)
		return
	}
	data, err := h.service.SetEnabled(c.Request.Context(), *input.Enabled)
	h.respond(c, data, err)
}

func (h *LearningHandler) Overview(c *gin.Context) {
	data, err := h.service.Overview(c.Request.Context(), c.Query("knowledge_base_id"))
	h.respond(c, data, err)
}

func (h *LearningHandler) Node(c *gin.Context) {
	data, err := h.service.Node(c.Request.Context(), c.Param("id"))
	h.respond(c, data, err)
}

func (h *LearningHandler) Recommendations(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "5"))
	if err != nil || limit < 1 || limit > 20 {
		h.respond(c, nil, types.ErrLearningInvalid)
		return
	}
	data, err := h.service.Recommend(c.Request.Context(), c.Query("knowledge_base_id"), limit)
	h.respond(c, data, err)
}

func (h *LearningHandler) RecordView(c *gin.Context) {
	h.respond(c, nil, h.service.RecordView(c.Request.Context(), c.Param("id")))
}

func (h *LearningHandler) Overlay(c *gin.Context) {
	var input struct {
		KnowledgeBaseID string   `json:"knowledge_base_id"`
		Slugs           []string `json:"slugs"`
	}
	if !decodeLearningRequest(c, &input) {
		return
	}
	if len(input.Slugs) > 2000 || input.KnowledgeBaseID == "" {
		h.respond(c, nil, types.ErrLearningInvalid)
		return
	}
	data, err := h.service.Overlay(c.Request.Context(), input.KnowledgeBaseID, input.Slugs)
	h.respond(c, data, err)
}

func (h *LearningHandler) PrepareQuiz(c *gin.Context) {
	var input struct {
		PageID string `json:"page_id"`
	}
	if !decodeLearningRequest(c, &input) {
		return
	}
	if input.PageID == "" {
		h.respond(c, nil, types.ErrLearningInvalid)
		return
	}
	data, err := h.service.PrepareQuiz(c.Request.Context(), input.PageID)
	h.respond(c, data, err)
}

func (h *LearningHandler) GetQuiz(c *gin.Context) {
	data, err := h.service.GetQuiz(c.Request.Context(), c.Param("id"))
	h.respond(c, data, err)
}

func (h *LearningHandler) SubmitAnswer(c *gin.Context) {
	var input types.LearningAnswer
	if !decodeLearningRequest(c, &input) {
		return
	}
	data, err := h.service.SubmitAnswer(c.Request.Context(), input)
	h.respond(c, data, err)
}

func (h *LearningHandler) Export(c *gin.Context) {
	data, err := h.service.Export(c.Request.Context(), c.Query("knowledge_base_id"))
	h.respond(c, data, err)
}

func (h *LearningHandler) Clear(c *gin.Context) {
	data, err := h.service.Clear(c.Request.Context(), c.Query("knowledge_base_id"))
	h.respond(c, data, err)
}

func (h *LearningHandler) respond(c *gin.Context, data any, err error) {
	c.Header("Cache-Control", "no-store")
	if err == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
		return
	}
	status, code, message := learningHTTPError(err)
	if status == http.StatusInternalServerError {
		// Provider/database errors may contain answer material. Log a stable
		// code plus request ID, never the wrapped error or payload.
		logger.Warnf(c.Request.Context(), "learning request failed: code=%s", code)
	}
	c.JSON(status, gin.H{"success": false, "error": gin.H{"code": code, "message": message}})
}

func learningHTTPError(err error) (int, string, string) {
	switch {
	case errors.Is(err, types.ErrLearningForbidden):
		return 403, "learning_forbidden", "Learning is available only to Web users in their active workspace."
	case errors.Is(err, types.ErrLearningDisabled):
		return 403, "learning_disabled", "Personal learning is disabled."
	case errors.Is(err, types.ErrLearningNotFound):
		return 404, "learning_not_found", "Learning resource not found."
	case errors.Is(err, types.ErrLearningStale):
		return 409, "learning_stale", "The source changed. Prepare a new quiz."
	case errors.Is(err, types.ErrLearningNotReady):
		return 409, "learning_not_ready", "The quiz is not ready."
	case errors.Is(err, types.ErrLearningConflict):
		return 409, "learning_conflict", "The request conflicts with an earlier submission."
	case errors.Is(err, types.ErrLearningInvalid):
		return 400, "learning_invalid", "Invalid learning request."
	case errors.Is(err, types.ErrLearningBusy):
		return 429, "learning_busy", "Learning generation is busy. Try again later."
	case errors.Is(err, types.ErrLearningEvidence):
		return 422, "learning_evidence", "Not enough current source evidence for a quiz."
	default:
		return 500, "learning_unavailable", "Learning is temporarily unavailable."
	}
}

func decodeLearningRequest(c *gin.Context, target any) bool {
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128*1024)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(target)
	if err == nil {
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			err = types.ErrLearningInvalid
		}
	}
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"success": false, "error": gin.H{"code": "learning_invalid", "message": "Invalid learning request."}})
		return false
	}
	return true
}
