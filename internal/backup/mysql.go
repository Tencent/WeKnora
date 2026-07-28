// Package backup provides small, auditable building blocks for operational
// backups. Database restore orchestration intentionally lives elsewhere.
package backup

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/database"
	"gorm.io/gorm"
)

const (
	defaultBackupLocalDir = "/data/backups"
	defaultBackupTimeout  = 15 * time.Minute
	backupManifestVersion = 1
	backupLockName        = "weknora_mysql_manual_backup"
)

// ErrorKind is safe to expose to administrators. It deliberately excludes
// command output, filesystem paths, DSNs, and credentials.
type ErrorKind string

const (
	ErrorDisabled            ErrorKind = "disabled"
	ErrorUnsupportedDatabase ErrorKind = "unsupported_database"
	ErrorConfiguration       ErrorKind = "configuration_invalid"
	ErrorInProgress          ErrorKind = "in_progress"
	ErrorStorage             ErrorKind = "storage_failed"
	ErrorIntegrity           ErrorKind = "integrity_failed"
	ErrorDump                ErrorKind = "dump_failed"
	ErrorTimeout             ErrorKind = "timeout"
	ErrorInsufficientSpace   ErrorKind = "insufficient_space"
)

// Error represents a backup failure using a safe, stable category.
type Error struct {
	Kind ErrorKind
}

func (e *Error) Error() string {
	return "backup: " + string(e.Kind)
}

// IsErrorKind reports whether err represents the supplied public failure
// category.
func IsErrorKind(err error, kind ErrorKind) bool {
	var backupError *Error
	return errors.As(err, &backupError) && backupError.Kind == kind
}

// MySQLConfig contains the deployment-owned settings required to create a
// logical MySQL backup. It must never be serialized or returned by an API.
type MySQLConfig struct {
	Enabled            bool
	LocalDir           string
	Timeout            time.Duration
	MySQLDumpPath      string
	Host               string
	Port               string
	User               string
	Password           string
	Database           string
	ApplicationVersion string
}

// MigrationState is a snapshot captured in a backup manifest so restore
// verification can check schema compatibility without recording a DSN.
type MigrationState struct {
	Known   bool `json:"known"`
	Version uint `json:"version"`
	Dirty   bool `json:"dirty"`
}

// Archive describes a gzip archive with a SHA-256 checksum. Encryption is
// configured in a later backup-destination phase, so deployments must protect
// this directory.
type Archive struct {
	File        string `json:"file"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	Compression string `json:"compression"`
}

// Manifest is written next to each backup. It contains no absolute paths,
// connection details, credentials, or command output.
type Manifest struct {
	FormatVersion      int            `json:"format_version"`
	BackupID           string         `json:"backup_id"`
	Result             string         `json:"result"`
	Trigger            string         `json:"trigger"`
	Reason             string         `json:"reason"`
	StartedAt          time.Time      `json:"started_at"`
	CompletedAt        time.Time      `json:"completed_at"`
	ApplicationVersion string         `json:"application_version"`
	Migration          MigrationState `json:"migration"`
	Archive            *Archive       `json:"archive,omitempty"`
	FailureKind        ErrorKind      `json:"failure_kind,omitempty"`
}

// Result is the safe projection returned to an administrator after a manual
// backup. File names are relative to BACKUP_LOCAL_DIR.
type Result struct {
	BackupID     string    `json:"backup_id"`
	CreatedAt    time.Time `json:"created_at"`
	ArchiveFile  string    `json:"archive_file,omitempty"`
	ManifestFile string    `json:"manifest_file,omitempty"`
	SizeBytes    int64     `json:"size_bytes,omitempty"`
	SHA256       string    `json:"sha256,omitempty"`
}

type dumpExecutor interface {
	Dump(context.Context, MySQLConfig, io.Writer) error
}

type backupLocker interface {
	Acquire(context.Context) (func(), error)
}

// MySQLManager creates a single logical MySQL backup at a time. Future
// scheduled and restore workflows reuse this manager instead of reimplementing
// its archive and manifest rules.
type MySQLManager struct {
	config      MySQLConfig
	configErr   error
	driverName  func() string
	executor    dumpExecutor
	locker      backupLocker
	now         func() time.Time
	newBackupID func(time.Time) (string, error)
	migration   func() MigrationState
}

// NewMySQLManager builds the production manager from deployment environment
// variables. A malformed optional configuration does not prevent application
// startup; it blocks only the protected backup operation.
func NewMySQLManager(db *gorm.DB) *MySQLManager {
	config, err := LoadMySQLConfigFromEnv()
	return &MySQLManager{
		config:    config,
		configErr: err,
		driverName: func() string {
			if db == nil {
				return ""
			}
			return db.Dialector.Name()
		},
		executor:    mysqldumpExecutor{},
		locker:      mysqlLocker{db: db},
		now:         time.Now,
		newBackupID: newID,
		migration: func() MigrationState {
			version, dirty, known := database.CachedMigrationVersion()
			return MigrationState{Known: known, Version: version, Dirty: dirty}
		},
	}
}

// LoadMySQLConfigFromEnv resolves the manual-backup configuration. The
// password is intentionally retained only in process memory and the temporary
// mysqldump option file.
func LoadMySQLConfigFromEnv() (MySQLConfig, error) {
	enabled, err := parseBoolEnv("BACKUP_ENABLED", false)
	if err != nil {
		return MySQLConfig{}, &Error{Kind: ErrorConfiguration}
	}
	if !enabled {
		return MySQLConfig{}, nil
	}

	config := MySQLConfig{
		Enabled:            true,
		LocalDir:           strings.TrimSpace(os.Getenv("BACKUP_LOCAL_DIR")),
		Timeout:            defaultBackupTimeout,
		MySQLDumpPath:      strings.TrimSpace(os.Getenv("BACKUP_MYSQLDUMP_PATH")),
		Host:               strings.TrimSpace(os.Getenv("DB_HOST")),
		Port:               strings.TrimSpace(os.Getenv("DB_PORT")),
		User:               strings.TrimSpace(os.Getenv("DB_USER")),
		Password:           os.Getenv("DB_PASSWORD"),
		Database:           strings.TrimSpace(os.Getenv("DB_NAME")),
		ApplicationVersion: applicationVersion(),
	}
	if config.LocalDir == "" {
		config.LocalDir = defaultBackupLocalDir
	}
	if config.MySQLDumpPath == "" {
		config.MySQLDumpPath = "mysqldump"
	}
	if config.Port == "" {
		config.Port = "3306"
	}
	if !filepath.IsAbs(config.LocalDir) || filepath.Clean(config.LocalDir) == string(filepath.Separator) {
		return MySQLConfig{}, &Error{Kind: ErrorConfiguration}
	}
	port, err := strconv.ParseUint(config.Port, 10, 16)
	if err != nil || port == 0 {
		return MySQLConfig{}, &Error{Kind: ErrorConfiguration}
	}
	if config.Host == "" || config.User == "" || config.Database == "" || strings.ContainsAny(config.MySQLDumpPath, "\r\n\x00") {
		return MySQLConfig{}, &Error{Kind: ErrorConfiguration}
	}
	for _, value := range []string{config.Host, config.Port, config.User, config.Password, config.Database} {
		if strings.ContainsAny(value, "\r\n\x00") {
			return MySQLConfig{}, &Error{Kind: ErrorConfiguration}
		}
	}
	config.Timeout, err = parseSecondsEnv("BACKUP_TIMEOUT_SECONDS", defaultBackupTimeout, 60, 86400)
	if err != nil {
		return MySQLConfig{}, &Error{Kind: ErrorConfiguration}
	}
	return config, nil
}

// CreateManual creates one compressed MySQL logical backup and its manifest.
// It is synchronous by design: a caller receives success only after the
// archive and checksum have been durably written.
func (m *MySQLManager) CreateManual(ctx context.Context, reason string) (Result, error) {
	return m.create(ctx, "manual", reason)
}

// CreateScheduled creates a backup owned by the configured scheduler. Unlike
// manual backups it has no user-supplied reason, so it cannot accidentally
// persist operator-entered secrets in the manifest.
func (m *MySQLManager) CreateScheduled(ctx context.Context) (Result, error) {
	return m.create(ctx, "scheduled", "scheduled backup")
}

func (m *MySQLManager) create(ctx context.Context, trigger, reason string) (Result, error) {
	if m.driverName == nil || m.driverName() != "mysql" {
		return Result{}, &Error{Kind: ErrorUnsupportedDatabase}
	}
	if m.configErr != nil {
		return Result{}, &Error{Kind: ErrorConfiguration}
	}
	if !m.config.Enabled {
		return Result{}, &Error{Kind: ErrorDisabled}
	}

	operationCtx, cancel := context.WithTimeout(ctx, m.config.Timeout)
	defer cancel()
	release, err := m.locker.Acquire(operationCtx)
	if err != nil {
		if errors.Is(err, ErrBackupInProgress) {
			return Result{}, &Error{Kind: ErrorInProgress}
		}
		return Result{}, &Error{Kind: ErrorStorage}
	}
	defer release()

	if err := ensureBackupDirectory(m.config.LocalDir); err != nil {
		return Result{}, &Error{Kind: ErrorStorage}
	}
	startedAt := m.now().UTC()
	backupID, err := m.newBackupID(startedAt)
	if err != nil {
		return Result{}, &Error{Kind: ErrorStorage}
	}
	result := Result{BackupID: backupID, CreatedAt: startedAt}
	manifest := Manifest{
		FormatVersion:      backupManifestVersion,
		BackupID:           backupID,
		Trigger:            trigger,
		Reason:             reason,
		StartedAt:          startedAt,
		ApplicationVersion: m.config.ApplicationVersion,
		Migration:          m.migration(),
	}

	archiveFile := backupID + ".sql.gz"
	archivePath := filepath.Join(m.config.LocalDir, archiveFile)
	size, checksum, dumpErr := m.writeArchive(operationCtx, archivePath)
	manifest.CompletedAt = m.now().UTC()
	if dumpErr != nil {
		failureKind := ErrorDump
		if errors.Is(operationCtx.Err(), context.DeadlineExceeded) {
			failureKind = ErrorTimeout
		}
		manifest.Result = "failed"
		manifest.FailureKind = failureKind
		manifestPath, err := writeManifest(m.config.LocalDir, manifest)
		if err == nil {
			result.ManifestFile = filepath.Base(manifestPath)
		}
		if err != nil {
			return result, &Error{Kind: ErrorStorage}
		}
		return result, &Error{Kind: failureKind}
	}

	manifest.Result = "success"
	manifest.Archive = &Archive{
		File:        archiveFile,
		SizeBytes:   size,
		SHA256:      checksum,
		Compression: "gzip",
	}
	manifestPath, err := writeManifest(m.config.LocalDir, manifest)
	if err != nil {
		_ = os.Remove(archivePath)
		return result, &Error{Kind: ErrorStorage}
	}
	result.ArchiveFile = archiveFile
	result.ManifestFile = filepath.Base(manifestPath)
	result.SizeBytes = size
	result.SHA256 = checksum
	return result, nil
}

func (m *MySQLManager) isMySQL() bool {
	return m != nil && m.driverName != nil && m.driverName() == "mysql"
}

// VerifyResult recomputes the checksum for a successful archive. Restore
// workflows call this before attempting any import.
func (m *MySQLManager) VerifyResult(result Result) error {
	if result.ArchiveFile == "" || result.ManifestFile == "" || filepath.Base(result.ArchiveFile) != result.ArchiveFile || filepath.Base(result.ManifestFile) != result.ManifestFile {
		return &Error{Kind: ErrorStorage}
	}
	contents, err := os.ReadFile(filepath.Join(m.config.LocalDir, result.ManifestFile))
	if err != nil {
		return &Error{Kind: ErrorStorage}
	}
	var manifest Manifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return &Error{Kind: ErrorStorage}
	}
	if manifest.Result != "success" || manifest.Archive == nil || manifest.BackupID != result.BackupID || manifest.Archive.File != result.ArchiveFile {
		return &Error{Kind: ErrorStorage}
	}
	return VerifyArchive(filepath.Join(m.config.LocalDir, result.ArchiveFile), *manifest.Archive)
}

// VerifyArchive compares the archive's current byte count and SHA-256 sum to
// the immutable manifest record.
func VerifyArchive(path string, archive Archive) error {
	file, err := os.Open(path)
	if err != nil {
		return &Error{Kind: ErrorStorage}
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return &Error{Kind: ErrorStorage}
	}
	if size != archive.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != archive.SHA256 {
		return &Error{Kind: ErrorIntegrity}
	}
	return nil
}

func (m *MySQLManager) writeArchive(ctx context.Context, archivePath string) (int64, string, error) {
	temporary, err := os.CreateTemp(m.config.LocalDir, ".weknora-backup-*.sql.gz.partial")
	if err != nil {
		return 0, "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return 0, "", err
	}
	hash := sha256.New()
	compressed := gzip.NewWriter(io.MultiWriter(temporary, hash))
	dumpErr := m.executor.Dump(ctx, m.config, compressed)
	closeErr := compressed.Close()
	syncErr := temporary.Sync()
	fileErr := temporary.Close()
	if dumpErr != nil {
		return 0, "", dumpErr
	}
	if closeErr != nil || syncErr != nil || fileErr != nil {
		return 0, "", errors.New("backup archive write failed")
	}
	info, err := os.Stat(temporaryPath)
	if err != nil {
		return 0, "", err
	}
	if err := os.Rename(temporaryPath, archivePath); err != nil {
		return 0, "", err
	}
	return info.Size(), hex.EncodeToString(hash.Sum(nil)), nil
}

func ensureBackupDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	return os.Chmod(directory, 0o700)
}

func writeManifest(directory string, manifest Manifest) (string, error) {
	contents, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	contents = append(contents, '\n')
	temporary, err := os.CreateTemp(directory, ".weknora-backup-*.manifest.json.partial")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	path := filepath.Join(directory, manifest.BackupID+".manifest.json")
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", err
	}
	return path, nil
}

// ErrBackupInProgress signals that another process currently owns the MySQL
// advisory lock. It is separate from Error so callers can map it to 409.
var ErrBackupInProgress = errors.New("backup already in progress")

type mysqlLocker struct {
	db *gorm.DB
}

func (l mysqlLocker) Acquire(ctx context.Context) (func(), error) {
	if l.db == nil {
		return nil, errors.New("database unavailable")
	}
	sqlDB, err := l.db.DB()
	if err != nil {
		return nil, err
	}
	connection, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var acquired sql.NullInt64
	if err := connection.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", backupLockName).Scan(&acquired); err != nil {
		_ = connection.Close()
		return nil, err
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		_ = connection.Close()
		return nil, ErrBackupInProgress
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = connection.ExecContext(releaseCtx, "SELECT RELEASE_LOCK(?)", backupLockName)
			_ = connection.Close()
		})
	}, nil
}

type mysqldumpExecutor struct {
	run func(context.Context, string, []string, io.Writer) error
}

func (e mysqldumpExecutor) Dump(ctx context.Context, config MySQLConfig, output io.Writer) error {
	optionFile, err := writeClientOptionFile(config)
	if err != nil {
		return err
	}
	defer os.Remove(optionFile)
	run := e.run
	if run == nil {
		run = runMySQLDump
	}
	return run(ctx, config.MySQLDumpPath, []string{
		"--defaults-extra-file=" + optionFile,
		"--single-transaction",
		"--routines",
		"--events",
		"--triggers",
		"--no-tablespaces",
		"--default-character-set=utf8mb4",
		"--databases",
		config.Database,
	}, output)
}

func runMySQLDump(ctx context.Context, command string, args []string, output io.Writer) error {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("mysqldump failed")
	}
	return nil
}

func writeClientOptionFile(config MySQLConfig) (string, error) {
	file, err := os.CreateTemp("", "weknora-mysqldump-*.cnf")
	if err != nil {
		return "", err
	}
	path := file.Name()
	cleanup := func(err error) (string, error) {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Chmod(0o600); err != nil {
		return cleanup(err)
	}
	contents := fmt.Sprintf(
		"[client]\nhost=%s\nport=%s\nuser=%s\npassword=%s\n",
		mysqlOptionValue(config.Host),
		mysqlOptionValue(config.Port),
		mysqlOptionValue(config.User),
		mysqlOptionValue(config.Password),
	)
	if _, err := io.WriteString(file, contents); err != nil {
		return cleanup(err)
	}
	if err := file.Sync(); err != nil {
		return cleanup(err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func mysqlOptionValue(value string) string {
	escaped := strings.ReplaceAll(value, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	return "\"" + escaped + "\""
}

func parseBoolEnv(name string, defaultValue bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue, nil
	}
	return strconv.ParseBool(raw)
}

func parseSecondsEnv(name string, defaultValue time.Duration, minimum, maximum int) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < minimum || seconds > maximum {
		return 0, errors.New("invalid backup timeout")
	}
	return time.Duration(seconds) * time.Second, nil
}

func applicationVersion() string {
	if version := strings.TrimSpace(os.Getenv("WEKNORA_VERSION")); version != "" {
		return version
	}
	return "dev"
}

func newID(now time.Time) (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "weknora-mysql-" + now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(bytes), nil
}
