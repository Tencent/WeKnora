package handler

import (
	"net/http"
	"strconv"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
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
	jobIDs := make([]string, 0, len(rows))
	for _, job := range rows {
		jobIDs = append(jobIDs, job.JobID)
	}
	execsByJob, err := h.repo.ListExecutionsForJobs(c.Request.Context(), q.TenantID, jobIDs)
	if err != nil {
		c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, job := range rows {
		items = append(items, h.presentJob(c, job, execsByJob[job.JobID]))
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
	execs, err := h.repo.ListExecutions(c.Request.Context(), job.TenantID, job.JobID)
	if err != nil {
		c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
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
	q.PageSize = 100
	matched, retried, skipped, failed := int64(0), int64(0), int64(0), int64(0)
	for page := 1; ; page++ {
		q.Page = page
		rows, total, err := h.repo.ListJobs(c.Request.Context(), q)
		if err != nil {
			c.Error(apperrors.NewInternalServerError(err.Error()))
			return
		}
		if page == 1 {
			matched = total
		}
		if len(rows) == 0 {
			break
		}
		for _, job := range rows {
			if _, err := h.retryJob(c, job); err == nil {
				retried++
			} else if appErr, ok := apperrors.IsAppError(err); ok && appErr.Code == apperrors.ErrBadRequest {
				skipped++
			} else {
				failed++
			}
		}
		if int64(page*q.PageSize) >= total {
			break
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"matched": matched,
		"retried": retried,
		"skipped": skipped,
		"failed":  failed,
	}})
}

func (h *TaskCenterHandler) retryJob(c *gin.Context, job *types.TaskJob) (string, error) {
	execs, _ := h.repo.ListExecutions(c.Request.Context(), job.TenantID, job.JobID)
	if !canRetryJob(job, execs) {
		return "", apperrors.NewBadRequestError("task cannot be retried")
	}
	if h.kgService == nil || job.Scope != types.TaskScopeKnowledge {
		return "", apperrors.NewBadRequestError("task cannot be retried")
	}
	userID, _ := types.UserIDFromContext(c.Request.Context())
	_, exec, err := h.kgService.RetryKnowledgeTask(c.Request.Context(), job.JobID, userID)
	if err != nil {
		if appErr, ok := apperrors.IsAppError(err); ok {
			return "", appErr
		}
		return "", apperrors.NewInternalServerError(err.Error())
	}
	if exec == nil {
		return "", apperrors.NewInternalServerError("retry did not create an execution")
	}
	return exec.ExecutionID, nil
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
		if err := h.kgService.CancelKnowledgeAttempt(c.Request.Context(), job.ScopeID, job.ProcessAttempt, job.JobID); err != nil {
			if appErr, ok := apperrors.IsAppError(err); ok {
				c.Error(appErr)
				return
			}
			c.Error(apperrors.NewInternalServerError(err.Error()))
			return
		}
	}
	sel := interfaces.TaskJobAttemptSelector{JobID: job.JobID, TenantID: job.TenantID, Scope: job.Scope, ScopeID: job.ScopeID, ProcessAttempt: job.ProcessAttempt}
	_, _ = h.repo.MarkJobCanceledIfCurrentAttempt(c.Request.Context(), sel, interfaces.TaskLedgerFailure{
		ErrorClass: types.TaskErrorClassCanceled,
		LastError:  "canceled by user",
	}, time.Now())
	_, _ = h.repo.MarkExecutionsCanceledForJob(c.Request.Context(), job.TenantID, job.JobID, interfaces.TaskLedgerFailure{
		ErrorClass: types.TaskErrorClassCanceled,
		LastError:  "canceled by user",
	}, time.Now())
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
		execs = []*types.TaskExecution{}
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
