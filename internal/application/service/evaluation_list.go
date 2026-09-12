package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	evaluationDefaultListPageSize = 20
	evaluationMaxListPageSize     = 100
)

// ErrEvaluationTaskListInvalidCursor rejects malformed, outdated, or
// cross-filter cursors with a client-visible 400.
var ErrEvaluationTaskListInvalidCursor = types.ErrEvaluationTaskListInvalidCursor

// ListEvaluations returns one keyset page of the current tenant's evaluation
// tasks ordered by (start_time DESC, id DESC). Soft-deleted tasks are hidden.
func (e *EvaluationService) ListEvaluations(
	ctx context.Context,
	input types.EvaluationTaskListInput,
) (*types.EvaluationTaskListPage, error) {
	tenantID := types.MustTenantIDFromContext(ctx)

	pageSize := input.PageSize
	if pageSize < 0 {
		return nil, fmt.Errorf("%w: page_size must be positive", types.ErrEvaluationTaskQueryInvalid)
	}
	if pageSize == 0 {
		pageSize = evaluationDefaultListPageSize
	}
	if pageSize > evaluationMaxListPageSize {
		pageSize = evaluationMaxListPageSize
	}
	if input.Status != nil && !isKnownEvaluationStatus(*input.Status) {
		return nil, fmt.Errorf(
			"%w: unsupported status filter %d",
			ErrEvaluationTaskListInvalidCursor,
			*input.Status,
		)
	}
	labels, err := types.NormalizeEvaluationTaskLabels(input.Labels, types.EvaluationTaskLabelMaxFilterCount)
	if err != nil {
		return nil, err
	}
	if input.StartedFrom != nil && input.StartedTo != nil && input.StartedFrom.After(*input.StartedTo) {
		return nil, fmt.Errorf("%w: started_from must not be after started_to", types.ErrEvaluationTaskQueryInvalid)
	}
	filters := types.EvaluationTaskListFilters{
		Status:           input.Status,
		DatasetID:        strings.TrimSpace(input.DatasetID),
		DatasetVersionID: strings.TrimSpace(input.DatasetVersionID),
		ModelID:          strings.TrimSpace(input.ModelID),
		StartedFrom:      input.StartedFrom,
		StartedTo:        input.StartedTo,
		Labels:           labels,
	}

	query := types.EvaluationTaskListQuery{
		Status:           filters.Status,
		DatasetID:        filters.DatasetID,
		DatasetVersionID: filters.DatasetVersionID,
		ModelID:          filters.ModelID,
		StartedFrom:      filters.StartedFrom,
		StartedTo:        filters.StartedTo,
		Labels:           filters.Labels,
		Limit:            pageSize + 1,
	}
	if input.Cursor != "" {
		cursor, err := types.DecodeEvaluationTaskListCursor(input.Cursor, filters)
		if err != nil {
			return nil, err
		}
		startBefore := cursor.StartTime.UTC()
		query.StartBefore = &startBefore
		query.IDBefore = cursor.ID
	}

	tasks := make([]*types.EvaluationTaskEntity, 0, pageSize+1)
	scanQuery := query
	for len(tasks) <= pageSize {
		batch, err := e.evaluationTaskRepository.ListTasks(ctx, tenantID, scanQuery)
		if err != nil {
			return nil, err
		}
		for _, task := range batch {
			if AuthorizeEvaluationTaskForAPIKey(ctx, task) == nil {
				tasks = append(tasks, task)
				if len(tasks) > pageSize {
					break
				}
			}
		}
		if len(tasks) > pageSize || len(batch) < scanQuery.Limit || len(batch) == 0 {
			break
		}
		lastScanned := batch[len(batch)-1]
		startBefore := lastScanned.StartTime.UTC()
		scanQuery.StartBefore = &startBefore
		scanQuery.IDBefore = lastScanned.ID
	}
	taskIDs := make([]string, 0, len(tasks))
	for _, task := range tasks {
		taskIDs = append(taskIDs, task.ID)
	}
	labelsByTask, err := e.evaluationTaskRepository.ListTaskLabels(ctx, tenantID, taskIDs)
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		task.Labels = labelsByTask[task.ID]
		if task.Labels == nil {
			task.Labels = []string{}
		}
	}

	page := &types.EvaluationTaskListPage{Items: tasks}
	if len(tasks) > pageSize {
		page.Items = tasks[:pageSize]
		nextCursor, err := types.EncodeEvaluationTaskListCursor(types.EvaluationTaskKeyset{
			StartTime: tasks[pageSize-1].StartTime,
			ID:        tasks[pageSize-1].ID,
		}, filters)
		if err != nil {
			return nil, err
		}
		page.NextCursor = nextCursor
	}
	return page, nil
}

// ReplaceEvaluationTaskLabels validates and atomically replaces one task's labels.
func (e *EvaluationService) ReplaceEvaluationTaskLabels(
	ctx context.Context,
	taskID string,
	rawLabels []string,
) ([]string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("%w: task_id is required", types.ErrEvaluationTaskQueryInvalid)
	}
	labels, err := types.NormalizeEvaluationTaskLabels(rawLabels, types.EvaluationTaskLabelMaxPerTask)
	if err != nil {
		return nil, err
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	task, err := e.evaluationTaskRepository.GetTask(ctx, tenantID, taskID)
	if err != nil {
		return nil, err
	}
	if err := AuthorizeEvaluationTaskForAPIKey(ctx, task); err != nil {
		return nil, err
	}
	if err := e.evaluationTaskRepository.ReplaceTaskLabels(
		ctx,
		tenantID,
		taskID,
		labels,
		time.Now().UTC(),
	); err != nil {
		return nil, err
	}
	return labels, nil
}
