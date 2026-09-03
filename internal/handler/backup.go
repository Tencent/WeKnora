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
// kept in the backup dir as the rollback point. After a restore the Redis queues
// are flushed (stale tasks would reference replaced-away IDs) and an application
// restart is recommended.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
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
	cfg *config.Config
	db  *gorm.DB
}

// NewBackupHandler creates the backup handler.
func NewBackupHandler(cfg *config.Config, db *gorm.DB) *BackupHandler {
	return &BackupHandler{cfg: cfg, db: db}
}

// restoreInFlight guards against overlapping restore attempts; exports and
// snapshots are allowed to run concurrently with each other.
var restoreInFlight atomic.Bool

// ---------------------------------------------------------------------------
// Endpoints
// ---------------------------------------------------------------------------

// Export streams a freshly built backup archive to the client.
func (h *BackupHandler) Export(c *gin.Context) {
	ctx := c.Request.Context()
	bundle, cleanup, err := h.buildExport(ctx)
	if err != nil {
		c.Error(apperrors.NewInternalServerError("backup export failed").WithDetails(err.Error()))
		return
	}
	defer cleanup()
	name := "weknora-backup-" + time.Now().UTC().Format("20060102-150405") + ".tar.gz"
	logger.Infof(ctx, "[backup] export served: %s", name)
	c.FileAttachment(bundle, name)
}

// SnapshotRequest is the body of POST /backups.
type SnapshotRequest struct {
	Note string `json:"note"`
}

// CreateSnapshot builds an archive and stores it in the backup dir with a
// sidecar metadata file.
func (h *BackupHandler) CreateSnapshot(c *gin.Context) {
	ctx := c.Request.Context()
	var req SnapshotRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		c.Error(apperrors.NewBadRequestError("invalid request body"))
		return
	}
	dir, err := h.backupDir()
	if err != nil {
		c.Error(apperrors.NewInternalServerError("backup dir unavailable").WithDetails(err.Error()))
		return
	}
	bundle, manifest, cleanup, err := h.buildExportWithManifest(ctx)
	if err != nil {
		c.Error(apperrors.NewInternalServerError("backup snapshot failed").WithDetails(err.Error()))
		return
	}
	defer cleanup()

	id, final := uniqueSnapshotPath(dir, "weknora-snapshot-"+time.Now().UTC().Format("20060102-150405"))
	if err := moveFile(bundle, final); err != nil {
		c.Error(apperrors.NewInternalServerError("backup snapshot failed").WithDetails(err.Error()))
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
		logger.Warnf(ctx, "[backup] snapshot sidecar write failed: %v", err)
	}
	logger.Infof(ctx, "[backup] snapshot created: %s (%d bytes)", id, meta.SizeBytes)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": meta})
}

// ListSnapshots returns stored snapshots, newest first.
func (h *BackupHandler) ListSnapshots(c *gin.Context) {
	dir, err := h.backupDir()
	if err != nil {
		c.Error(apperrors.NewInternalServerError("backup dir unavailable").WithDetails(err.Error()))
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		c.Error(apperrors.NewInternalServerError("backup dir unreadable").WithDetails(err.Error()))
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

// DownloadSnapshot streams a stored snapshot.
func (h *BackupHandler) DownloadSnapshot(c *gin.Context) {
	id := c.Param("id")
	if err := validateSnapshotID(id); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid snapshot id"))
		return
	}
	dir, err := h.backupDir()
	if err != nil {
		c.Error(apperrors.NewInternalServerError("backup dir unavailable").WithDetails(err.Error()))
		return
	}
	path := filepath.Join(dir, id+".tar.gz")
	if _, err := os.Stat(path); err != nil {
		c.Error(apperrors.NewBadRequestError("snapshot not found: " + id))
		return
	}
	c.FileAttachment(path, id+".tar.gz")
}

// DeleteSnapshot removes a snapshot and its sidecar.
func (h *BackupHandler) DeleteSnapshot(c *gin.Context) {
	id := c.Param("id")
	if err := validateSnapshotID(id); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid snapshot id"))
		return
	}
	dir, err := h.backupDir()
	if err != nil {
		c.Error(apperrors.NewInternalServerError("backup dir unavailable").WithDetails(err.Error()))
		return
	}
	if err := os.Remove(filepath.Join(dir, id+".tar.gz")); err != nil {
		c.Error(apperrors.NewBadRequestError("snapshot not found: " + id))
		return
	}
	_ = os.Remove(filepath.Join(dir, id+".meta.json"))
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RestoreRequest is the multipart/JSON body of POST /backups/restore.
// Either SnapshotID (a stored snapshot) or an uploaded archive is required;
// Confirm must be true — restore replaces the whole database.
type RestoreRequest struct {
	SnapshotID string `json:"snapshot_id" form:"snapshot_id"`
	Confirm    bool   `json:"confirm" form:"confirm"`
}

// Restore applies an archive to this instance (full database replace).
func (h *BackupHandler) Restore(c *gin.Context) {
	ctx := c.Request.Context()

	var req RestoreRequest
	_ = c.ShouldBind(&req)
	if !req.Confirm {
		c.Error(apperrors.NewBadRequestError("restore requires confirm=true (it replaces the entire database)"))
		return
	}
	if restoreInFlight.Swap(true) {
		c.Error(apperrors.NewInternalServerError("another restore is already in progress"))
		return
	}
	defer restoreInFlight.Store(false)

	// Resolve the archive: multipart upload or stored snapshot id.
	tmpDir, err := os.MkdirTemp("", "weknora-restore-")
	if err != nil {
		c.Error(apperrors.NewInternalServerError("restore failed").WithDetails(err.Error()))
		return
	}
	defer os.RemoveAll(tmpDir)

	archivePath := ""
	if f, ferr := c.FormFile(backupMultipartFile); ferr == nil {
		archivePath = filepath.Join(tmpDir, "upload.tar.gz")
		if err := c.SaveUploadedFile(f, archivePath); err != nil {
			c.Error(apperrors.NewBadRequestError("failed to read uploaded archive").WithDetails(err.Error()))
			return
		}
	} else if id := strings.TrimSpace(req.SnapshotID); id != "" {
		if err := validateSnapshotID(id); err != nil {
			c.Error(apperrors.NewBadRequestError("invalid snapshot id"))
			return
		}
		dir, err := h.backupDir()
		if err != nil {
			c.Error(apperrors.NewInternalServerError("backup dir unavailable").WithDetails(err.Error()))
			return
		}
		archivePath = filepath.Join(dir, id+".tar.gz")
		if _, err := os.Stat(archivePath); err != nil {
			c.Error(apperrors.NewBadRequestError("snapshot not found: " + id))
			return
		}
	} else {
		c.Error(apperrors.NewBadRequestError("provide either a snapshot_id or an uploaded archive file"))
		return
	}

	summary, err := h.restoreArchive(ctx, archivePath, tmpDir)
	if err != nil {
		logger.Errorf(ctx, "[backup] restore failed: %v", err)
		c.Error(apperrors.NewInternalServerError("restore failed: " + err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": summary})
}

// ---------------------------------------------------------------------------
// Export pipeline
// ---------------------------------------------------------------------------

// buildExport builds a fresh archive in a temp dir; the returned cleanup
// removes all intermediate state.
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

	if baseDir := h.localStorageBaseDir(); baseDir != "" {
		if fi, err := os.Stat(baseDir); err == nil && fi.IsDir() {
			if err := tarDirTo(filepath.Join(work, backupFilesArchive), baseDir); err != nil {
				cleanup()
				return "", nil, nil, fmt.Errorf("archiving uploaded files failed: %w", err)
			}
			includes.Files = true
			members = append(members, backupFilesArchive)
		} else {
			logger.Warnf(ctx,
				"[backup] local storage dir %q missing or not a directory; archive will not contain uploaded files", baseDir)
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
	if err := os.WriteFile(filepath.Join(work, backupManifestFile), manifestBytes, 0o644); err != nil {
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
		return fmt.Errorf("pg_dump not found in PATH; install postgresql-client on the server")
	}
	host, port, user, password, dbname := pgConnFromEnv()
	args := []string{
		"--host", host, "--port", port, "--username", user, "--dbname", dbname,
		"--format=plain", "--compress=5", "--no-owner", "--no-privileges",
		"--file", out,
	}
	cmd := exec.CommandContext(ctx, "pg_dump", args...)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+password)
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

// restoreSummary reports what a completed restore did, including the
// auto-created rollback snapshot id.
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

	// 1) Validate before touching anything.
	if err := untarGzFile(archivePath, staging); err != nil {
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
		return nil, fmt.Errorf("archive schema version %d is newer than this instance supports (%d); upgrade WeKnora first",
			manifest.SchemaVersion, backupSchemaVersion)
	}
	if manifest.DBDriver != "postgres" {
		return nil, fmt.Errorf("v1 restore supports postgres archives only (archive driver: %q); "+
			"for sqlite, stop the instance and replace the db file manually", manifest.DBDriver)
	}
	if !versionAtLeast(Version, manifest.WeKnoraVersion) {
		logger.Warnf(ctx, "[backup] archive was produced by %s, newer than this instance (%s); "+
			"restore may reference unknown schema — refusing", manifest.WeKnoraVersion, Version)
		return nil, fmt.Errorf("archive was produced by a newer WeKnora (%s > %s); upgrade this instance first",
			manifest.WeKnoraVersion, Version)
	}
	dumpGz := filepath.Join(staging, backupDBPostgres)
	if _, err := os.Stat(dumpGz); err != nil {
		return nil, fmt.Errorf("archive is missing the database dump")
	}

	// 2) Rollback point: snapshot current state before replacing it.
	preID := ""
	if bundle, m, cleanup, err := h.buildExportWithManifest(ctx); err == nil {
		dir, derr := h.backupDir()
		if derr == nil {
			preID, prePath := uniqueSnapshotPath(dir, "pre-restore-"+time.Now().UTC().Format("20060102-150405"))
			if rerr := moveFile(bundle, prePath); rerr == nil {
				if st, serr := os.Stat(prePath); serr == nil {
					_ = writeSnapshotMeta(dir, snapshotMeta{
						ID: preID, Note: "auto: pre-restore rollback point",
						CreatedAt: m.CreatedAt, SizeBytes: st.Size(), Manifest: *m,
					})
				}
			} else {
				preID = ""
			}
		} else {
			preID = ""
		}
		cleanup()
	} else {
		logger.Warnf(ctx, "[backup] pre-restore snapshot failed (continuing): %v", err)
	}

	// 3) Decompress the dump.
	dumpSQL := filepath.Join(tmpDir, "db.sql")
	if err := gunzipFile(dumpGz, dumpSQL); err != nil {
		return nil, fmt.Errorf("database dump decompression failed: %w", err)
	}

	// 4) Replace the database in one shot.
	if err := h.replacePostgresDB(ctx, dumpSQL); err != nil {
		return nil, err
	}

	// 5) Restore uploaded files (paths are relative to the storage base dir).
	filesRestored := false
	filesArchive := filepath.Join(staging, backupFilesArchive)
	if manifest.Includes.Files {
		if _, err := os.Stat(filesArchive); err == nil {
			base := h.localStorageBaseDir()
			if base == "" {
				base = "/data/files"
			}
			if err := os.MkdirAll(base, 0o755); err != nil {
				return nil, fmt.Errorf("storage dir unavailable: %w", err)
			}
			if err := untarFile(filesArchive, base); err != nil {
				return nil, fmt.Errorf("uploaded files restore failed: %w", err)
			}
			filesRestored = true
		}
	}

	// 6) Drop stale queued tasks — they reference replaced-away IDs.
	h.flushRedis(ctx)

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
			"instance's environment for those fields to decrypt.",
	}, nil
}

func (h *BackupHandler) replacePostgresDB(ctx context.Context, dumpSQL string) error {
	if _, err := exec.LookPath("psql"); err != nil {
		return fmt.Errorf("psql not found in PATH; install postgresql-client on the server")
	}
	host, port, user, password, dbname := pgConnFromEnv()

	admin, err := gorm.Open(postgres.Open(fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=postgres sslmode=prefer", host, port, user, password,
	)), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("admin connection failed: %w", err)
	}
	sqlDB, err := admin.DB()
	if err != nil {
		return fmt.Errorf("admin connection failed: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	// Drop in-flight connections to the target database, then replace it.
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

	// Single transaction: the import is atomic — concurrent sessions either see
	// an empty database or the fully restored one, never a half-imported state.
	cmd := exec.CommandContext(ctx, "psql",
		"--host", host, "--port", port, "--username", user, "--dbname", dbname,
		"--single-transaction", "--set", "ON_ERROR_STOP=1", "--file", dumpSQL,
	)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+password)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("psql import failed: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (h *BackupHandler) flushRedis(ctx context.Context) {
	addr := strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	if addr == "" {
		addr = "redis:6379"
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("REDIS_USE_TLS")), "true") {
		logger.Warnf(ctx, "[backup] redis uses TLS; skipping queue flush — restart the instance to reset queues")
		return
	}
	db := 0
	if v := strings.TrimSpace(os.Getenv("REDIS_DB")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			db = n
		}
	}
	c := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("REDIS_PASSWORD"), DB: db})
	defer func() { _ = c.Close() }()
	fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.FlushDB(fctx).Err(); err != nil {
		logger.Warnf(ctx, "[backup] redis flush failed (restart the instance to reset queues): %v", err)
	} else {
		logger.Infof(ctx, "[backup] redis db flushed")
	}
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

// localStorageBaseDir returns the local-storage dir when local storage is
// actually in use, "" otherwise (remote object stores are out of scope v1).
func (h *BackupHandler) localStorageBaseDir() string {
	if h.storageType() != "local" {
		return ""
	}
	b := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	if b == "" {
		b = "/data/files"
	}
	return b
}

// backupDir prefers an explicit BACKUP_DIR; otherwise it lands next to the
// local storage volume (e.g. /data/backups for /data/files) so snapshots
// survive container replacement on volume-backed deployments.
func (h *BackupHandler) backupDir() (string, error) {
	if d := strings.TrimSpace(os.Getenv("BACKUP_DIR")); d != "" {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", err
		}
		return d, nil
	}
	base := h.localStorageBaseDir()
	if base == "" {
		base = "/data/files"
	}
	d := filepath.Join(filepath.Dir(base), "backups")
	if err := os.MkdirAll(d, 0o755); err != nil {
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
	return os.WriteFile(snapshotMetaPath(dir, meta.ID), b, 0o644)
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

// uniqueSnapshotPath returns a (id, path) pair under dir that does not
// collide with an existing snapshot — two snapshots created within the same
// second would otherwise silently overwrite each other.
func uniqueSnapshotPath(dir, base string) (string, string) {
	id := base
	for n := 2; ; n++ {
		path := filepath.Join(dir, id+".tar.gz")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return id, path
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

// validateSnapshotID rejects anything that could escape the backup dir.
func validateSnapshotID(id string) error {
	if id == "" || id == "." || id == ".." || id != filepath.Base(id) || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid id")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Tar/gzip helpers (all extraction paths enforce traversal safety)
// ---------------------------------------------------------------------------

// tarDirTo writes every regular file under src into a tar archive at out,
// with names relative to src.
func tarDirTo(out, src string) error {
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
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
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

// tarFilesTo packs the given files ( residing in srcDir) into a tar.gz at out.
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

// untarGzFile extracts a tar.gz archive; untarFile extracts a plain tar.
// Both reject absolute paths, traversal outside dest, and non-regular entries.
func untarGzFile(archive, dest string) error {
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
	return untarReader(gr, dest)
}

func untarFile(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return untarReader(f, dest)
}

func untarReader(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
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
			if _, err := io.Copy(out, tr); err != nil {
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

// safeJoin resolves name under base and refuses traversal outside it.
// Tar entry names must use forward slashes; a backslash is either a
// portability bug or a traversal attempt targeting Windows hosts.
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

// moveFile renames src to dst, falling back to a copy when the two live on
// different filesystems (e.g. /tmp overlay vs the /data volume — os.Rename
// fails there with EXDEV). The copied size is verified before the source is
// removed so a half-written snapshot never replaces a good one.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		_ = in.Close()
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = in.Close()
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		_ = in.Close()
		return err
	}
	if err := in.Close(); err != nil {
		return err
	}
	srcStat, err := os.Stat(src)
	if err != nil {
		return err
	}
	dstStat, err := os.Stat(dst)
	if err != nil {
		return err
	}
	if srcStat.Size() != dstStat.Size() {
		return fmt.Errorf("snapshot copy size mismatch: %d != %d", srcStat.Size(), dstStat.Size())
	}
	return os.Remove(src)
}

// sqlQuoteIdent escapes a double-quoted SQL identifier (doubling embedded
// quotes), for the drop/create statements that cannot use bind parameters.
func sqlQuoteIdent(name string) string {
	return strings.ReplaceAll(name, `"`, `""`)
}

func gunzipFile(in, out string) error {
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
	if _, err := io.Copy(dst, gr); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}

// ---------------------------------------------------------------------------
// Version comparison
// ---------------------------------------------------------------------------

// versionAtLeast reports whether v >= min. Unparseable versions (dev builds,
// "unknown") compare as equal so local builds are not locked out; explicit
// downgrade attempts still pass the numeric check when both parse.
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
	s = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(s)), "v")
	var out [3]int
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
