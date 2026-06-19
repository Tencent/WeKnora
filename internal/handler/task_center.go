package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

type TaskCenterHandler struct {
	repo      interfaces.TaskJobRepository
	enqueuer  interfaces.TaskEnqueuer
	kgService interfaces.KnowledgeService
}

func NewTaskCenterHandler(
	repo interfaces.TaskJobRepository,
	enqueuer interfaces.TaskEnqueuer,
	kgService interfaces.KnowledgeService,
) *TaskCenterHandler {
	return &TaskCenterHandler{repo: repo, enqueuer: enqueuer, kgService: kgService}
}

func (h *TaskCenterHandler) Summary(c *gin.Context) {
	q, ok := h.query(c)
	if !ok {
		return
	}
	summary, err := h.repo.Summary(c.Request.Context(), q)
	if err != nil {
		c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": summary})
}

func (h *TaskCenterHandler) List(c *gin.Context) {
	q, ok := h.query(c)
	if !ok {
		return
	}
	rows, total, err := h.repo.ListJobs(c.Request.Context(), q)
	if err != nil {
		c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, job := range rows {
		items = append(items, h.presentJob(c, job, nil))
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":     items,
			"total":     total,
			"page":      q.Page,
			"page_size": q.PageSize,
		},
	})
}

func (h *TaskCenterHandler) Detail(c *gin.Context) {
	job, ok := h.loadJob(c)
	if !ok {
		return
	}
	execs, _ := h.repo.ListExecutions(c.Request.Context(), job.TenantID, job.JobID)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": h.presentJob(c, job, execs)})
}

func (h *TaskCenterHandler) Executions(c *gin.Context) {
	job, ok := h.loadJob(c)
	if !ok {
		return
	}
	execs, err := h.repo.ListExecutions(c.Request.Context(), job.TenantID, job.JobID)
	if err != nil {
		c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": execs})
}

func (h *TaskCenterHandler) Retry(c *gin.Context) {
	job, ok := h.loadJob(c)
	if !ok {
		return
	}
	executionID, err := h.retryJob(c, job)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"execution_id": executionID}})
}

func (h *TaskCenterHandler) RetryAll(c *gin.Context) {
	if !taskCenterAdmin(c) {
		c.Error(apperrors.NewForbiddenError("admin required"))
		return
	}
	q, ok := h.query(c)
	if !ok {
		return
	}
	q.State = "failed_or_canceled"
	q.Page = 1
	q.PageSize = 100
	rows, _, err := h.repo.ListJobs(c.Request.Context(), q)
	if err != nil {
		c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
	retried := 0
	for _, job := range rows {
		if _, err := h.retryJob(c, job); err == nil {
			retried++
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"retried": retried}})
}

func (h *TaskCenterHandler) retryJob(c *gin.Context, job *types.TaskJob) (string, error) {
	execs, _ := h.repo.ListExecutions(c.Request.Context(), job.TenantID, job.JobID)
	if !canRetryJob(job, execs) {
		return "", apperrors.NewBadRequestError("task cannot be retried")
	}
	payload, queue, err := replayDocumentPayload(job)
	if err != nil {
		return "", apperrors.NewBadRequestError(err.Error())
	}
	executionID := uuid.NewString()
	exec, changed, err := h.repo.PrepareManualRetry(c.Request.Context(), job.JobID, executionID, types.TypeDocumentProcess, queue, time.Now())
	if err != nil {
		return "", apperrors.NewInternalServerError(err.Error())
	}
	if !changed || exec == nil {
		return "", apperrors.NewBadRequestError("task cannot be retried")
	}
	payload.Attempt = exec.ProcessAttempt
	raw, _ := json.Marshal(payload)
	task := asynq.NewTask(types.TypeDocumentProcess, raw)
	info, err := h.enqueuer.Enqueue(task, asynq.Queue(queue), asynq.TaskID(executionID), asynq.MaxRetry(3))
	if err != nil {
		_, _ = h.repo.MarkDispatchFailed(c.Request.Context(), job.JobID, executionID, interfaces.TaskLedgerFailure{
			ErrorClass: types.TaskErrorClassEnqueueFailed,
			LastError:  err.Error(),
		}, time.Now())
		return "", apperrors.NewInternalServerError(err.Error())
	}
	_, _ = h.repo.MarkDispatched(c.Request.Context(), executionID, time.Now())
	_ = info
	return executionID, nil
}

func (h *TaskCenterHandler) Cancel(c *gin.Context) {
	job, ok := h.loadJob(c)
	if !ok {
		return
	}
	if !canCancelJob(job) {
		c.Error(apperrors.NewBadRequestError("task cannot be canceled"))
		return
	}
	if job.Scope == types.TaskScopeKnowledge && h.kgService != nil {
		if _, err := h.kgService.CancelKnowledgeParse(c.Request.Context(), job.ScopeID); err != nil {
			if appErr, ok := apperrors.IsAppError(err); ok {
				c.Error(appErr)
				return
			}
			c.Error(apperrors.NewInternalServerError(err.Error()))
			return
		}
	}
	sel := interfaces.TaskJobAttemptSelector{TenantID: job.TenantID, Scope: job.Scope, ScopeID: job.ScopeID, ProcessAttempt: job.ProcessAttempt}
	_, _ = h.repo.MarkJobCanceledIfCurrentAttempt(c.Request.Context(), sel, interfaces.TaskLedgerFailure{
		ErrorClass: types.TaskErrorClassCanceled,
		LastError:  "canceled by user",
	}, time.Now())
	execs, _ := h.repo.ListExecutions(c.Request.Context(), job.TenantID, job.JobID)
	for _, exec := range execs {
		if !types.TaskExecutionIsTerminal(exec.State) {
			_, _ = h.repo.MarkExecCanceledIfNonTerminal(c.Request.Context(), exec.ExecutionID, interfaces.TaskLedgerFailure{
				ErrorClass: types.TaskErrorClassCanceled,
				LastError:  "canceled by user",
			}, time.Now())
			break
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *TaskCenterHandler) DeleteRecords(c *gin.Context) {
	if !taskCenterAdmin(c) {
		c.Error(apperrors.NewForbiddenError("admin required"))
		return
	}
	days, _ := strconv.Atoi(c.DefaultQuery("older_than_days", "30"))
	if days <= 0 {
		days = 30
	}
	deleted, err := h.repo.DeleteTerminalJobsFinishedBefore(c.Request.Context(), time.Now().AddDate(0, 0, -days), 1000)
	if err != nil {
		c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"deleted": deleted}})
}

func (h *TaskCenterHandler) query(c *gin.Context) (interfaces.TaskJobQuery, bool) {
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(apperrors.NewUnauthorizedError("tenant ID not found"))
		return interfaces.TaskJobQuery{}, false
	}
	userID, _ := types.UserIDFromContext(c.Request.Context())
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	q := interfaces.TaskJobQuery{
		TenantID:  tenantID,
		UserID:    userID,
		IsAdmin:   taskCenterAdmin(c),
		State:     c.Query("state"),
		Kind:      c.Query("kind"),
		KBID:      c.Query("kb_id"),
		CreatedBy: c.Query("created_by"),
		Q:         c.Query("q"),
		Page:      page,
		PageSize:  pageSize,
		Sort:      c.Query("sort"),
		Origin:    string(types.TaskJobOriginUser),
	}
	return q, true
}

func (h *TaskCenterHandler) loadJob(c *gin.Context) (*types.TaskJob, bool) {
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	job, err := h.repo.GetJob(c.Request.Context(), tenantID, c.Param("job_id"))
	if err != nil {
		c.Error(apperrors.NewInternalServerError(err.Error()))
		return nil, false
	}
	if job == nil {
		c.Error(apperrors.NewNotFoundError("task not found"))
		return nil, false
	}
	if !taskCenterAdmin(c) {
		userID, _ := types.UserIDFromContext(c.Request.Context())
		if job.CreatedBy != userID {
			c.Error(apperrors.NewForbiddenError("permission denied"))
			return nil, false
		}
	}
	return job, true
}

func (h *TaskCenterHandler) presentJob(c *gin.Context, job *types.TaskJob, execs []*types.TaskExecution) gin.H {
	if execs == nil {
		execs, _ = h.repo.ListExecutions(c.Request.Context(), job.TenantID, job.JobID)
	}
	return gin.H{
		"job_id":           job.JobID,
		"tenant_id":        job.TenantID,
		"created_by":       job.CreatedBy,
		"kind":             job.Kind,
		"origin":           job.Origin,
		"display_name":     job.DisplayName,
		"scope":            job.Scope,
		"scope_id":         job.ScopeID,
		"related_id":       job.RelatedID,
		"process_attempt":  job.ProcessAttempt,
		"state":            job.State,
		"metadata":         job.Metadata,
		"last_error_class": job.LastErrorClass,
		"last_error":       job.LastError,
		"failed_task_type": job.FailedTaskType,
		"failed_task_id":   job.FailedTaskID,
		"created_at":       job.CreatedAt,
		"updated_at":       job.UpdatedAt,
		"finished_at":      job.FinishedAt,
		"capabilities": gin.H{
			"can_retry":         canRetryJob(job, execs),
			"can_cancel":        canCancelJob(job),
			"can_delete_record": taskCenterAdmin(c) && types.TaskJobIsTerminal(job.State),
		},
	}
}

func taskCenterAdmin(c *gin.Context) bool {
	role := types.TenantRoleFromContext(c.Request.Context())
	return role == types.TenantRoleAdmin || role == types.TenantRoleOwner
}
