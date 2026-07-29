package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

const (
	progressPreStageWeight  = 1
	progressPostStageWeight = 2
	progressCountdown       = time.Second
)

type knowledgeProgressService interface {
	ListKnowledgeProgressByKnowledgeBaseID(context.Context, string) ([]*types.Knowledge, error)
	ClearTerminalProgressMarkers(context.Context, string, []string) error
}

type knowledgeProgressSpanRepository interface {
	ListLatestByKnowledgeIDs(context.Context, []string) (map[string][]types.KnowledgeProcessingSpan, error)
}
type knowledgeProgressCounts struct {
	Completed  int `json:"completed"`
	Deleted    int `json:"deleted"`
	Stopped    int `json:"stopped"`
	Incomplete int `json:"incomplete"`
}

var (
	progressPreStages = []string{
		types.StageDocReader, types.StageChunking, types.StageEmbedding, types.StageMultimodal,
	}
	progressPostStages = []string{"postprocess.question", "postprocess.summary"}
	progressWikiStages = []string{
		"postprocess.wiki.extract", "postprocess.wiki.summary", "postprocess.wiki.classify",
	}
)

// GetKnowledgeProgress returns the current aggregate progress for one KB.
func (h *KnowledgeHandler) GetKnowledgeProgress(c *gin.Context) {
	ctx := c.Request.Context()
	kbID, kb, ok := h.loadProgressKnowledgeBase(c)
	if !ok {
		return
	}

	knowledges, err := h.listProgressKnowledges(ctx, kbID)
	if err != nil {
		logger.Errorf(ctx, "failed to list knowledge progress, kb_id=%s: %v", kbID, err)
		c.Error(apperrors.NewInternalServerError("failed to load knowledge progress"))
		return
	}

	if len(knowledges) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
			"active": false, "percent": 0, "counts": knowledgeProgressCounts{},
		}})
		return
	}

	counts := progressCountsFromKnowledges(knowledges)
	wikiEnabled := kb.IsWikiEnabled()
	spansByKnowledgeID := h.progressSpansByKnowledgeID(ctx, knowledges)
	totalWeight := 0
	completedWeight := 0
	allTerminal := true
	for _, knowledge := range knowledges {
		if knowledge == nil {
			continue
		}
		total := progressDocumentTotal(wikiEnabled)
		done := total
		if !progressKnowledgeTerminal(knowledge) {
			allTerminal = false
			done = progressWeightFromSpans(spansByKnowledgeID[knowledge.ID], wikiEnabled)
		}
		totalWeight += total
		completedWeight += done
	}

	percent := 0
	if totalWeight > 0 {
		percent = completedWeight * 100 / totalWeight
	}
	if allTerminal {
		percent = 100
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"active":         true,
		"percent":        percent,
		"document_count": len(knowledges),
		"completed":      allTerminal,
		"counts":         counts,
	}})
}

// CountdownKnowledgeProgress keeps 100% visible for one second, then clears
// terminal markers for this KB. A document added during the countdown remains
// marked, and active=true tells the frontend to continue polling.
func (h *KnowledgeHandler) CountdownKnowledgeProgress(c *gin.Context) {
	ctx := c.Request.Context()
	kbID, _, ok := h.loadProgressKnowledgeBase(c)
	if !ok {
		return
	}

	service, ok := h.kgService.(knowledgeProgressService)
	if !ok {
		c.Error(apperrors.NewInternalServerError("knowledge progress is unavailable"))
		return
	}
	captured, err := service.ListKnowledgeProgressByKnowledgeBaseID(ctx, kbID)
	if err != nil {
		c.Error(apperrors.NewInternalServerError("failed to load knowledge progress"))
		return
	}
	capturedIDs := make([]string, 0, len(captured))
	capturedSet := make(map[string]struct{}, len(captured))
	for _, knowledge := range captured {
		if knowledge == nil {
			continue
		}
		if !progressKnowledgeTerminal(knowledge) {
			c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"active": true}})
			return
		}
		capturedIDs = append(capturedIDs, knowledge.ID)
		capturedSet[knowledge.ID] = struct{}{}
	}
	if len(capturedIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"active": false}})
		return
	}

	timer := time.NewTimer(progressCountdown)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}

	current, err := service.ListKnowledgeProgressByKnowledgeBaseID(ctx, kbID)
	if err != nil {
		logger.Errorf(ctx, "failed to recheck knowledge progress, kb_id=%s: %v", kbID, err)
		c.Error(apperrors.NewInternalServerError("failed to recheck knowledge progress"))
		return
	}
	if len(current) != len(capturedIDs) {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"active": true}})
		return
	}
	for _, knowledge := range current {
		if knowledge == nil {
			c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"active": true}})
			return
		}
		if _, ok := capturedSet[knowledge.ID]; !ok || !progressKnowledgeTerminal(knowledge) {
			c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"active": true}})
			return
		}
	}

	if err := service.ClearTerminalProgressMarkers(ctx, kbID, capturedIDs); err != nil {
		logger.Errorf(ctx, "failed to clear progress markers, kb_id=%s: %v", kbID, err)
		c.Error(apperrors.NewInternalServerError("failed to finish knowledge progress"))
		return
	}
	remaining, err := service.ListKnowledgeProgressByKnowledgeBaseID(ctx, kbID)
	if err != nil {
		logger.Errorf(ctx, "failed to reload knowledge progress, kb_id=%s: %v", kbID, err)
		c.Error(apperrors.NewInternalServerError("failed to reload knowledge progress"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"active": len(remaining) > 0,
	}})
}

func (h *KnowledgeHandler) loadProgressKnowledgeBase(c *gin.Context) (string, *types.KnowledgeBase, bool) {
	kbID := strings.TrimSpace(c.Param("id"))
	if kbID == "" {
		c.Error(apperrors.NewBadRequestError("knowledge base ID cannot be empty"))
		return "", nil, false
	}
	kb, err := h.kbService.GetKnowledgeBaseByID(c.Request.Context(), kbID)
	if err != nil || kb == nil {
		c.Error(apperrors.NewNotFoundError("knowledge base not found"))
		return "", nil, false
	}
	return kbID, kb, true
}

func (h *KnowledgeHandler) listProgressKnowledges(ctx context.Context, kbID string) ([]*types.Knowledge, error) {
	if service, ok := h.kgService.(knowledgeProgressService); ok {
		return service.ListKnowledgeProgressByKnowledgeBaseID(ctx, kbID)
	}
	knowledges, err := h.kgService.ListKnowledgeByKnowledgeBaseID(ctx, kbID)
	if err != nil {
		return nil, err
	}
	marked := make([]*types.Knowledge, 0, len(knowledges))
	for _, knowledge := range knowledges {
		if knowledge != nil && knowledge.ProgressMarked {
			marked = append(marked, knowledge)
		}
	}
	return marked, nil
}

func progressDocumentTotal(wikiEnabled bool) int {
	if wikiEnabled {
		return 4*progressPreStageWeight + 6*progressPostStageWeight
	}
	return 4*progressPreStageWeight + 2*progressPostStageWeight
}

func progressCountsFromKnowledges(knowledges []*types.Knowledge) knowledgeProgressCounts {
	counts := knowledgeProgressCounts{}
	for _, knowledge := range knowledges {
		if knowledge == nil {
			continue
		}
		switch {
		case knowledge.DeletedAt.Valid || knowledge.ParseStatus == types.ParseStatusDeleting:
			counts.Deleted++
		case knowledge.ParseStatus == types.ParseStatusCancelled:
			counts.Stopped++
		case knowledge.ParseStatus == types.ParseStatusCompleted:
			counts.Completed++
		default:
			counts.Incomplete++
		}
	}
	return counts
}

func progressKnowledgeTerminal(knowledge *types.Knowledge) bool {
	return knowledge != nil && (knowledge.DeletedAt.Valid || progressTerminalStatus(knowledge.ParseStatus))
}

func progressTerminalStatus(status string) bool {
	switch status {
	case types.ParseStatusCompleted, types.ParseStatusFailed,
		types.ParseStatusCancelled, types.ParseStatusDeleting:
		return true
	default:
		return false
	}
}

func progressSpanTerminal(status string) bool {
	switch status {
	case types.SpanStatusDone, types.SpanStatusSkipped,
		types.SpanStatusFailed, types.SpanStatusCancelled:
		return true
	default:
		return false
	}
}

func (h *KnowledgeHandler) progressSpansByKnowledgeID(
	ctx context.Context, knowledges []*types.Knowledge,
) map[string][]types.KnowledgeProcessingSpan {
	grouped := make(map[string][]types.KnowledgeProcessingSpan)
	if h.spanRepo == nil {
		return grouped
	}
	ids := make([]string, 0, len(knowledges))
	for _, knowledge := range knowledges {
		if knowledge != nil && !progressKnowledgeTerminal(knowledge) {
			ids = append(ids, knowledge.ID)
		}
	}
	if len(ids) == 0 {
		return grouped
	}
	repo, ok := h.spanRepo.(knowledgeProgressSpanRepository)
	if !ok {
		return grouped
	}
	rows, err := repo.ListLatestByKnowledgeIDs(ctx, ids)
	if err != nil {
		logger.Warnf(ctx, "failed to load batch knowledge progress spans: %v", err)
		return grouped
	}
	return rows
}

func progressWeightFromSpans(rows []types.KnowledgeProcessingSpan, wikiEnabled bool) int {
	total := progressDocumentTotal(wikiEnabled)
	done := 0
	for _, stage := range progressPreStages {
		if latestSpanTerminal(rows, stage) {
			done += progressPreStageWeight
		}
	}
	for _, stage := range progressPostStages {
		if latestSpanTerminal(rows, stage) {
			done += progressPostStageWeight
		}
	}
	if wikiEnabled {
		for _, stage := range progressWikiStages {
			if latestSpanTerminal(rows, stage) {
				done += progressPostStageWeight
			}
		}
		pageCount := 0
		pageDone := 0
		for _, row := range rows {
			if strings.HasPrefix(row.Name, "postprocess.wiki.page[") {
				pageCount++
				if progressSpanTerminal(row.Status) {
					pageDone++
				}
			}
		}
		if pageCount > 0 && pageCount == pageDone {
			done += progressPostStageWeight
		}
	}
	if done > total {
		return total
	}
	return done
}

func latestSpanTerminal(rows []types.KnowledgeProcessingSpan, name string) bool {
	found := false
	terminal := false
	for _, row := range rows {
		if row.Name != name {
			continue
		}
		found = true
		terminal = progressSpanTerminal(row.Status)
	}
	return found && terminal
}
