package backup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeScheduledCreator struct {
	result Result
	err    error
	calls  int
	mysql  bool
}

func (c *fakeScheduledCreator) CreateScheduled(context.Context) (Result, error) {
	c.calls++
	return c.result, c.err
}

func (c *fakeScheduledCreator) isMySQL() bool { return c.mysql }

func TestLoadScheduleConfigFromEnv(t *testing.T) {
	t.Setenv("BACKUP_ENABLED", "true")
	t.Setenv("BACKUP_SCHEDULE", "30 3 * * *")
	t.Setenv("BACKUP_RETENTION_DAYS", "14")
	t.Setenv("BACKUP_MIN_FREE_GB", "8")
	t.Setenv("BACKUP_LOCAL_DIR", t.TempDir())
	t.Setenv("DB_HOST", "mysql")
	t.Setenv("DB_PORT", "3306")
	t.Setenv("DB_USER", "weknora")
	t.Setenv("DB_PASSWORD", "test")
	t.Setenv("DB_NAME", "weknora")

	config, err := loadScheduleConfigFromEnv()
	if err != nil {
		t.Fatalf("loadScheduleConfigFromEnv returned error: %v", err)
	}
	if !config.Enabled || config.Expression != "30 3 * * *" || config.RetentionDays != 14 || config.MinFreeBytes != 8*1024*1024*1024 {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestLoadScheduleConfigRequiresValidExplicitCron(t *testing.T) {
	t.Setenv("BACKUP_ENABLED", "true")
	t.Setenv("BACKUP_SCHEDULE", "")
	config, err := loadScheduleConfigFromEnv()
	if err != nil || config.Enabled {
		t.Fatalf("empty schedule = %#v, %v; want disabled without error", config, err)
	}

	t.Setenv("BACKUP_SCHEDULE", "every morning")
	if _, err := loadScheduleConfigFromEnv(); err == nil {
		t.Fatal("invalid schedule returned nil error")
	}

	t.Setenv("BACKUP_SCHEDULE", "@daily")
	if _, err := loadScheduleConfigFromEnv(); err == nil {
		t.Fatal("cron descriptor returned nil error")
	}
}

func TestMySQLSchedulerRunSkipsWhenDiskSpaceIsLow(t *testing.T) {
	creator := &fakeScheduledCreator{mysql: true}
	scheduler := newMySQLScheduler(creator, ScheduleConfig{
		Enabled: true, MinFreeBytes: 5 * 1024 * 1024 * 1024, LocalDir: t.TempDir(),
	}, nil)
	scheduler.now = func() time.Time { return time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC) }
	scheduler.freeBytes = func(string) (uint64, error) { return 4 * 1024 * 1024 * 1024, nil }
	var runs []ScheduledRun
	scheduler.SetRunHandler(func(run ScheduledRun) { runs = append(runs, run) })

	scheduler.run()
	status := scheduler.Status()
	if creator.calls != 0 || status.LastFailureKind != ErrorInsufficientSpace || len(runs) != 1 || !IsErrorKind(runs[0].Err, ErrorInsufficientSpace) {
		t.Fatalf("unexpected low-space result: calls=%d status=%#v runs=%#v", creator.calls, status, runs)
	}
}

func TestMySQLSchedulerRunSkipsHeldLockWithoutFailure(t *testing.T) {
	creator := &fakeScheduledCreator{mysql: true, err: &Error{Kind: ErrorInProgress}}
	scheduler := newMySQLScheduler(creator, ScheduleConfig{Enabled: true, LocalDir: t.TempDir()}, nil)
	now := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
	scheduler.now = func() time.Time { return now }
	var runs int
	scheduler.SetRunHandler(func(ScheduledRun) { runs++ })

	scheduler.run()
	status := scheduler.Status()
	if creator.calls != 1 || !status.LastSkippedAt.Equal(now) || !status.LastFailureAt.IsZero() || runs != 0 {
		t.Fatalf("unexpected lock-contention result: calls=%d status=%#v runs=%d", creator.calls, status, runs)
	}
}

func TestMySQLSchedulerRunPurgesExpiredBackupsButKeepsNewest(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
	oldID := "weknora-mysql-20260701T000000Z-000000000000000000000001"
	newID := "weknora-mysql-20260727T000000Z-000000000000000000000002"
	writeRetentionBackup(t, directory, oldID, now.Add(-10*24*time.Hour))
	writeRetentionBackup(t, directory, newID, now.Add(-24*time.Hour))
	creator := &fakeScheduledCreator{mysql: true, result: Result{BackupID: "weknora-mysql-20260728T080000Z-000000000000000000000003"}}
	scheduler := newMySQLScheduler(creator, ScheduleConfig{Enabled: true, RetentionDays: 7, LocalDir: directory}, nil)
	scheduler.now = func() time.Time { return now }
	scheduler.freeBytes = func(string) (uint64, error) { return 0, errors.New("must not inspect disabled threshold") }
	var run ScheduledRun
	scheduler.SetRunHandler(func(value ScheduledRun) { run = value })

	scheduler.run()
	if run.Err != nil || run.RetentionFailed || run.RetentionDeleted != 1 {
		t.Fatalf("unexpected scheduled run: %#v", run)
	}
	if _, err := os.Stat(filepath.Join(directory, oldID+".sql.gz")); !os.IsNotExist(err) {
		t.Fatalf("expired archive still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, oldID+".manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("expired manifest still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, newID+".sql.gz")); err != nil {
		t.Fatalf("newest archive was removed: %v", err)
	}
}

func TestPruneExpiredBackupsNeverDeletesOnlyRecoverableBackup(t *testing.T) {
	directory := t.TempDir()
	backupID := "weknora-mysql-20260701T000000Z-000000000000000000000001"
	writeRetentionBackup(t, directory, backupID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	deleted, err := pruneExpiredBackups(directory, time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC))
	if err != nil || deleted != 0 {
		t.Fatalf("prune = %d, %v; want 0, nil", deleted, err)
	}
	if _, err := os.Stat(filepath.Join(directory, backupID+".manifest.json")); err != nil {
		t.Fatalf("only recoverable manifest was removed: %v", err)
	}
}

func TestPruneExpiredBackupsRemovesAssociatedFileArchive(t *testing.T) {
	directory := t.TempDir()
	oldID := "weknora-mysql-20260701T000000Z-000000000000000000000001"
	newID := "weknora-mysql-20260727T000000Z-000000000000000000000002"
	writeRetentionBackupWithFiles(t, directory, oldID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	writeRetentionBackupWithFiles(t, directory, newID, time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC))
	deleted, err := pruneExpiredBackups(directory, time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC))
	if err != nil || deleted != 1 {
		t.Fatalf("prune = %d, %v; want 1, nil", deleted, err)
	}
	for _, name := range []string{oldID + ".sql.gz", oldID + ".files.tar.gz", oldID + ".files.json", oldID + ".manifest.json"} {
		if _, err := os.Stat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			t.Fatalf("expired associated file still exists: %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(directory, newID+".files.tar.gz")); err != nil {
		t.Fatalf("newest file archive removed: %v", err)
	}
}

func TestMySQLSchedulerDisablesRetentionWhenConfiguredAsZero(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)
	oldID := "weknora-mysql-20260701T000000Z-000000000000000000000001"
	newID := "weknora-mysql-20260727T000000Z-000000000000000000000002"
	writeRetentionBackup(t, directory, oldID, now.Add(-10*24*time.Hour))
	writeRetentionBackup(t, directory, newID, now.Add(-24*time.Hour))
	scheduler := newMySQLScheduler(&fakeScheduledCreator{mysql: true}, ScheduleConfig{
		Enabled: true, RetentionDays: 0, LocalDir: directory,
	}, nil)
	scheduler.now = func() time.Time { return now }

	scheduler.run()
	if _, err := os.Stat(filepath.Join(directory, oldID+".manifest.json")); err != nil {
		t.Fatalf("retention=0 removed an archive: %v", err)
	}
}

func writeRetentionBackup(t *testing.T, directory, backupID string, completedAt time.Time) {
	t.Helper()
	archiveFile := backupID + ".sql.gz"
	archivePath := filepath.Join(directory, archiveFile)
	if err := os.WriteFile(archivePath, []byte("archive"), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	manifest := Manifest{
		FormatVersion: backupManifestVersion,
		BackupID:      backupID,
		Result:        "success",
		CompletedAt:   completedAt,
		Archive: &Archive{
			File: archiveFile, Compression: "gzip",
		},
	}
	contents, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, backupID+".manifest.json"), contents, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func writeRetentionBackupWithFiles(t *testing.T, directory, backupID string, completedAt time.Time) {
	t.Helper()
	writeRetentionBackup(t, directory, backupID, completedAt)
	for _, name := range []string{backupID + ".files.tar.gz", backupID + ".files.json"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("file backup"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	manifestPath := filepath.Join(directory, backupID+".manifest.json")
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	manifest.Files = &FileArchive{File: backupID + ".files.tar.gz", InventoryFile: backupID + ".files.json", SizeBytes: 11, SHA256: "checksum", Compression: "gzip"}
	contents, err = json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, contents, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}
