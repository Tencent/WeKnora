package router

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/backup"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

const maxManualBackupReasonRunes = 512

type manualBackupCreator interface {
	CreateManual(context.Context, string) (backup.Result, error)
}

type manualBackupRequest struct {
	Reason string `json:"reason"`
}

func (o *operationsObserver) createManualBackup(c *gin.Context) {
	if o.backupManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Manual backup is unavailable"})
		return
	}
	var request manualBackupRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A backup reason is required"})
		return
	}
	reason := strings.TrimSpace(request.Reason)
	if reason == "" || utf8.RuneCountInString(reason) > maxManualBackupReasonRunes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Backup reason must contain 1 to 512 characters"})
		return
	}

	result, err := o.backupManager.CreateManual(c.Request.Context(), reason)
	if err != nil {
		o.emitManualBackupAudit(c, result, reason, err)
		c.JSON(manualBackupErrorStatus(err), gin.H{
			"error": "Manual backup failed",
			"code":  manualBackupErrorCode(err),
		})
		return
	}

	o.emitManualBackupAudit(c, result, reason, nil)
	c.JSON(http.StatusCreated, result)
}

func manualBackupErrorStatus(err error) int {
	switch {
	case backup.IsErrorKind(err, backup.ErrorDisabled),
		backup.IsErrorKind(err, backup.ErrorUnsupportedDatabase),
		backup.IsErrorKind(err, backup.ErrorInProgress):
		return http.StatusConflict
	case backup.IsErrorKind(err, backup.ErrorConfiguration), backup.IsErrorKind(err, backup.ErrorStorage):
		return http.StatusServiceUnavailable
	case backup.IsErrorKind(err, backup.ErrorTimeout):
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}

func manualBackupErrorCode(err error) string {
	for _, kind := range []backup.ErrorKind{
		backup.ErrorDisabled,
		backup.ErrorUnsupportedDatabase,
		backup.ErrorConfiguration,
		backup.ErrorInProgress,
		backup.ErrorStorage,
		backup.ErrorIntegrity,
		backup.ErrorDump,
		backup.ErrorTimeout,
		backup.ErrorInsufficientSpace,
	} {
		if backup.IsErrorKind(err, kind) {
			return string(kind)
		}
	}
	return "backup_failed"
}

func (o *operationsObserver) emitScheduledBackupAudit(run backup.ScheduledRun) {
	if o.auditService == nil {
		return
	}
	action := types.AuditActionSystemBackupCreated
	outcome := types.AuditOutcomeSuccess
	details := map[string]any{
		"backup_id":         run.Result.BackupID,
		"trigger":           "scheduled",
		"manifest_file":     run.Result.ManifestFile,
		"retention_deleted": run.RetentionDeleted,
	}
	if run.Err != nil {
		action = types.AuditActionSystemBackupFailed
		outcome = types.AuditOutcomeFailed
		details["failure_kind"] = manualBackupErrorCode(run.Err)
	} else {
		details["archive_file"] = run.Result.ArchiveFile
		details["size_bytes"] = run.Result.SizeBytes
		details["sha256"] = run.Result.SHA256
		details["files_archive_file"] = run.Result.FilesArchiveFile
		details["files_inventory_file"] = run.Result.FilesInventoryFile
		details["files_count"] = run.Result.FilesCount
		details["qdrant_snapshot_count"] = run.Result.QdrantSnapshotCount
		details["retention_failed"] = run.RetentionFailed
	}
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return
	}
	_ = o.auditService.Log(context.Background(), &types.AuditLog{
		TenantID:   0,
		ActorRole:  "system",
		Action:     action,
		TargetType: "mysql_backup",
		TargetID:   run.Result.BackupID,
		Outcome:    outcome,
		Details:    types.JSON(detailsJSON),
	})
}

func (o *operationsObserver) emitManualBackupAudit(c *gin.Context, result backup.Result, reason string, operationErr error) {
	if o.auditService == nil {
		return
	}
	action := types.AuditActionSystemBackupCreated
	outcome := types.AuditOutcomeSuccess
	details := map[string]any{
		"backup_id":     result.BackupID,
		"reason":        reason,
		"manifest_file": result.ManifestFile,
	}
	if operationErr != nil {
		action = types.AuditActionSystemBackupFailed
		outcome = types.AuditOutcomeFailed
		details["failure_kind"] = manualBackupErrorCode(operationErr)
	} else {
		details["archive_file"] = result.ArchiveFile
		details["size_bytes"] = result.SizeBytes
		details["sha256"] = result.SHA256
		details["files_archive_file"] = result.FilesArchiveFile
		details["files_inventory_file"] = result.FilesInventoryFile
		details["files_count"] = result.FilesCount
		details["qdrant_snapshot_count"] = result.QdrantSnapshotCount
	}
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return
	}
	actorID, _ := types.UserIDFromContext(c.Request.Context())
	_ = o.auditService.Log(c.Request.Context(), &types.AuditLog{
		TenantID:      0,
		ActorUserID:   actorID,
		ActorRole:     "system_admin",
		Action:        action,
		TargetType:    "mysql_backup",
		TargetID:      result.BackupID,
		RequestPath:   c.FullPath(),
		RequestMethod: c.Request.Method,
		Outcome:       outcome,
		Details:       types.JSON(detailsJSON),
	})
}
