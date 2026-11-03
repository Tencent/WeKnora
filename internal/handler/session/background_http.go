package session

import (
	"context"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type backgroundWorkItem struct {
	WorkID           string `json:"work_id"`
	Kind             string `json:"kind"`
	Title            string `json:"title"`
	State            string `json:"state"`
	StartedAt        string `json:"started_at,omitempty"`
	FinishedAt       string `json:"finished_at,omitempty"`
	ParentToolCallID string `json:"parent_tool_call_id,omitempty"`
}

// ListBackgroundWork returns the session's background items: live ones from the
// activity registry plus notices finished within the last 24h. Terminal states
// come from Handler.Status with Detailed false. Nothing here execs a sandbox.
func (h *Handler) ListBackgroundWork(c *gin.Context) {
	sessionID := paramSessionID(c)
	ctx := c.Request.Context()
	session, err := h.sessionService.GetSession(ctx, sessionID)
	if err != nil || session == nil {
		writeNotFound(c, "session not found")
		return
	}
	items := make([]backgroundWorkItem, 0)
	if h.background == nil || !h.background.Enabled() {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": items})
		return
	}
	ref := sessionWorkRef(session)
	live, err := h.background.Registry.List(ctx, ref)
	if err != nil {
		logger.Warnf(ctx, "list background work session=%s: %v", sessionID, err)
		writeUnavailable(c, "background work is temporarily unavailable")
		return
	}
	liveSet := make(map[string]struct{}, len(live))
	for _, activity := range live {
		liveSet[activity.ID] = struct{}{}
		items = append(items, backgroundWorkItem{
			WorkID:    activity.ID,
			Kind:      activity.Kind,
			Title:     activity.Title,
			State:     interfaces.WorkStateRunning,
			StartedAt: formatWorkTime(activity.StartedAt),
		})
	}
	notices, err := h.background.Inbox.Pending(ctx, ref)
	if err != nil {
		logger.Warnf(ctx, "pending background notices session=%s: %v", sessionID, err)
		writeUnavailable(c, "background work is temporarily unavailable")
		return
	}
	terminalIDs := make([]string, 0, len(notices))
	for _, notice := range notices {
		if _, ok := liveSet[notice.ID]; ok || notice.ID == "" {
			continue
		}
		terminalIDs = append(terminalIDs, notice.ID)
	}
	resolved := h.terminalWorkResults(ctx, ref, terminalIDs)
	for _, notice := range notices {
		if _, ok := liveSet[notice.ID]; ok {
			continue
		}
		result, ok := resolved[notice.ID]
		state, title := terminalWorkView(notice, result, ok)
		items = append(items, backgroundWorkItem{
			WorkID:     notice.ID,
			Kind:       notice.Kind,
			Title:      title,
			State:      state,
			FinishedAt: formatWorkTime(notice.FinishedAt),
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": items})
}

// GetBackgroundWork returns one item's detail with output. RunID stays empty:
// a user-triggered read must not move the model's incremental cursor.
func (h *Handler) GetBackgroundWork(c *gin.Context) {
	sessionID := paramSessionID(c)
	workID := c.Param("work_id")
	ctx := c.Request.Context()
	session, err := h.sessionService.GetSession(ctx, sessionID)
	if err != nil || session == nil {
		writeNotFound(c, "session not found")
		return
	}
	if h.background == nil || !h.background.Enabled() {
		writeNotFound(c, "background work is not available")
		return
	}
	ref := sessionWorkRef(session)
	results, err := h.background.Handler.Status(ctx, ref, []string{workID}, interfaces.StatusOptions{
		Detailed: true,
		RunID:    "",
	})
	if err != nil || len(results) != 1 {
		writeNotFound(c, "background work not found")
		return
	}
	id := results[0].ID
	if id == "" {
		id = workID
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"work_id": id,
		"state":   results[0].State,
		"title":   results[0].Title,
		"result":  results[0].Payload,
	}})
}

// CancelBackgroundWork stops one item. Strictly owner-only: mutations must not
// honour admin read access (same GetOwnedSession rule as steer).
func (h *Handler) CancelBackgroundWork(c *gin.Context) {
	sessionID := paramSessionID(c)
	workID := c.Param("work_id")
	ctx := c.Request.Context()
	session, err := h.sessionService.GetOwnedSession(ctx, sessionID)
	if err != nil || session == nil {
		writeNotFound(c, "session not found")
		return
	}
	if h.background == nil || !h.background.Enabled() {
		writeNotFound(c, "background work is not available")
		return
	}
	ref := sessionWorkRef(session)
	results, err := h.background.Handler.Cancel(ctx, ref, []string{workID})
	if err != nil || len(results) != 1 {
		writeNotFound(c, "background work not found")
		return
	}
	id := results[0].ID
	if id == "" {
		id = workID
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"work_id": id,
		"state":   results[0].State,
	}})
}

func sessionWorkRef(session *types.Session) interfaces.SessionRef {
	return interfaces.SessionRef{TenantID: session.TenantID, SessionID: session.ID}
}

func (h *Handler) terminalWorkResults(
	ctx context.Context, ref interfaces.SessionRef, ids []string,
) map[string]interfaces.WorkResult {
	out := make(map[string]interfaces.WorkResult, len(ids))
	if len(ids) == 0 || h.background == nil || h.background.Handler == nil {
		return out
	}
	results, err := h.background.Handler.Status(ctx, ref, ids, interfaces.StatusOptions{
		Detailed: false,
		RunID:    "",
	})
	if err != nil {
		logger.Warnf(ctx, "background work status session=%s: %v", ref.SessionID, err)
		return out
	}
	for _, result := range results {
		out[result.ID] = result
	}
	return out
}

func terminalWorkView(
	notice interfaces.BackgroundNotice, result interfaces.WorkResult, ok bool,
) (string, string) {
	if ok && result.State != "" && result.State != interfaces.WorkStateUnknown {
		return result.State, result.Title
	}
	if parsed := service.ParseShellJobNoticeState(notice.Text); parsed != "" {
		if ok {
			return parsed, result.Title
		}
		return parsed, ""
	}
	if ok {
		return interfaces.WorkStateLost, result.Title
	}
	return interfaces.WorkStateLost, ""
}

func formatWorkTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// writeNotFound returns HTTP 404 itself. Session routes in production also
// install ErrorHandler, which only writes when c.Errors is set; writing here
// keeps a router that has no error middleware on the same status.
func writeNotFound(c *gin.Context, message string) {
	writeAppError(c, errors.NewNotFoundError(message))
}

// writeUnavailable is a retryable failure. An empty 200 would clear the chip
// and stop the client's poll, while the jobs are still running.
func writeUnavailable(c *gin.Context, message string) {
	writeAppError(c, errors.NewServiceUnavailableError(message))
}

func writeAppError(c *gin.Context, appErr *errors.AppError) {
	c.AbortWithStatusJSON(appErr.HTTPCode, gin.H{
		"success": false,
		"error": gin.H{
			"code":    appErr.Code,
			"message": appErr.Message,
			"details": appErr.Details,
		},
	})
}
