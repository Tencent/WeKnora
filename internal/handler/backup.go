package handler

// backup.go implements the full-instance backup API (#675, #2887):
//
//	GET    /api/v1/backups/export          — stream a fresh backup archive (download)
//	POST   /api/v1/backups                 — create a server-side snapshot with a note
//	GET    /api/v1/backups                 — list snapshots
//	GET    /api/v1/backups/:id/download    — download a snapshot
//	DELETE /api/v1/backups/:id             — delete a snapshot
//	POST   /api/v1/backups/restore         — restore (by snapshot id or uploaded archive)
//
// All endpoints are SystemAdmin-only: archives contain every tenant's data.
//
// Archive layout (tar.gz):
//
//	manifest.json   — schema version, timestamps, instance/version info, includes
//	db.sql.gz       — plain-format pg_dump (postgres) or db.sqlite — VACUUM INTO (sqlite)
//	files.tar       — uploaded files, paths relative to LOCAL_STORAGE_BASE_DIR (local storage only)
//
// Restore replaces the whole database (drop/create + single-transaction import),
// so primary keys, foreign keys and sequences stay exactly consistent — there is
// no row merging and no ID remapping. A pre-restore snapshot is taken first and
// kept in the backup dir as the rollback point. File restore is a replace (not
// a merge). After a restore the Redis queues are flushed and an application
// restart is required.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	backupSchemaVersion = 1
	backupManifestFile  = "manifest.json"
	backupDBPostgres    = "db.sql.gz"
	backupDBSQLite      = "db.sqlite"
	backupFilesArchive  = "files.tar"
	backupMultipartFile = "file"

	// Snapshots live under the files volume so they survive container
	// replacement. These directory names are skipped during file export
	// and never overwritten during file restore.
	backupSnapshotsDirName = ".weknora-backups"
	backupRestoreNewDir    = ".weknora-restore-new"
	backupRestoreOldDir    = ".weknora-restore-old"

	backupDirPerm  os.FileMode = 0o700
	backupFilePerm os.FileMode = 0o600

	backupRestoreTimeout      = 4 * time.Hour
	backupUploadEnvelopeSlack = 1 << 20
)

var (
	errExtractLimit = stderrors.New("archive exceeds extraction size limit")
	versionCoreRe   = regexp.MustCompile(`(?i)^v?(\d+)\.(\d+)\.(\d+)`)
)

type backupIncludes struct {
	Database bool `json:"database"`
	Files    bool `json:"files"`
}

// backupManifest describes an archive's provenance and contents. The restore
// path refuses archives whose schema version or db driver it does not know.
type backupManifest struct {
	SchemaVersion  int            `json:"schema_version"`
	CreatedAt      string         `json:"created_at"`
	WeKnoraVersion string         `json:"weknora_version"`
	Edition        string         `json:"edition"`
	DBDriver       string         `json:"db_driver"`
	StorageType    string         `json:"storage_type"`
	FilesBaseDir   string         `json:"files_base_dir,omitempty"`
	Includes       backupIncludes `json:"includes"`
}

// snapshotMeta is the sidecar JSON stored next to each server-side snapshot.
type snapshotMeta struct {
	ID        string         `json:"id"`
	Note      string         `json:"note"`
	CreatedAt string         `json:"created_at"`
	SizeBytes int64          `json:"size_bytes"`
	Manifest  backupManifest `json:"manifest"`
}

// BackupHandler serves the backup endpoints.
type BackupHandler struct {
	cfg   *config.Config
	db    *gorm.DB
	redis *redis.Client
	audit interfaces.AuditLogService
}

// NewBackupHandler creates the backup handler.
func NewBackupHandler(
	cfg *config.Config,
	db *gorm.DB,
	redisClient *redis.Client,
	audit interfaces.AuditLogService,
) *BackupHandler {
	return &BackupHandler{cfg: cfg, db: db, redis: redisClient, audit: audit}
}

// restoreInFlight guards against overlapping restore attempts; exports and
// snapshots are allowed to run concurrently with each other.
var restoreInFlight atomic.Bool

// ---------------------------------------------------------------------------
// Endpoints
// ---------------------------------------------------------------------------

// Export godoc
// @Summary      Export a full-instance backup
// @Description  SystemAdmin only. Streams a tar.gz of the DB dump and local files.
// @Tags         backup
// @Produce      application/octet-stream
// @Success      200  {file}  binary
// @Failure      403  {object}  apperrors.AppError
// @Failure      500  {object}  apperrors.AppError
// @Security     Bearer
// @Router       /backups/export [get]
func (h *BackupHandler) Export(c *gin.Context) {
	ctx := c.Request.Context()
	bundle, cleanup, err := h.buildExport(ctx)
	if err != nil {
		logger.Errorf(ctx, "[backup] export failed: %v", err)
		_ = c.Error(apperrors.NewInternalServerError("backup export failed"))
		return
	}
	defer cleanup()
	name := "weknora-backup-" + time.Now().UTC().Format("20060102-150405") + ".tar.gz"
	logger.Infof(ctx, "[backup] export served: %s", name)
	h.emitBackupAudit(ctx, types.AuditActionSystemBackupExported, name, nil)
	c.FileAttachment(bundle, name)
}

// SnapshotRequest is the body of POST /backups.
type SnapshotRequest struct {
	Note string `json:"note"`
}

// CreateSnapshot godoc
// @Summary      Create a server-side backup snapshot
// @Description  SystemAdmin only. Stores a snapshot under BACKUP_DIR.
// @Tags         backup
// @Accept       json
// @Produce      json
// @Param        request  body  SnapshotRequest  false  "Optional note"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  apperrors.AppError
// @Failure      403  {object}  apperrors.AppError
// @Failure      500  {object}  apperrors.AppError
// @Security     Bearer
// @Router       /backups [post]
func (h *BackupHandler) CreateSnapshot(c *gin.Context) {
	ctx := c.Request.Context()
	var req SnapshotRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		_ = c.Error(apperrors.NewBadRequestError("invalid request body"))
		return
	}
	dir, err := h.backupDir()
	if err != nil {
		_ = c.Error(apperrors.NewInternalServerError("backup dir unavailable"))
		return
	}
	bundle, manifest, cleanup, err := h.buildExportWithManifest(ctx)
	if err != nil {
		logger.Errorf(ctx, "[backup] snapshot failed: %v", err)
		_ = c.Error(apperrors.NewInternalServerError("backup snapshot failed"))
		return
	}
	defer cleanup()

	id, final, err := uniqueSnapshotPath(dir, "weknora-snapshot-"+time.Now().UTC().Format("20060102-150405"))
	if err != nil {
		_ = c.Error(apperrors.NewInternalServerError("backup snapshot failed"))
		return
	}
	if err := moveFile(bundle, final); err != nil {
		logger.Errorf(ctx, "[backup] snapshot move failed: %v", err)
		_ = c.Error(apperrors.NewInternalServerError("backup snapshot failed"))
		return
	}
	meta := snapshotMeta{
		ID:        id,
		Note:      strings.TrimSpace(req.Note),
		CreatedAt: manifest.CreatedAt,
		Manifest:  *manifest,
	}
	if st, err := os.Stat(final); err == nil {
		meta.SizeBytes = st.Size()
	}
	if err := writeSnapshotMeta(dir, meta); err != nil {
		_ = os.Remove(final)
		logger.Errorf(ctx, "[backup] snapshot sidecar write failed: %v", err)
		_ = c.Error(apperrors.NewInternalServerError("backup snapshot failed"))
		return
	}
	logger.Infof(ctx, "[backup] snapshot created: %s (%d bytes)", id, meta.SizeBytes)
	h.emitBackupAudit(
		ctx,
		types.AuditActionSystemBackupSnapshotCreated,
		id,
		map[string]any{"size_bytes": meta.SizeBytes},
	)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": meta})
}

// ListSnapshots godoc
// @Summary      List server-side backup snapshots
// @Description  SystemAdmin only. Newest first.
// @Tags         backup
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      403  {object}  apperrors.AppError
// @Failure      500  {object}  apperrors.AppError
// @Security     Bearer
// @Router       /backups [get]
func (h *BackupHandler) ListSnapshots(c *gin.Context) {
	dir, err := h.backupDir()
	if err != nil {
		_ = c.Error(apperrors.NewInternalServerError("backup dir unavailable"))
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		_ = c.Error(apperrors.NewInternalServerError("backup dir unreadable"))
		return
	}
	metas := make([]snapshotMeta, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		meta, err := readSnapshotMeta(dir, strings.TrimSuffix(e.Name(), ".tar.gz"))
		if err != nil {
			continue // orphan archive without sidecar; skip rather than fail the listing
		}
		metas = append(metas, meta)
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].CreatedAt > metas[j].CreatedAt })
	c.JSON(http.StatusOK, gin.H{"success": true, "data": metas})
}

// DownloadSnapshot godoc
// @Summary      Download a stored backup snapshot
// @Description  SystemAdmin only.
// @Tags         backup
// @Produce      application/octet-stream
// @Param        id  path  string  true  "Snapshot id"
// @Success      200  {file}  binary
// @Failure      400  {object}  apperrors.AppError
// @Failure      403  {object}  apperrors.AppError
// @Security     Bearer
// @Router       /backups/{id}/download [get]
func (h *BackupHandler) DownloadSnapshot(c *gin.Context) {
	id := c.Param("id")
	if err := validateSnapshotID(id); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid snapshot id"))
		return
	}
	dir, err := h.backupDir()
	if err != nil {
		_ = c.Error(apperrors.NewInternalServerError("backup dir unavailable"))
		return
	}
	path := filepath.Join(dir, id+".tar.gz")
	if _, err := os.Stat(path); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("snapshot not found: " + id))
		return
	}
	c.FileAttachment(path, id+".tar.gz")
}

// DeleteSnapshot godoc
// @Summary      Delete a stored backup snapshot
// @Description  SystemAdmin only.
// @Tags         backup
// @Produce      json
// @Param        id  path  string  true  "Snapshot id"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  apperrors.AppError
// @Failure      403  {object}  apperrors.AppError
// @Security     Bearer
// @Router       /backups/{id} [delete]
func (h *BackupHandler) DeleteSnapshot(c *gin.Context) {
	id := c.Param("id")
	if err := validateSnapshotID(id); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid snapshot id"))
		return
	}
	dir, err := h.backupDir()
	if err != nil {
		_ = c.Error(apperrors.NewInternalServerError("backup dir unavailable"))
		return
	}
	if err := os.Remove(filepath.Join(dir, id+".tar.gz")); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("snapshot not found: " + id))
		return
	}
	_ = os.Remove(filepath.Join(dir, id+".meta.json"))
	h.emitBackupAudit(c.Request.Context(), types.AuditActionSystemBackupSnapshotDeleted, id, nil)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RestoreRequest is the multipart/JSON body of POST /backups/restore.
// Either SnapshotID (a stored snapshot) or an uploaded archive is required;
// Confirm must be true — restore replaces the whole database.
type RestoreRequest struct {
	SnapshotID string `json:"snapshot_id" form:"snapshot_id"`
	Confirm    bool   `json:"confirm" form:"confirm"`
}

// Restore godoc
// @Summary      Restore from a snapshot or uploaded archive
// @Description  SystemAdmin only. Replaces the database. Requires confirm=true.
// @Tags         backup
// @Accept       multipart/form-data
// @Produce      json
// @Param        confirm      formData  bool    true   "Must be true"
// @Param        snapshot_id  formData  string  false  "Stored snapshot id"
// @Param        file         formData  file    false  "Uploaded archive (tar.gz)"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  apperrors.AppError
// @Failure      403  {object}  apperrors.AppError
// @Failure      500  {object}  apperrors.AppError
// @Security     Bearer
// @Router       /backups/restore [post]
func (h *BackupHandler) Restore(c *gin.Context) {
	ctx := c.Request.Context()

	maxBytes := secutils.GetMaxBackupArchiveSize()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes+backupUploadEnvelopeSlack)

	var req RestoreRequest
	if err := c.ShouldBind(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if stderrors.As(err, &tooLarge) {
			_ = c.Error(apperrors.NewBadRequestError(
				fmt.Sprintf("backup archive cannot exceed %d MB", secutils.GetMaxBackupArchiveSizeMB())))
			return
		}
	}
	if !req.Confirm {
		_ = c.Error(apperrors.NewBadRequestError("restore requires confirm=true (it replaces the entire database)"))
		return
	}
	if restoreInFlight.Swap(true) {
		_ = c.Error(apperrors.NewInternalServerError("another restore is already in progress"))
		return
	}
	defer restoreInFlight.Store(false)

	tmpDir, err := os.MkdirTemp("", "weknora-restore-")
	if err != nil {
		_ = c.Error(apperrors.NewInternalServerError("restore failed"))
		return
	}
	defer os.RemoveAll(tmpDir)

	archivePath := ""
	if f, ferr := c.FormFile(backupMultipartFile); ferr == nil {
		archivePath = filepath.Join(tmpDir, "upload.tar.gz")
		if err := c.SaveUploadedFile(f, archivePath); err != nil {
			var tooLarge *http.MaxBytesError
			if stderrors.As(err, &tooLarge) {
				_ = c.Error(apperrors.NewBadRequestError(
					fmt.Sprintf("backup archive cannot exceed %d MB", secutils.GetMaxBackupArchiveSizeMB())))
				return
			}
			_ = c.Error(apperrors.NewBadRequestError("failed to read uploaded archive"))
			return
		}
	} else if id := strings.TrimSpace(req.SnapshotID); id != "" {
		if err := validateSnapshotID(id); err != nil {
			_ = c.Error(apperrors.NewBadRequestError("invalid snapshot id"))
			return
		}
		dir, err := h.backupDir()
		if err != nil {
			_ = c.Error(apperrors.NewInternalServerError("backup dir unavailable"))
			return
		}
		archivePath = filepath.Join(dir, id+".tar.gz")
		if _, err := os.Stat(archivePath); err != nil {
			_ = c.Error(apperrors.NewBadRequestError("snapshot not found: " + id))
			return
		}
	} else {
		var tooLarge *http.MaxBytesError
		if stderrors.As(ferr, &tooLarge) {
			_ = c.Error(apperrors.NewBadRequestError(
				fmt.Sprintf("backup archive cannot exceed %d MB", secutils.GetMaxBackupArchiveSizeMB())))
			return
		}
		_ = c.Error(apperrors.NewBadRequestError("provide either a snapshot_id or an uploaded archive file"))
		return
	}

	summary, err := h.restoreArchive(ctx, archivePath, tmpDir)
	if err != nil {
		logger.Errorf(ctx, "[backup] restore failed: %v", err)
		_ = c.Error(apperrors.NewInternalServerError("restore failed: " + userFacingRestoreError(err)))
		return
	}
	h.emitBackupAudit(ctx, types.AuditActionSystemBackupRestored, summary.PreRestoreSnapID, map[string]any{
		"source_version": summary.SourceVersion,
		"files_restored": summary.FilesRestored,
	})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": summary})
}

func userFacingRestoreError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "archive unreadable"),
		strings.Contains(msg, "not a WeKnora backup"),
		strings.Contains(msg, "manifest"),
		strings.Contains(msg, "schema version"),
		strings.Contains(msg, "v1 restore supports"),
		strings.Contains(msg, "produced by a newer"),
		strings.Contains(msg, "missing the database dump"),
		strings.Contains(msg, "pre-restore snapshot failed"),
		strings.Contains(msg, "extraction size limit"),
		strings.Contains(msg, "unsafe entry"):
		return msg
	default:
		return "see server logs for details"
	}
}

// ---------------------------------------------------------------------------
// Export pipeline
// ---------------------------------------------------------------------------

func (h *BackupHandler) buildExport(ctx context.Context) (string, func(), error) {
	bundle, _, cleanup, err := h.buildExportWithManifest(ctx)
	return bundle, cleanup, err
}

func (h *BackupHandler) buildExportWithManifest(ctx context.Context) (string, *backupManifest, func(), error) {
	tmpDir, err := os.MkdirTemp("", "weknora-export-")
	if err != nil {
		return "", nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	work := filepath.Join(tmpDir, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		cleanup()
		return "", nil, nil, err
	}

	driver := h.dbDriver()
	includes := backupIncludes{Database: true}
	members := []string{backupManifestFile}

	switch driver {
	case "postgres":
		if err := h.dumpPostgres(ctx, filepath.Join(work, backupDBPostgres)); err != nil {
			cleanup()
			return "", nil, nil, err
		}
		members = append(members, backupDBPostgres)
	case "sqlite":
		out := filepath.Join(work, backupDBSQLite)
		if err := h.db.WithContext(ctx).Exec("VACUUM INTO ?", out).Error; err != nil {
			cleanup()
			return "", nil, nil, fmt.Errorf("sqlite export failed: %w", err)
		}
		members = append(members, backupDBSQLite)
	default:
		cleanup()
		return "", nil, nil, fmt.Errorf("unsupported db driver for export: %q", driver)
	}

	backupRoot, _ := h.backupDir()
	if baseDir := h.localStorageBaseDir(); baseDir != "" {
		if fi, err := os.Stat(baseDir); err == nil && fi.IsDir() {
			filesTar := filepath.Join(work, backupFilesArchive)
			skip := func(abs, rel string, _ os.FileInfo) bool {
				if isReservedStorageRel(rel) {
					return true
				}
				return backupRoot != "" && pathIsUnder(abs, backupRoot)
			}
			if err := tarDirTo(filesTar, baseDir, skip); err != nil {
				cleanup()
				return "", nil, nil, fmt.Errorf("archiving uploaded files failed: %w", err)
			}
			includes.Files = true
			members = append(members, backupFilesArchive)
		} else {
			logger.Warnf(ctx, "[backup] local storage dir %q missing; files omitted", baseDir)
		}
	}

	manifest := &backupManifest{
		SchemaVersion:  backupSchemaVersion,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		WeKnoraVersion: Version,
		Edition:        Edition,
		DBDriver:       driver,
		StorageType:    h.storageType(),
		FilesBaseDir:   h.localStorageBaseDir(),
		Includes:       includes,
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		cleanup()
		return "", nil, nil, err
	}
	if err := os.WriteFile(filepath.Join(work, backupManifestFile), manifestBytes, backupFilePerm); err != nil {
		cleanup()
		return "", nil, nil, err
	}

	bundle := filepath.Join(tmpDir, "bundle.tar.gz")
	if err := tarFilesTo(bundle, work, members); err != nil {
		cleanup()
		return "", nil, nil, err
	}
	return bundle, manifest, cleanup, nil
}

func (h *BackupHandler) dumpPostgres(ctx context.Context, out string) error {
	if _, err := exec.LookPath("pg_dump"); err != nil {
		return fmt.Errorf("pg_dump not found in PATH; install postgresql-client-17")
	}
	host, port, user, password, dbname := pgConnFromEnv()
	args := []string{
		"--host", host, "--port", port, "--username", user, "--dbname", dbname,
		"--format=plain", "--compress=5", "--no-owner", "--no-privileges",
		"--file", out,
	}
	cmd := exec.CommandContext(ctx, "pg_dump", args...)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+password, "PGSSLMODE=disable")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_dump failed: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Restore pipeline
// ---------------------------------------------------------------------------

type restoreSummary struct {
	RestoredAt       string `json:"restored_at"`
	SourceVersion    string `json:"source_version"`
	SourceCreatedAt  string `json:"source_created_at"`
	FilesRestored    bool   `json:"files_restored"`
	PreRestoreSnapID string `json:"pre_restore_snapshot"`
	RestartRequired  bool   `json:"restart_required"`
	Note             string `json:"note"`
}

func (h *BackupHandler) restoreArchive(ctx context.Context, archivePath, tmpDir string) (*restoreSummary, error) {
	staging := filepath.Join(tmpDir, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return nil, err
	}
	limit := secutils.GetMaxBackupExtractBytes()

	if err := untarGzFile(archivePath, staging, untarOptions{maxBytes: limit}); err != nil {
		return nil, fmt.Errorf("archive unreadable: %w", err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(staging, backupManifestFile))
	if err != nil {
		return nil, fmt.Errorf("archive is not a WeKnora backup (missing %s)", backupManifestFile)
	}
	var manifest backupManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("manifest corrupted: %w", err)
	}
	if manifest.SchemaVersion > backupSchemaVersion {
		return nil, fmt.Errorf(
			"archive schema version %d is newer than this instance supports (%d); upgrade WeKnora first",
			manifest.SchemaVersion, backupSchemaVersion)
	}
	if manifest.DBDriver != "postgres" {
		return nil, fmt.Errorf("v1 restore supports postgres archives only (archive driver: %q); "+
			"for sqlite, stop the instance and replace the db file manually", manifest.DBDriver)
	}
	if !versionAtLeast(Version, manifest.WeKnoraVersion) {
		return nil, fmt.Errorf("archive was produced by a newer WeKnora (%s > %s); upgrade this instance first",
			manifest.WeKnoraVersion, Version)
	}
	dumpGz := filepath.Join(staging, backupDBPostgres)
	if _, err := os.Stat(dumpGz); err != nil {
		return nil, fmt.Errorf("archive is missing the database dump")
	}

	// Stage uploaded files onto the live volume *before* dropping the database
	// so a bad archive cannot leave us with a replaced DB and unrestored files.
	var filesStaging string
	filesRestored := false
	if manifest.Includes.Files {
		filesArchive := filepath.Join(staging, backupFilesArchive)
		if _, err := os.Stat(filesArchive); err != nil {
			return nil, fmt.Errorf("archive is missing uploaded files")
		}
		base := h.filesVolumeDir()
		if err := os.MkdirAll(base, 0o755); err != nil {
			return nil, fmt.Errorf("storage dir unavailable: %w", err)
		}
		filesStaging = filepath.Join(base, backupRestoreNewDir)
		_ = os.RemoveAll(filesStaging)
		if err := os.MkdirAll(filesStaging, backupDirPerm); err != nil {
			return nil, fmt.Errorf("storage dir unavailable: %w", err)
		}
		if err := untarFile(filesArchive, filesStaging, untarOptions{maxBytes: limit, skipReserved: true}); err != nil {
			_ = os.RemoveAll(filesStaging)
			return nil, fmt.Errorf("uploaded files restore failed: %w", err)
		}
	}

	// Rollback point: must succeed before replacing the live database.
	bundle, m, cleanup, err := h.buildExportWithManifest(ctx)
	if err != nil {
		_ = os.RemoveAll(filesStaging)
		return nil, fmt.Errorf("pre-restore snapshot failed (restore aborted): %w", err)
	}
	dir, derr := h.backupDir()
	if derr != nil {
		cleanup()
		_ = os.RemoveAll(filesStaging)
		return nil, fmt.Errorf("pre-restore snapshot failed (restore aborted): %w", derr)
	}
	preID, prePath, err := uniqueSnapshotPath(dir, "pre-restore-"+time.Now().UTC().Format("20060102-150405"))
	if err != nil {
		cleanup()
		_ = os.RemoveAll(filesStaging)
		return nil, fmt.Errorf("pre-restore snapshot failed (restore aborted): %w", err)
	}
	if err := moveFile(bundle, prePath); err != nil {
		cleanup()
		_ = os.RemoveAll(filesStaging)
		return nil, fmt.Errorf("pre-restore snapshot failed (restore aborted): %w", err)
	}
	cleanup()
	if st, serr := os.Stat(prePath); serr == nil {
		if werr := writeSnapshotMeta(dir, snapshotMeta{
			ID: preID, Note: "auto: pre-restore rollback point",
			CreatedAt: m.CreatedAt, SizeBytes: st.Size(), Manifest: *m,
		}); werr != nil {
			_ = os.Remove(prePath)
			_ = os.RemoveAll(filesStaging)
			return nil, fmt.Errorf("pre-restore snapshot failed (restore aborted): %w", werr)
		}
	}

	dumpSQL := filepath.Join(tmpDir, "db.sql")
	if err := gunzipFile(dumpGz, dumpSQL, limit); err != nil {
		_ = os.RemoveAll(filesStaging)
		return nil, fmt.Errorf("database dump decompression failed: %w", err)
	}

	// Point of no return: drop/import must not follow the HTTP request
	// cancellation (browser close / proxy timeout would otherwise kill psql
	// after DROP DATABASE and leave an empty cluster).
	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), backupRestoreTimeout)
	defer cancel()

	if err := h.replacePostgresDB(restoreCtx, dumpSQL); err != nil {
		_ = os.RemoveAll(filesStaging)
		return nil, err
	}

	if filesStaging != "" {
		if err := promoteRestoredFiles(h.filesVolumeDir(), filesStaging); err != nil {
			return nil, fmt.Errorf("uploaded files restore failed after database replace: %w", err)
		}
		filesRestored = true
	}

	h.flushRedis(restoreCtx)

	return &restoreSummary{
		RestoredAt:       time.Now().UTC().Format(time.RFC3339),
		SourceVersion:    manifest.WeKnoraVersion,
		SourceCreatedAt:  manifest.CreatedAt,
		FilesRestored:    filesRestored,
		PreRestoreSnapID: preID,
		RestartRequired:  true,
		Note: "database replaced and queues flushed; restart the WeKnora process/container now. " +
			"If this archive came from another instance and contains encrypted credentials " +
			"(model API keys, MCP tokens), SYSTEM_AES_KEY of the source instance must be set in this " +
			"instance's environment for those fields to decrypt. External vector stores, graph DBs, " +
			"and remote object storage are not included in this archive.",
	}, nil
}

func (h *BackupHandler) replacePostgresDB(ctx context.Context, dumpSQL string) error {
	if _, err := exec.LookPath("psql"); err != nil {
		return fmt.Errorf("psql not found in PATH; install postgresql-client-17")
	}
	host, port, user, password, dbname := pgConnFromEnv()

	admin, err := gorm.Open(postgres.Open(fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=postgres sslmode=disable", host, port, user, password,
	)), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("admin connection failed: %w", err)
	}
	sqlDB, err := admin.DB()
	if err != nil {
		return fmt.Errorf("admin connection failed: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	if err := admin.WithContext(ctx).Exec(
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = ? AND pid <> pg_backend_pid()`, dbname,
	).Error; err != nil {
		return fmt.Errorf("terminating db sessions failed: %w", err)
	}
	dropSQL := `DROP DATABASE IF EXISTS "` + sqlQuoteIdent(dbname) + `"`
	if err := admin.WithContext(ctx).Exec(dropSQL).Error; err != nil {
		return fmt.Errorf("drop database failed: %w", err)
	}
	createSQL := `CREATE DATABASE "` + sqlQuoteIdent(dbname) + `"`
	if err := admin.WithContext(ctx).Exec(createSQL).Error; err != nil {
		return fmt.Errorf("create database failed: %w", err)
	}

	cmd := exec.CommandContext(ctx, "psql",
		"--host", host, "--port", port, "--username", user, "--dbname", dbname,
		"--single-transaction", "--set", "ON_ERROR_STOP=1", "--file", dumpSQL,
	)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+password, "PGSSLMODE=disable")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("psql import failed: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (h *BackupHandler) flushRedis(ctx context.Context) {
	if h.redis == nil {
		return
	}
	fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := h.redis.FlushDB(fctx).Err(); err != nil {
		logger.Warnf(ctx, "[backup] redis flush failed (restart the instance to reset queues): %v", err)
	} else {
		logger.Infof(ctx, "[backup] redis db flushed")
	}
}

func (h *BackupHandler) emitBackupAudit(
	ctx context.Context,
	action types.AuditAction,
	targetID string,
	details map[string]any,
) {
	if h.audit == nil {
		return
	}
	actorID, _ := types.UserIDFromContext(ctx)
	var detailsJSON types.JSON
	if details != nil {
		if b, err := json.Marshal(details); err == nil {
			detailsJSON = types.JSON(b)
		}
	}
	_ = h.audit.Log(ctx, &types.AuditLog{
		TenantID:    0,
		ActorUserID: actorID,
		ActorRole:   systemAuditActorRole(ctx),
		Action:      action,
		TargetType:  "backup",
		TargetID:    targetID,
		Outcome:     types.AuditOutcomeSuccess,
		Details:     detailsJSON,
	})
}

// ---------------------------------------------------------------------------
// Environment helpers
// ---------------------------------------------------------------------------

func (h *BackupHandler) dbDriver() string {
	d := strings.ToLower(strings.TrimSpace(os.Getenv("DB_DRIVER")))
	if d == "" {
		return "postgres"
	}
	return d
}

func (h *BackupHandler) storageType() string {
	s := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_TYPE")))
	if s == "" {
		return "local"
	}
	return s
}

func (h *BackupHandler) filesVolumeDir() string {
	b := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	if b == "" {
		b = "/data/files"
	}
	return b
}

func (h *BackupHandler) localStorageBaseDir() string {
	if h.storageType() != "local" {
		return ""
	}
	return h.filesVolumeDir()
}

func (h *BackupHandler) backupDir() (string, error) {
	if d := strings.TrimSpace(os.Getenv("BACKUP_DIR")); d != "" {
		if err := os.MkdirAll(d, backupDirPerm); err != nil {
			return "", err
		}
		return d, nil
	}
	d := filepath.Join(h.filesVolumeDir(), backupSnapshotsDirName)
	if err := os.MkdirAll(d, backupDirPerm); err != nil {
		return "", err
	}
	return d, nil
}

func pgConnFromEnv() (host, port, user, password, dbname string) {
	host = envOr("DB_HOST", "127.0.0.1")
	port = envOr("DB_PORT", "5432")
	user = envOr("DB_USER", "postgres")
	password = os.Getenv("DB_PASSWORD")
	dbname = envOr("DB_NAME", "WeKnora")
	return
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// ---------------------------------------------------------------------------
// Snapshot sidecar helpers
// ---------------------------------------------------------------------------

func snapshotMetaPath(dir, id string) string {
	return filepath.Join(dir, id+".meta.json")
}

func writeSnapshotMeta(dir string, meta snapshotMeta) error {
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(snapshotMetaPath(dir, meta.ID), b, backupFilePerm)
}

func readSnapshotMeta(dir, id string) (snapshotMeta, error) {
	var meta snapshotMeta
	b, err := os.ReadFile(snapshotMetaPath(dir, id))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		return meta, err
	}
	meta.ID = id
	return meta, nil
}

func uniqueSnapshotPath(dir, base string) (string, string, error) {
	id := base
	for n := 2; n < 10000; n++ {
		path := filepath.Join(dir, id+".tar.gz")
		_, err := os.Stat(path)
		if os.IsNotExist(err) {
			return id, path, nil
		}
		if err != nil {
			return "", "", err
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return "", "", fmt.Errorf("too many snapshot name collisions under %s", dir)
}

func validateSnapshotID(id string) error {
	if id == "" || id == "." || id == ".." || id != filepath.Base(id) || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid id")
	}
	return nil
}

func isReservedStorageRel(rel string) bool {
	rel = filepath.ToSlash(rel)
	first, _, _ := strings.Cut(rel, "/")
	switch first {
	case backupSnapshotsDirName, backupRestoreNewDir, backupRestoreOldDir:
		return true
	}
	return false
}

func pathIsUnder(child, parent string) bool {
	absChild, err := filepath.Abs(child)
	if err != nil {
		return false
	}
	absParent, err := filepath.Abs(parent)
	if err != nil {
		return false
	}
	if absChild == absParent {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(absChild, absParent+sep)
}

// ---------------------------------------------------------------------------
// Tar/gzip helpers (all extraction paths enforce traversal safety)
// ---------------------------------------------------------------------------

func tarDirTo(out, src string, skip func(abs, rel string, info os.FileInfo) bool) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	tw := tar.NewWriter(f)
	defer func() { _ = tw.Close() }()
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if skip != nil && skip(path, rel, info) {
			if info.IsDir() && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		hdr.Typeflag = tar.TypeReg
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		fh, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, fh)
		_ = fh.Close()
		return err
	})
}

func tarFilesTo(out, srcDir string, names []string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gw := gzip.NewWriter(f)
	defer func() { _ = gw.Close() }()
	tw := tar.NewWriter(gw)
	defer func() { _ = tw.Close() }()
	for _, name := range names {
		path := filepath.Join(srcDir, name)
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(name)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, src)
		_ = src.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

type untarOptions struct {
	maxBytes     int64
	skipReserved bool
}

func untarGzFile(archive, dest string, opts untarOptions) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gr.Close() }()
	return untarReader(gr, dest, opts)
}

func untarFile(archive, dest string, opts untarOptions) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return untarReader(f, dest, opts)
}

func untarReader(r io.Reader, dest string, opts untarOptions) error {
	remain := opts.maxBytes
	if remain <= 0 {
		remain = secutils.GetMaxBackupExtractBytes()
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if opts.skipReserved && isReservedStorageRel(hdr.Name) {
			continue
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return fmt.Errorf("unsafe entry %q: %w", hdr.Name, err)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if err := copyLimited(out, tr, &remain); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported entry type (%c) in archive: %q", hdr.Typeflag, hdr.Name)
		}
	}
}

func copyLimited(dst io.Writer, src io.Reader, remain *int64) error {
	if remain == nil {
		_, err := io.Copy(dst, src)
		return err
	}
	n, err := io.Copy(dst, io.LimitReader(src, *remain+1))
	*remain -= n
	if *remain < 0 {
		return errExtractLimit
	}
	return err
}

func safeJoin(base, name string) (string, error) {
	if strings.Contains(name, `\`) {
		return "", fmt.Errorf("backslash in entry name")
	}
	clean, err := filepath.Rel(".", filepath.FromSlash(name))
	if err != nil {
		return "", err
	}
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("path escapes archive root")
	}
	target := filepath.Join(base, clean)
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if absTarget != absBase && !strings.HasPrefix(absTarget, absBase+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes archive root")
	}
	return target, nil
}

// promoteRestoredFiles replaces dest's contents with staging, preserving
// reserved snapshot/restore directories. Staging is removed on success.
func promoteRestoredFiles(dest, staging string) error {
	old := filepath.Join(dest, backupRestoreOldDir)
	_ = os.RemoveAll(old)
	if err := os.MkdirAll(old, backupDirPerm); err != nil {
		return err
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if name == backupSnapshotsDirName || name == backupRestoreNewDir || name == backupRestoreOldDir {
			continue
		}
		if err := os.Rename(filepath.Join(dest, name), filepath.Join(old, name)); err != nil {
			_ = moveDirEntries(old, dest)
			return err
		}
	}
	staged, err := os.ReadDir(staging)
	if err != nil {
		_ = moveDirEntries(old, dest)
		return err
	}
	for _, e := range staged {
		if err := os.Rename(filepath.Join(staging, e.Name()), filepath.Join(dest, e.Name())); err != nil {
			return fmt.Errorf("promote restored files (live files kept in %s): %w", old, err)
		}
	}
	_ = os.RemoveAll(staging)
	_ = os.RemoveAll(old)
	return nil
}

func moveDirEntries(src, dest string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		_ = os.Rename(filepath.Join(src, e.Name()), filepath.Join(dest, e.Name()))
	}
	return nil
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		_ = os.Chmod(dst, backupFilePerm)
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, backupFilePerm)
	if err != nil {
		_ = in.Close()
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = in.Close()
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = in.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := in.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	srcStat, err := os.Stat(src)
	if err != nil {
		_ = os.Remove(dst)
		return err
	}
	dstStat, err := os.Stat(dst)
	if err != nil {
		_ = os.Remove(dst)
		return err
	}
	if srcStat.Size() != dstStat.Size() {
		_ = os.Remove(dst)
		return fmt.Errorf("snapshot copy size mismatch: %d != %d", srcStat.Size(), dstStat.Size())
	}
	return os.Remove(src)
}

func sqlQuoteIdent(name string) string {
	return strings.ReplaceAll(name, `"`, `""`)
}

func gunzipFile(in, out string, maxBytes int64) error {
	src, err := os.Open(in)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	gr, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	defer func() { _ = gr.Close() }()
	dst, err := os.Create(out)
	if err != nil {
		return err
	}
	remain := maxBytes
	if err := copyLimited(dst, gr, &remain); err != nil {
		_ = dst.Close()
		_ = os.Remove(out)
		return err
	}
	return dst.Close()
}

// versionAtLeast reports whether v >= min. Leading X.Y.Z is parsed even when
// followed by a prerelease/git suffix. Completely unparseable values (dev
// builds, "unknown") compare as equal so local builds are not locked out of
// restoring their own archives; a parseable archive vs parseable instance
// still enforces the numeric gate.
func versionAtLeast(v, min string) bool {
	vv, vok := parseVersion(v)
	mv, mok := parseVersion(min)
	if !vok || !mok {
		return true
	}
	for i := range vv {
		if vv[i] != mv[i] {
			return vv[i] > mv[i]
		}
	}
	return true
}

func parseVersion(s string) ([3]int, bool) {
	s = strings.TrimSpace(s)
	m := versionCoreRe.FindStringSubmatch(s)
	var out [3]int
	if m == nil {
		return out, false
	}
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
