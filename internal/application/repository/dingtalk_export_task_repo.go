package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type DingTalkExportTaskRepository struct {
	db *gorm.DB
}

func NewDingTalkExportTaskRepository(db *gorm.DB) interfaces.DingTalkExportTaskRepository {
	return &DingTalkExportTaskRepository{db: db}
}

func (r *DingTalkExportTaskRepository) UpsertPending(ctx context.Context, task *types.DingTalkExportTask) error {
	if task == nil {
		return errors.New("dingtalk export task is nil")
	}
	if task.TaskID == "" {
		return errors.New("dingtalk export task id is empty")
	}
	if task.Status == "" {
		task.Status = types.DingTalkExportTaskStatusPending
	}

	var existing types.DingTalkExportTask
	err := r.db.WithContext(ctx).Where("task_id = ?", task.TaskID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.db.WithContext(ctx).Create(task).Error
	}
	if err != nil {
		return err
	}
	if existing.Status != types.DingTalkExportTaskStatusPending {
		return nil
	}

	return r.db.WithContext(ctx).
		Model(&types.DingTalkExportTask{}).
		Where("task_id = ?", task.TaskID).
		Updates(map[string]interface{}{
			"tenant_id":          task.TenantID,
			"data_source_id":     task.DataSourceID,
			"sync_log_id":        task.SyncLogID,
			"external_id":        task.ExternalID,
			"source_resource_id": task.SourceResourceID,
			"workspace_id":       task.WorkspaceID,
			"node_id":            task.NodeID,
			"dentry_uuid":        task.DentryUUID,
			"title":              task.Title,
			"file_name":          task.FileName,
			"source_url":         task.SourceURL,
			"status":             types.DingTalkExportTaskStatusPending,
			"event_id":           "",
			"export_url":         "",
			"error_code":         "",
			"error_message":      "",
			"finished_at":        nil,
			"updated_at":         time.Now().UTC(),
		}).Error
}

func (r *DingTalkExportTaskRepository) FindByTaskID(
	ctx context.Context,
	taskID string,
) (*types.DingTalkExportTask, error) {
	if taskID == "" {
		return nil, errors.New("dingtalk export task id is empty")
	}
	var task types.DingTalkExportTask
	if err := r.db.WithContext(ctx).Where("task_id = ?", taskID).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("dingtalk export task not found")
		}
		return nil, err
	}
	return &task, nil
}

func (r *DingTalkExportTaskRepository) FindPendingOlderThan(
	ctx context.Context,
	cutoff time.Time,
	limit int,
) ([]*types.DingTalkExportTask, error) {
	if limit <= 0 {
		limit = 100
	}
	var tasks []*types.DingTalkExportTask
	if err := r.db.WithContext(ctx).
		Where("status = ?", types.DingTalkExportTaskStatusPending).
		Where("created_at < ?", cutoff).
		Order("created_at ASC").
		Limit(limit).
		Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func (r *DingTalkExportTaskRepository) MarkSucceeded(ctx context.Context, taskID, eventID, exportURL string) error {
	return r.markTerminal(ctx, taskID, map[string]interface{}{
		"status":        types.DingTalkExportTaskStatusSucceeded,
		"event_id":      eventID,
		"export_url":    exportURL,
		"error_code":    "",
		"error_message": "",
	})
}

func (r *DingTalkExportTaskRepository) MarkFailed(
	ctx context.Context,
	taskID string,
	eventID string,
	errorCode string,
	errorMessage string,
) error {
	return r.markTerminal(ctx, taskID, map[string]interface{}{
		"status":        types.DingTalkExportTaskStatusFailed,
		"event_id":      eventID,
		"error_code":    errorCode,
		"error_message": errorMessage,
	})
}

func (r *DingTalkExportTaskRepository) markTerminal(
	ctx context.Context,
	taskID string,
	updates map[string]interface{},
) error {
	if taskID == "" {
		return errors.New("dingtalk export task id is empty")
	}
	now := time.Now().UTC()
	updates["finished_at"] = &now
	updates["updated_at"] = now
	result := r.db.WithContext(ctx).
		Model(&types.DingTalkExportTask{}).
		Where("task_id = ?", taskID).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("dingtalk export task not found")
	}
	return nil
}
