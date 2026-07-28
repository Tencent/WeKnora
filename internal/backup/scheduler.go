package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/robfig/cron/v3"
	"github.com/shirou/gopsutil/v3/disk"
)

const (
	defaultBackupRetentionDays = 7
	defaultBackupMinFreeGB     = 5
)

var backupScheduleParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

// ScheduleConfig is intentionally environment-owned. A schedule is active
// only when manual backup support is also explicitly enabled.
type ScheduleConfig struct {
	Enabled       bool
	Expression    string
	RetentionDays int
	MinFreeBytes  uint64
	LocalDir      string
}

// ScheduleStatus contains safe state for metrics, the protected operations
// endpoint, and alert evaluation. It intentionally excludes paths and errors.
type ScheduleStatus struct {
	Enabled                bool      `json:"enabled"`
	ConfigurationError     bool      `json:"configuration_error"`
	Schedule               string    `json:"schedule,omitempty"`
	RetentionDays          int       `json:"retention_days"`
	MinFreeGB              int       `json:"min_free_gb"`
	LastStartedAt          time.Time `json:"last_started_at,omitempty"`
	LastSuccessAt          time.Time `json:"last_success_at,omitempty"`
	LastFailureAt          time.Time `json:"last_failure_at,omitempty"`
	LastFailureKind        ErrorKind `json:"last_failure_kind,omitempty"`
	LastRetentionAt        time.Time `json:"last_retention_at,omitempty"`
	LastRetentionFailureAt time.Time `json:"last_retention_failure_at,omitempty"`
	LastRetentionDeleted   int       `json:"last_retention_deleted"`
	LastSkippedAt          time.Time `json:"last_skipped_at,omitempty"`
}

// ScheduledRun is delivered after a completed scheduled backup attempt. A
// skipped run is deliberately excluded so a held advisory lock cannot flood
// the audit log.
type ScheduledRun struct {
	Result           Result
	Err              error
	RetentionDeleted int
	RetentionFailed  bool
}

type scheduledBackupCreator interface {
	CreateScheduled(context.Context) (Result, error)
	isMySQL() bool
}

// MySQLScheduler owns one process-local cron runner. The backup manager's
// MySQL advisory lock remains the cross-process concurrency guard.
type MySQLScheduler struct {
	manager   scheduledBackupCreator
	config    ScheduleConfig
	configErr error
	cron      *cron.Cron
	now       func() time.Time
	freeBytes func(string) (uint64, error)
	onRun     func(ScheduledRun)

	mu      sync.Mutex
	started bool
	status  ScheduleStatus
}

// NewMySQLScheduler constructs a dormant scheduler. Call Start to register
// the cron entry. Invalid optional configuration is retained as safe status
// and must not prevent the rest of the application from starting.
func NewMySQLScheduler(manager *MySQLManager) *MySQLScheduler {
	config, err := loadScheduleConfigFromEnv()
	if err == nil && config.Enabled && manager != nil && manager.configErr != nil {
		err = errors.New("backup configuration invalid")
	}
	return newMySQLScheduler(manager, config, err)
}

func newMySQLScheduler(manager scheduledBackupCreator, config ScheduleConfig, configErr error) *MySQLScheduler {
	scheduler := &MySQLScheduler{
		manager:   manager,
		config:    config,
		configErr: configErr,
		cron: cron.New(
			cron.WithParser(backupScheduleParser),
			cron.WithChain(cron.Recover(cron.DefaultLogger), cron.SkipIfStillRunning(cron.DefaultLogger)),
		),
		now: time.Now,
		freeBytes: func(directory string) (uint64, error) {
			usage, usageErr := disk.Usage(directory)
			if usageErr != nil {
				return 0, usageErr
			}
			return usage.Free, nil
		},
	}
	scheduler.status = ScheduleStatus{
		ConfigurationError: configErr != nil,
		Schedule:           config.Expression,
		RetentionDays:      config.RetentionDays,
		MinFreeGB:          int(config.MinFreeBytes / (1024 * 1024 * 1024)),
	}
	return scheduler
}

// Start registers the optional five-field Cron expression. PostgreSQL and
// SQLite never start this scheduler, even if backup environment variables are
// accidentally present.
func (s *MySQLScheduler) Start() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || !s.config.Enabled || (s.manager != nil && !s.manager.isMySQL()) {
		return nil
	}
	if s.configErr != nil || s.manager == nil {
		s.status.ConfigurationError = true
		return errors.New("scheduled MySQL backup configuration invalid")
	}
	if _, err := s.cron.AddFunc(s.config.Expression, s.run); err != nil {
		s.status.ConfigurationError = true
		return errors.New("scheduled MySQL backup schedule invalid")
	}
	s.started = true
	s.status.Enabled = true
	s.cron.Start()
	logger.Infof(context.Background(), "[backup] scheduled MySQL backup enabled: retention_days=%d", s.config.RetentionDays)
	return nil
}

// Stop waits for an in-flight scheduled job. It is safe during startup paths
// where the scheduler was never enabled.
func (s *MySQLScheduler) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	s.started = false
	s.status.Enabled = false
	s.mu.Unlock()
	<-s.cron.Stop().Done()
}

// Status returns a snapshot with no credentials, paths, or command output.
func (s *MySQLScheduler) Status() ScheduleStatus {
	if s == nil {
		return ScheduleStatus{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// SetRunHandler installs an optional observer for audit logging. It receives
// terminal success/failure only, never a lock-contention skip.
func (s *MySQLScheduler) SetRunHandler(handler func(ScheduledRun)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onRun = handler
}

func (s *MySQLScheduler) run() {
	now := s.now().UTC()
	s.mu.Lock()
	s.status.LastStartedAt = now
	s.mu.Unlock()
	if s.manager == nil {
		s.finish(ScheduledRun{Err: &Error{Kind: ErrorConfiguration}}, now)
		return
	}
	if err := ensureBackupDirectory(s.config.LocalDir); err != nil {
		s.finish(ScheduledRun{Err: &Error{Kind: ErrorStorage}}, now)
		return
	}

	if s.config.MinFreeBytes > 0 {
		free, err := s.freeBytes(s.config.LocalDir)
		if err != nil {
			s.finish(ScheduledRun{Err: &Error{Kind: ErrorStorage}}, now)
			return
		}
		if free < s.config.MinFreeBytes {
			s.finish(ScheduledRun{Err: &Error{Kind: ErrorInsufficientSpace}}, now)
			return
		}
	}

	result, err := s.manager.CreateScheduled(context.Background())
	if IsErrorKind(err, ErrorInProgress) {
		s.mu.Lock()
		s.status.LastSkippedAt = now
		s.mu.Unlock()
		logger.Infof(context.Background(), "[backup] scheduled MySQL backup skipped because another backup is running")
		return
	}
	if err != nil {
		s.finish(ScheduledRun{Result: result, Err: err}, now)
		return
	}

	deleted := 0
	var retentionErr error
	if s.config.RetentionDays > 0 {
		deleted, retentionErr = pruneExpiredBackups(s.config.LocalDir, now.Add(-time.Duration(s.config.RetentionDays)*24*time.Hour))
	}
	run := ScheduledRun{Result: result, RetentionDeleted: deleted, RetentionFailed: retentionErr != nil}
	s.finish(run, now)
}

func (s *MySQLScheduler) finish(run ScheduledRun, now time.Time) {
	s.mu.Lock()
	if run.Err != nil {
		s.status.LastFailureAt = now
		s.status.LastFailureKind = scheduledErrorKind(run.Err)
	} else {
		s.status.LastSuccessAt = now
		s.status.LastFailureAt = time.Time{}
		s.status.LastFailureKind = ""
		s.status.LastRetentionAt = now
		s.status.LastRetentionDeleted = run.RetentionDeleted
		if run.RetentionFailed {
			s.status.LastRetentionFailureAt = now
		} else {
			s.status.LastRetentionFailureAt = time.Time{}
		}
	}
	handler := s.onRun
	s.mu.Unlock()

	if run.Err != nil {
		logger.Warnf(context.Background(), "[backup] scheduled MySQL backup failed: kind=%s", scheduledErrorKind(run.Err))
	} else if run.RetentionFailed {
		logger.Warnf(context.Background(), "[backup] scheduled backup completed but retention cleanup failed; details suppressed")
	} else {
		logger.Infof(context.Background(), "[backup] scheduled MySQL backup completed: retention_deleted=%d", run.RetentionDeleted)
	}
	if handler != nil {
		handler(run)
	}
}

func scheduledErrorKind(err error) ErrorKind {
	for _, kind := range []ErrorKind{
		ErrorDisabled, ErrorUnsupportedDatabase, ErrorConfiguration, ErrorInProgress,
		ErrorStorage, ErrorIntegrity, ErrorDump, ErrorTimeout, ErrorInsufficientSpace,
	} {
		if IsErrorKind(err, kind) {
			return kind
		}
	}
	return ErrorStorage
}

func loadScheduleConfigFromEnv() (ScheduleConfig, error) {
	enabled, err := parseBoolEnv("BACKUP_ENABLED", false)
	if err != nil {
		return ScheduleConfig{}, errors.New("BACKUP_ENABLED invalid")
	}
	if !enabled {
		return ScheduleConfig{}, nil
	}

	expression := strings.TrimSpace(os.Getenv("BACKUP_SCHEDULE"))
	if expression == "" {
		return ScheduleConfig{}, nil
	}
	if strings.ContainsAny(expression, "\r\n\x00") {
		return ScheduleConfig{}, errors.New("BACKUP_SCHEDULE invalid")
	}
	if _, err := backupScheduleParser.Parse(expression); err != nil {
		return ScheduleConfig{}, errors.New("BACKUP_SCHEDULE invalid")
	}

	backupConfig, err := LoadMySQLConfigFromEnv()
	if err != nil || !backupConfig.Enabled {
		return ScheduleConfig{}, errors.New("backup configuration invalid")
	}
	retentionDays, err := parseScheduleNonNegativeInt("BACKUP_RETENTION_DAYS", defaultBackupRetentionDays, 3650)
	if err != nil {
		return ScheduleConfig{}, err
	}
	minFreeGB, err := parseScheduleNonNegativeInt("BACKUP_MIN_FREE_GB", defaultBackupMinFreeGB, 1024*1024)
	if err != nil {
		return ScheduleConfig{}, err
	}
	return ScheduleConfig{
		Enabled:       true,
		Expression:    expression,
		RetentionDays: retentionDays,
		MinFreeBytes:  uint64(minFreeGB) * 1024 * 1024 * 1024,
		LocalDir:      backupConfig.LocalDir,
	}, nil
}

func parseScheduleNonNegativeInt(name string, defaultValue, maximum int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || value > maximum {
		return 0, fmt.Errorf("%s invalid", name)
	}
	return value, nil
}

type backupCandidate struct {
	manifestPath string
	archivePath  string
	completedAt  time.Time
}

// pruneExpiredBackups removes only complete, successful archive/manifest
// pairs. The newest valid backup is retained even when its timestamp falls
// outside the policy, preserving a recoverable point after long outages.
func pruneExpiredBackups(directory string, cutoff time.Time) (int, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0, &Error{Kind: ErrorStorage}
	}
	candidates := make([]backupCandidate, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".manifest.json") {
			continue
		}
		manifestPath := filepath.Join(directory, entry.Name())
		contents, readErr := os.ReadFile(manifestPath)
		if readErr != nil {
			continue
		}
		var manifest Manifest
		if json.Unmarshal(contents, &manifest) != nil || !validRetentionManifest(manifest) {
			continue
		}
		if entry.Name() != manifest.BackupID+".manifest.json" {
			continue
		}
		archivePath := filepath.Join(directory, manifest.Archive.File)
		if _, statErr := os.Stat(archivePath); statErr != nil {
			continue
		}
		candidates = append(candidates, backupCandidate{
			manifestPath: manifestPath,
			archivePath:  archivePath,
			completedAt:  manifest.CompletedAt,
		})
	}
	if len(candidates) < 2 {
		return 0, nil
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].completedAt.After(candidates[j].completedAt) })

	deleted := 0
	for _, candidate := range candidates[1:] {
		if !candidate.completedAt.Before(cutoff) {
			continue
		}
		if err := removeBackupPair(candidate); err != nil {
			return deleted, &Error{Kind: ErrorStorage}
		}
		deleted++
	}
	return deleted, nil
}

func validRetentionManifest(manifest Manifest) bool {
	return manifest.FormatVersion == backupManifestVersion &&
		manifest.Result == "success" &&
		manifest.Archive != nil &&
		manifest.CompletedAt.IsZero() == false &&
		filepath.Base(manifest.Archive.File) == manifest.Archive.File &&
		manifest.Archive.File == manifest.BackupID+".sql.gz" &&
		manifest.Archive.Compression == "gzip"
}

func removeBackupPair(candidate backupCandidate) error {
	archiveStaging := candidate.archivePath + ".purging"
	manifestStaging := candidate.manifestPath + ".purging"
	if err := os.Rename(candidate.archivePath, archiveStaging); err != nil {
		return err
	}
	if err := os.Rename(candidate.manifestPath, manifestStaging); err != nil {
		_ = os.Rename(archiveStaging, candidate.archivePath)
		return err
	}
	if err := os.Remove(archiveStaging); err != nil {
		return err
	}
	return os.Remove(manifestStaging)
}
