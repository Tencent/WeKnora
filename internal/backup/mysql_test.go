package backup

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type fakeDumpExecutor struct {
	payload []byte
	err     error
}

func (e fakeDumpExecutor) Dump(_ context.Context, _ MySQLConfig, output io.Writer) error {
	if e.err != nil {
		return e.err
	}
	_, err := output.Write(e.payload)
	return err
}

type fakeBackupLocker struct {
	err      error
	released bool
}

func (l *fakeBackupLocker) Acquire(context.Context) (func(), error) {
	if l.err != nil {
		return nil, l.err
	}
	return func() { l.released = true }, nil
}

func newTestMySQLManager(directory string, executor dumpExecutor, locker backupLocker) *MySQLManager {
	started := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	return &MySQLManager{
		config: MySQLConfig{
			Enabled:            true,
			LocalDir:           directory,
			Timeout:            time.Minute,
			ApplicationVersion: "test-build",
		},
		driverName: func() string { return "mysql" },
		executor:   executor,
		locker:     locker,
		now:        func() time.Time { return started },
		newBackupID: func(time.Time) (string, error) {
			return "weknora-mysql-test-backup", nil
		},
		migration: func() MigrationState {
			return MigrationState{Known: true, Version: 74}
		},
	}
}

func TestMySQLManagerCreateManualWritesVerifiableBackup(t *testing.T) {
	directory := t.TempDir()
	locker := &fakeBackupLocker{}
	manager := newTestMySQLManager(directory, fakeDumpExecutor{payload: []byte("CREATE TABLE example (id BIGINT);\n")}, locker)

	result, err := manager.CreateManual(context.Background(), "before a schema experiment")
	if err != nil {
		t.Fatalf("CreateManual returned error: %v", err)
	}
	if result.ArchiveFile == "" || result.ManifestFile == "" || result.SHA256 == "" || result.SizeBytes == 0 || !locker.released {
		t.Fatalf("unexpected backup result: %#v released=%t", result, locker.released)
	}
	if err := manager.VerifyResult(result); err != nil {
		t.Fatalf("VerifyResult returned error: %v", err)
	}

	archivePath := filepath.Join(directory, result.ArchiveFile)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(archivePath)
		if err != nil {
			t.Fatalf("os.Stat archive returned error: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("archive permissions = %o, want 600", info.Mode().Perm())
		}
		manifestInfo, err := os.Stat(filepath.Join(directory, result.ManifestFile))
		if err != nil {
			t.Fatalf("os.Stat manifest returned error: %v", err)
		}
		if manifestInfo.Mode().Perm() != 0o600 {
			t.Fatalf("manifest permissions = %o, want 600", manifestInfo.Mode().Perm())
		}
	}
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("os.Open archive returned error: %v", err)
	}
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("gzip.NewReader returned error: %v", err)
	}
	payload, err := io.ReadAll(reader)
	_ = reader.Close()
	_ = file.Close()
	if err != nil || string(payload) != "CREATE TABLE example (id BIGINT);\n" {
		t.Fatalf("unexpected decompressed backup payload: %q error=%v", payload, err)
	}

	manifest, err := os.ReadFile(filepath.Join(directory, result.ManifestFile))
	if err != nil {
		t.Fatalf("os.ReadFile manifest returned error: %v", err)
	}
	if !strings.Contains(string(manifest), `"result": "success"`) || !strings.Contains(string(manifest), `"reason": "before a schema experiment"`) || strings.Contains(string(manifest), "password") || strings.Contains(string(manifest), directory) {
		t.Fatalf("unexpected manifest: %s", manifest)
	}
}

func TestMySQLManagerCreateScheduledWritesScheduledManifest(t *testing.T) {
	directory := t.TempDir()
	manager := newTestMySQLManager(directory, fakeDumpExecutor{payload: []byte("scheduled backup")}, &fakeBackupLocker{})
	result, err := manager.CreateScheduled(context.Background())
	if err != nil {
		t.Fatalf("CreateScheduled returned error: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(directory, result.ManifestFile))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if manifest.Trigger != "scheduled" || manifest.Reason != "scheduled backup" {
		t.Fatalf("unexpected scheduled manifest: %#v", manifest)
	}
}

func TestMySQLManagerCreateManualRecordsFailureWithoutArchive(t *testing.T) {
	directory := t.TempDir()
	manager := newTestMySQLManager(directory, fakeDumpExecutor{err: errors.New("mysqldump exit 1")}, &fakeBackupLocker{})

	result, err := manager.CreateManual(context.Background(), "test failure")
	if !IsErrorKind(err, ErrorDump) {
		t.Fatalf("CreateManual error = %v, want dump failure", err)
	}
	if result.ArchiveFile != "" || result.ManifestFile == "" {
		t.Fatalf("unexpected failed backup result: %#v", result)
	}
	archives, err := filepath.Glob(filepath.Join(directory, "*.sql.gz"))
	if err != nil || len(archives) != 0 {
		t.Fatalf("archives after failed backup = %v error=%v", archives, err)
	}
	manifest, err := os.ReadFile(filepath.Join(directory, result.ManifestFile))
	if err != nil || !strings.Contains(string(manifest), `"result": "failed"`) || !strings.Contains(string(manifest), `"failure_kind": "dump_failed"`) {
		t.Fatalf("unexpected failure manifest: %s error=%v", manifest, err)
	}
}

func TestMySQLManagerVerifyResultDetectsChecksumMismatch(t *testing.T) {
	directory := t.TempDir()
	manager := newTestMySQLManager(directory, fakeDumpExecutor{payload: []byte("valid backup")}, &fakeBackupLocker{})
	result, err := manager.CreateManual(context.Background(), "checksum test")
	if err != nil {
		t.Fatalf("CreateManual returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, result.ArchiveFile), []byte("corrupted"), 0o600); err != nil {
		t.Fatalf("os.WriteFile returned error: %v", err)
	}
	if err := manager.VerifyResult(result); !IsErrorKind(err, ErrorIntegrity) {
		t.Fatalf("VerifyResult error = %v, want checksum failure", err)
	}
}

func TestMySQLManagerCreateManualRejectsConcurrentBackup(t *testing.T) {
	manager := newTestMySQLManager(t.TempDir(), fakeDumpExecutor{}, &fakeBackupLocker{err: ErrBackupInProgress})
	_, err := manager.CreateManual(context.Background(), "concurrent backup")
	if !IsErrorKind(err, ErrorInProgress) {
		t.Fatalf("CreateManual error = %v, want in-progress", err)
	}
}

func TestMySQLDumpExecutorUsesTemporaryOptionFile(t *testing.T) {
	var optionFile string
	executor := mysqldumpExecutor{run: func(_ context.Context, command string, args []string, output io.Writer) error {
		if command != "mysqldump" || len(args) < 2 || !strings.HasPrefix(args[0], "--defaults-extra-file=") {
			t.Fatalf("unexpected mysqldump invocation: %q %q", command, args)
		}
		for _, argument := range args {
			if strings.Contains(argument, "secret-password") || strings.HasPrefix(argument, "--password") {
				t.Fatalf("credential leaked to command argument: %q", argument)
			}
		}
		optionFile = strings.TrimPrefix(args[0], "--defaults-extra-file=")
		contents, err := os.ReadFile(optionFile)
		if err != nil || !strings.Contains(string(contents), `password="secret-password"`) {
			t.Fatalf("unexpected temporary option file: %q error=%v", contents, err)
		}
		_, err = io.WriteString(output, "dump output")
		return err
	}}

	err := executor.Dump(context.Background(), MySQLConfig{
		MySQLDumpPath: "mysqldump",
		Host:          "mysql",
		Port:          "3306",
		User:          "weknora",
		Password:      "secret-password",
		Database:      "weknora",
	}, io.Discard)
	if err != nil {
		t.Fatalf("Dump returned error: %v", err)
	}
	if _, err := os.Stat(optionFile); !os.IsNotExist(err) {
		t.Fatalf("temporary option file still exists: %q error=%v", optionFile, err)
	}
}

func TestMySQLLockerUsesAdvisoryLock(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New returned error: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open returned error: %v", err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT GET_LOCK(?, 0)")).
		WithArgs(backupLockName).
		WillReturnRows(sqlmock.NewRows([]string{"GET_LOCK"}).AddRow(1))
	mock.ExpectExec(regexp.QuoteMeta("SELECT RELEASE_LOCK(?)")).
		WithArgs(backupLockName).
		WillReturnResult(sqlmock.NewResult(0, 1))

	release, err := (mysqlLocker{db: gormDB}).Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	release()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL expectations were not met: %v", err)
	}
}

func TestMySQLLockerRejectsHeldAdvisoryLock(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New returned error: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open returned error: %v", err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT GET_LOCK(?, 0)")).
		WithArgs(backupLockName).
		WillReturnRows(sqlmock.NewRows([]string{"GET_LOCK"}).AddRow(0))

	_, err = (mysqlLocker{db: gormDB}).Acquire(context.Background())
	if !errors.Is(err, ErrBackupInProgress) {
		t.Fatalf("Acquire error = %v, want ErrBackupInProgress", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL expectations were not met: %v", err)
	}
}
