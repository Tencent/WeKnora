// Package database provides verified schema migration and database readiness checks.
package database

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	// Register the PostgreSQL driver used by dedicated migration connections.
	_ "github.com/lib/pq"
	// Register the SQLite driver used by dedicated migration connections.
	_ "github.com/mattn/go-sqlite3"
)

// OfficialMigrationTable records the official migration chain.
const (
	OfficialMigrationTable = "schema_migrations"
	// Topic3MigrationTable records the independent topic3 migration chain.
	Topic3MigrationTable = "topic3_schema_migrations"
	// MigrationBridgeTable records source provenance and committed progress.
	MigrationBridgeTable = "migration_bridge_runs"
)

// MigrationChainState describes the persisted and required versions of one chain.
type MigrationChainState struct {
	Version         int  `json:"version"`
	Dirty           bool `json:"dirty"`
	ExpectedVersion int  `json:"expected_version"`
}

// MigrationStatus describes verified provenance, chain progress and readiness.
type MigrationStatus struct {
	Dialect         string              `json:"dialect"`
	Source          string              `json:"source"`
	Official        MigrationChainState `json:"official"`
	Topic3          MigrationChainState `json:"topic3"`
	Phase           string              `json:"phase"`
	RunID           string              `json:"run_id,omitempty"`
	InputSHA256     string              `json:"input_sha256"`
	InputManifest   map[string]string   `json:"-"`
	SourceVersion   int                 `json:"source_version"`
	StructureSHA256 string              `json:"structure_sha256"`
	Pending         []string            `json:"pending"`
	Ready           bool                `json:"ready"`
	Error           string              `json:"error,omitempty"`
}

// MigrationOptions supplies explicit inputs and coordination settings.
type MigrationOptions struct {
	// Dirty states require verified repair; true is rejected.
	AutoRecoverDirty bool
	SQLiteDBPath     string
	MigrationsRoot   string
	BackupID         string
	LockTimeout      time.Duration
	testCheckpoint   func(string) error
}

var (
	migrationStateMu       sync.RWMutex
	currentMigrationStatus MigrationStatus
)

// CachedMigrationStatus returns the most recent startup migration result.
func CachedMigrationStatus() MigrationStatus {
	migrationStateMu.RLock()
	defer migrationStateMu.RUnlock()
	result := currentMigrationStatus
	result.Pending = append([]string(nil), result.Pending...)
	return result
}

// CachedMigrationVersion exposes the official version for compatibility callers.
func CachedMigrationVersion() (uint, bool, bool) {
	s := CachedMigrationStatus()
	if s.Dialect == "" || s.Official.Version < 0 {
		return 0, false, false
	}
	return uint(s.Official.Version), s.Official.Dirty, true
}

// CachedMigrationError returns the most recent startup migration error.
func CachedMigrationError() string { return CachedMigrationStatus().Error }

func cacheMigrationStatus(s *MigrationStatus, err error) {
	migrationStateMu.Lock()
	defer migrationStateMu.Unlock()
	currentMigrationStatus = MigrationStatus{}
	if s != nil {
		currentMigrationStatus = *s
	}
	if err != nil {
		currentMigrationStatus.Error = err.Error()
		currentMigrationStatus.Ready = false
	}
}

type sqlQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type migrationConnection struct {
	db            *sql.DB
	conn          *sql.Conn
	dialect, path string
	skipEmbedding bool
}

func openMigrationConnection(
	ctx context.Context,
	dsn string,
	opts MigrationOptions,
	readOnly bool,
) (
	*migrationConnection,
	error,
) {
	if opts.AutoRecoverDirty {
		return nil, fmt.Errorf("automatic dirty-state rewriting is unsupported; inspect and repair " +
			"the recorded state")
	}
	m := &migrationConnection{dialect: "postgres"}
	driver := "postgres"
	skipConfigured := false
	if opts.SQLiteDBPath != "" || strings.HasPrefix(dsn, "sqlite3://") {
		m.dialect, driver = "sqlite", "sqlite3"
		registerMigrationSQLiteExtensions()
		if opts.SQLiteDBPath == "" {
			return nil, fmt.Errorf("SQLite migrations require an explicit raw SQLiteDBPath")
		}
		absolute, err := filepath.Abs(opts.SQLiteDBPath)
		if err != nil {
			return nil, err
		}
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			absolute = resolved
		} else {
			parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
			if err != nil {
				return nil, err
			}
			absolute = filepath.Join(parent, filepath.Base(absolute))
		}
		m.path = absolute
		u := &url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
		q := url.Values{"_foreign_keys": {"on"}, "_busy_timeout": {"5000"}}
		if readOnly {
			q.Set("mode", "ro")
		}
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		u, err := url.Parse(dsn)
		if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
			return nil, fmt.Errorf("PostgreSQL migrations require a postgres URL")
		}
		q := u.Query()
		skipConfigured = q.Has("x-skip-embedding")
		if skipConfigured {
			value, err := strconv.ParseBool(q.Get("x-skip-embedding"))
			if err != nil {
				return nil, fmt.Errorf("invalid embedding migration option")
			}
			m.skipEmbedding = value
		}
		q.Del("x-skip-embedding")
		u.RawQuery = q.Encode()
		dsn = u.String()
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open dedicated migration connection: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect migration database: %w", err)
	}
	m.db, m.conn = db, conn
	if m.dialect == "postgres" && !skipConfigured {
		var setting sql.NullString
		if err := conn.QueryRowContext(
			ctx, "SELECT current_setting('app.skip_embedding',true)",
		).Scan(&setting); err != nil {
			m.close()
			return nil, err
		}
		m.skipEmbedding = setting.String == "true"
	}
	return m, nil
}
func (m *migrationConnection) close() { _ = m.conn.Close(); _ = m.db.Close() }
func (m *migrationConnection) lock(ctx context.Context, timeout time.Duration) (func(), error) {
	if timeout <= 0 {
		timeout = time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if m.dialect == "sqlite" {
		return acquireSQLiteMigrationLock(ctx, m.path+".migration.lock")
	}
	var identity string
	if err := m.conn.QueryRowContext(ctx, "SELECT current_database()").Scan(&identity); err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte("weknora-migration-bridge:" + identity))
	key, _ := strconv.ParseInt(hex.EncodeToString(hash[:7]), 16, 64)
	for {
		var acquired bool
		if err := m.conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired); err != nil {
			return nil, err
		}
		if acquired {
			return func() {
				clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = m.conn.ExecContext(clean, "SELECT pg_advisory_unlock($1)", key)
			}, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("migration coordination lock: %w", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// RunMigrations applies verified migrations using the default options.
func RunMigrations(dsn string) error { return RunMigrationsWithOptions(dsn, MigrationOptions{}) }

// RunMigrationsWithOptions applies migrations within a bounded startup context.
func RunMigrationsWithOptions(dsn string, opts MigrationOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	_, err := ApplyMigrations(ctx, dsn, opts)
	return err
}

// InspectMigrations reads provenance, structures and pending work without writes.
func InspectMigrations(ctx context.Context, dsn string, opts MigrationOptions) (*MigrationStatus, error) {
	files, err := loadMigrationFiles(opts.MigrationsRoot)
	if err != nil {
		return nil, err
	}
	m, err := openMigrationConnection(ctx, dsn, opts, true)
	if err != nil {
		return nil, err
	}
	defer m.close()
	txOptions := &sql.TxOptions{ReadOnly: true}
	if m.dialect == "postgres" {
		txOptions.Isolation = sql.LevelRepeatableRead
	}
	tx, err := m.conn.BeginTx(ctx, txOptions)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	s, err := inspectMigrationState(ctx, tx, m.dialect, files)
	if err != nil {
		return s, err
	}
	if err := tx.Commit(); err != nil {
		return s, err
	}
	return s, nil
}

// ValidateMigrationReadiness checks both chains and updates cached startup status.
func ValidateMigrationReadiness(ctx context.Context, dsn string, opts MigrationOptions) error {
	s, err := InspectMigrations(ctx, dsn, opts)
	if err == nil && !s.Ready {
		err = fmt.Errorf("database migration chains are incomplete: run the verified migration " + "plan")
	}
	cacheMigrationStatus(s, err)
	return err
}

// PrepareDatabaseSchema applies migrations or performs the required read-only startup gate.
func PrepareDatabaseSchema(ctx context.Context, dsn string, opts MigrationOptions, autoMigrate bool) error {
	if autoMigrate {
		_, err := ApplyMigrations(ctx, dsn, opts)
		return err
	}
	return ValidateMigrationReadiness(ctx, dsn, opts)
}

// ApplyMigrations validates provenance and atomically advances each migration under one coordination lock.
func ApplyMigrations(
	ctx context.Context,
	dsn string,
	opts MigrationOptions,
) (
	state *MigrationStatus,
	returned error,
) {
	defer func() { cacheMigrationStatus(state, returned) }()
	files, err := loadMigrationFiles(opts.MigrationsRoot)
	if err != nil {
		return nil, err
	}
	m, err := openMigrationConnection(ctx, dsn, opts, false)
	if err != nil {
		return nil, err
	}
	defer m.close()
	unlock, err := m.lock(ctx, opts.LockTimeout)
	if err != nil {
		return nil, err
	}
	defer unlock()
	state, err = inspectMigrationState(ctx, m.conn, m.dialect, files)
	if err != nil {
		return state, err
	}
	if state.Ready {
		return state, nil
	}
	if state.RunID == "" {
		if state.Source != "empty" && strings.TrimSpace(opts.BackupID) == "" {
			return state, fmt.Errorf("accepting an existing database requires MigrationOptions.BackupID")
		}
		if err := acceptMigrationSource(ctx, m, state, opts.BackupID); err != nil {
			return state, err
		}
	}
	if err := migrationCheckpoint(opts, "accepted"); err != nil {
		return state, err
	}
	if m.dialect == "postgres" {
		if _, err := m.conn.ExecContext(
			ctx,
			"SELECT set_config('app.skip_embedding', $1, false)",
			strconv.FormatBool(m.skipEmbedding),
		); err != nil {
			return state, err
		}
	}
	for _, chain := range []string{"official", "topic3"} {
		for _, file := range files.chain(m.dialect, chain) {
			current := state.Official.Version
			if chain == "topic3" {
				current = state.Topic3.Version
			}
			if file.Version <= current {
				continue
			}
			if err := executeMigration(ctx, m, state, file, chain); err != nil {
				recordMigrationFailure(m, state, chain, file.Version)
				return state, err
			}
			if err := migrationCheckpoint(opts, chain+":"+strconv.Itoa(file.Version)); err != nil {
				return state, err
			}
		}
	}
	verified, err := inspectMigrationState(ctx, m.conn, m.dialect, files)
	if err != nil {
		return state, err
	}
	state = verified
	if state.Official.Version != state.Official.ExpectedVersion ||
		state.Topic3.Version != state.Topic3.ExpectedVersion {
		return state, fmt.Errorf("migration chains did not reach declared targets")
	}
	if err := markBridgeComplete(ctx, m, state); err != nil {
		return state, err
	}
	state.Phase, state.Ready = "complete", true
	return state, nil
}

func migrationCheckpoint(opts MigrationOptions, phase string) error {
	if opts.testCheckpoint != nil {
		return opts.testCheckpoint(phase)
	}
	return nil
}

func parameter(dialect string, i int) string {
	if dialect == "postgres" {
		return "$" + strconv.Itoa(i)
	}
	return "?"
}

func replaceVersion(ctx context.Context, tx *sql.Tx, dialect, table string, version int) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
		return err
	}
	if version < 0 {
		return nil
	}
	_, err := tx.ExecContext(
		ctx,
		"INSERT INTO "+table+" (version, dirty) VALUES ("+parameter(dialect, 1)+", false)",
		version,
	)
	return err
}

func acceptMigrationSource(ctx context.Context, m *migrationConnection, s *MigrationStatus, backup string) error {
	identity := m.path
	if m.dialect == "postgres" {
		if err := m.conn.QueryRowContext(ctx, "SELECT current_database() || ':' || current_schema()").Scan(
			&identity,
		); err != nil {
			return err
		}
	}
	tx, err := m.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, table := range []string{OfficialMigrationTable, Topic3MigrationTable} {
		if _, err := tx.ExecContext(
			ctx,
			"CREATE TABLE IF NOT EXISTS "+table+" (version BIGINT NOT NULL PRIMARY KEY, dirty BOOLEAN NOT NULL)",
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			"CREATE UNIQUE INDEX IF NOT EXISTS "+table+"_version_unique ON "+table+" (version)",
		); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "CREATE TABLE migration_bridge_runs (\n\t\tid VARCHAR(64) PRIMARY KEY, "+
		"source_profile TEXT NOT NULL, source_official BIGINT NOT NULL,"+
		"\n\t\tsource_topic3 BIGINT NOT NULL, source_structure_sha256 VARCHAR(64) "+
		"NOT NULL,\n\t\tinput_sha256 VARCHAR(64) NOT NULL, input_manifest TEXT "+
		"NOT NULL, source_version BIGINT NOT NULL,\n\t\tsource_database_sha256 "+
		"VARCHAR(64) NOT NULL, source_dirty BOOLEAN NOT NULL,"+
		"\n\t\tprofile_version BIGINT NOT NULL, official_target BIGINT NOT NULL, "+
		"topic3_target BIGINT NOT NULL,\n\t\tbackup_id TEXT NOT NULL, phase "+
		"VARCHAR(32) NOT NULL,\n\t\tofficial_version BIGINT NOT NULL, "+
		"topic3_version BIGINT NOT NULL, error_code TEXT NOT NULL DEFAULT '',"+
		"\n\t\tstarted_at TEXT NOT NULL, updated_at TEXT NOT NULL, completed_at "+
		"TEXT)"); err != nil {
		return err
	}
	if err := replaceVersion(ctx, tx, m.dialect, OfficialMigrationTable, s.Official.Version); err != nil {
		return err
	}
	if err := replaceVersion(ctx, tx, m.dialect, Topic3MigrationTable, s.Topic3.Version); err != nil {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	id := hex.EncodeToString(random[:])
	now := time.Now().UTC().Format(time.RFC3339Nano)
	manifest, err := json.Marshal(s.InputManifest)
	if err != nil {
		return err
	}
	args := []any{
		id,
		s.Source,
		s.Official.Version,
		s.Topic3.Version,
		s.StructureSHA256,
		s.InputSHA256,
		string(manifest),
		s.SourceVersion,
		hashJSON(identity),
		false,
		1,
		s.Official.ExpectedVersion,
		s.Topic3.ExpectedVersion,
		backup,
		"accepted",
		s.Official.Version,
		s.Topic3.Version,
		now,
		now,
	}
	marks := make([]string, len(args))
	for i := range args {
		marks[i] = parameter(m.dialect, i+1)
	}
	query := "INSERT INTO migration_bridge_runs (id,source_profile,source_official," +
		"source_topic3,source_structure_sha256,input_sha256,input_manifest," +
		"source_version,source_database_sha256,source_dirty,profile_version," +
		"official_target,topic3_target,backup_id,phase,official_version," +
		"topic3_version,started_at,updated_at) VALUES (" + strings.Join(marks, ",") + ")"
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.RunID, s.Phase = id, "accepted"
	return nil
}

func executeMigration(
	ctx context.Context,
	m *migrationConnection,
	s *MigrationStatus,
	file migrationFile,
	chain string,
) error {
	tx, err := m.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, file.SQL); err != nil {
		return fmt.Errorf("migration %s failed; transaction rolled back: %w", file.Path, err)
	}
	table := OfficialMigrationTable
	if chain == "topic3" {
		table = Topic3MigrationTable
	}
	if err := replaceVersion(ctx, tx, m.dialect, table, file.Version); err != nil {
		return err
	}
	official, topic := s.Official.Version, s.Topic3.Version
	if chain == "official" {
		official = file.Version
	} else {
		topic = file.Version
	}
	manifest, err := json.Marshal(s.InputManifest)
	if err != nil {
		return err
	}
	query := "UPDATE migration_bridge_runs SET phase=" + parameter(m.dialect, 1) + ", official_version=" +
		parameter(m.dialect, 2) +
		", topic3_version=" +
		parameter(m.dialect, 3) +
		", updated_at=" +
		parameter(m.dialect, 4) +
		", input_sha256=" +
		parameter(m.dialect, 5) +
		", input_manifest=" +
		parameter(m.dialect, 6) +
		", error_code='', completed_at=NULL, official_target=" +
		parameter(m.dialect, 7) +
		", topic3_target=" +
		parameter(m.dialect, 8) +
		" WHERE id=" +
		parameter(m.dialect, 9)
	result, err := tx.ExecContext(
		ctx,
		query,
		chain,
		official,
		topic,
		time.Now().UTC().Format(time.RFC3339Nano),
		s.InputSHA256,
		string(manifest),
		s.Official.ExpectedVersion,
		s.Topic3.ExpectedVersion,
		s.RunID,
	)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("migration bridge receipt is missing")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.Official.Version, s.Topic3.Version, s.Phase = official, topic, chain
	return nil
}

func markBridgeComplete(ctx context.Context, m *migrationConnection, s *MigrationStatus) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	r, err := m.conn.ExecContext(
		ctx, "UPDATE migration_bridge_runs SET phase='complete', error_code='', "+
			"completed_at="+parameter(m.dialect, 1)+", updated_at="+parameter(m.dialect, 2)+
			" WHERE id="+parameter(m.dialect, 3),
		now, now, s.RunID,
	)
	if err != nil {
		return err
	}
	if n, err := r.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("migration completion receipt is missing")
	}
	return nil
}

func recordMigrationFailure(m *migrationConnection, s *MigrationStatus, chain string, version int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Connection loss can prevent the receipt update; committed versions remain the recovery authority.
	_, _ = m.conn.ExecContext(
		ctx,
		"UPDATE migration_bridge_runs SET error_code="+parameter(m.dialect, 1)+", updated_at="+
			parameter(m.dialect, 2)+
			" WHERE id="+
			parameter(m.dialect, 3),
		fmt.Sprintf("%s_%d_transaction_failed", chain, version),
		time.Now().UTC().Format(time.RFC3339Nano),
		s.RunID,
	)
}

// GetMigrationVersion inspects the official version from PostgreSQL environment settings.
func GetMigrationVersion() (uint, bool, error) {
	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD")),
		Host:     os.Getenv("DB_HOST") + ":" + os.Getenv("DB_PORT"),
		Path:     os.Getenv("DB_NAME"),
		RawQuery: "sslmode=disable",
	}
	s, err := InspectMigrations(context.Background(), u.String(), MigrationOptions{})
	if err != nil {
		return 0, false, err
	}
	if s.Official.Version < 0 {
		return 0, false, sql.ErrNoRows
	}
	return uint(s.Official.Version), s.Official.Dirty, nil
}
