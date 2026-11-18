package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	wikiSourceAttemptRawInsert   = "raw-sql"
	wikiSourceAttemptRawUpdate   = "raw-attempt-update"
	wikiSourceAttemptTestTrigger = "trg_wiki_source_attempt_fence"
	wikiSourceAttemptRepoWrite   = "span-repository-sequence"
	wikiSourceAttemptBusyMS      = 100
)

// wikiSourceAttemptAuthorizationDB replaces the progress fixture's minimal span
// table with the actual migration DDL, including required fields and its unique key.
// Neither dialect's production span migration declares a knowledge-row foreign key.
func wikiSourceAttemptAuthorizationDB(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	db := wikiProgressTestDB(t, dialect)
	require.NoError(t, db.Exec("DROP TABLE knowledge_processing_spans").Error)
	migrationPath := filepath.Join("..", "..", "..", "migrations", "versioned",
		"000055_knowledge_processing_spans.up.sql")
	if dialect == "sqlite" {
		migrationPath = filepath.Join("..", "..", "..", "migrations", "sqlite",
			"000008_processing_spans_and_pending_subtasks.up.sql")
	}
	data, err := os.ReadFile(migrationPath)
	require.NoError(t, err)
	ddl := strings.ReplaceAll(string(data), "\r\n", "\n")
	if dialect == "sqlite" {
		// The generic progress fixture already has pending_subtasks_count.
		var found bool
		ddl, _, found = strings.Cut(ddl, "ALTER TABLE knowledges ADD COLUMN pending_subtasks_count")
		require.True(t, found)
	}
	require.NoError(t, db.Exec(ddl).Error)
	if dialect == "postgres" {
		fence, readErr := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned",
			"000139_wiki_source_attempt_fence.up.sql"))
		require.NoError(t, readErr)
		require.NoError(t, db.Exec(string(fence)).Error)
	}
	return db
}

func seedWikiSourceAttemptAuthorization(
	t *testing.T, db *gorm.DB,
) (types.WikiProgressClaim, *types.WikiPage, types.WikiPagePublication) {
	t.Helper()
	claim, page, publication := seedWikiPublicationAuthorization(t, db)
	require.NoError(t, NewKnowledgeSpanRepository(db).Upsert(context.Background(),
		wikiSourceAttemptRoot(claim.KnowledgeID, claim.Attempt)))
	return claim, page, publication
}

func wikiSourceAttemptRoot(knowledgeID string, attempt int) *types.KnowledgeProcessingSpan {
	now := time.Now()
	return &types.KnowledgeProcessingSpan{
		KnowledgeID: knowledgeID, Attempt: attempt, SpanID: uuid.NewString(),
		Name: "knowledge_processing", Kind: types.SpanKindRoot, Status: types.SpanStatusRunning,
		StartedAt: &now,
	}
}

// appendWikiSourceAttempt separates complete raw SQL writes
// from real repository calls. The latter follows OpenAttempt's persistence order;
// it does not invoke the service tracker, heartbeat, or the reparse state machine.
func appendWikiSourceAttempt(
	ctx context.Context, db *gorm.DB, claim types.WikiProgressClaim, mode string,
) error {
	if mode == wikiSourceAttemptRawUpdate {
		result := db.WithContext(ctx).Exec(`UPDATE knowledge_processing_spans SET attempt = attempt + 1
			WHERE knowledge_id = ? AND attempt = ?`, claim.KnowledgeID, claim.Attempt)
		if result.Error == nil && result.RowsAffected != 1 {
			return fmt.Errorf("attempt UPDATE affected %d rows, want 1", result.RowsAffected)
		}
		return result.Error
	}
	if mode == wikiSourceAttemptRawInsert {
		return db.WithContext(ctx).Exec(`INSERT INTO knowledge_processing_spans
			(knowledge_id, attempt, span_id, name, kind, status)
			VALUES (?, ?, ?, ?, ?, ?)`, claim.KnowledgeID, claim.Attempt+1, uuid.NewString(),
			"knowledge_processing", types.SpanKindRoot, types.SpanStatusRunning).Error
	}
	repo := NewKnowledgeSpanRepository(db)
	attempt, err := repo.NextAttempt(ctx, claim.KnowledgeID)
	if err != nil {
		return err
	}
	if attempt != claim.Attempt+1 {
		return fmt.Errorf("new attempt = %d, want %d", attempt, claim.Attempt+1)
	}
	// OpenAttempt logs cancellation failure and still tries to persist its root.
	_, _ = repo.CancelOpenSpansBeforeAttempt(ctx, claim.KnowledgeID, attempt,
		"ATTEMPT_SUPERSEDED", "synthetic newer source attempt")
	return repo.Upsert(ctx, wikiSourceAttemptRoot(claim.KnowledgeID, attempt))
}

func runWikiSourceAttemptAuthorizedWrite(
	ctx context.Context, db *gorm.DB, operation string, claim types.WikiProgressClaim,
	page *types.WikiPage, publication types.WikiPagePublication,
) error {
	if operation == "settlement" {
		_, err := (&taskPendingOpsRepository{db: db}).SettleWikiIngestClaim(ctx, claim, true, false)
		return err
	}
	if operation == "publication" {
		return NewWikiPageRepository(db).PublishDraftPage(types.WithWikiPagePublication(ctx, publication),
			page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug)
	}
	_, err := enqueueWikiAuthorization(types.WithWikiProgressClaims(ctx, []types.WikiProgressClaim{claim}),
		db, wikiAuthorizationFinalizeOp(), false)
	return err
}

// TestWikiSourceAttemptCommittedBeforeAuthorizationIsRejected is the sequential
// control: both complete SQL inserts and repository-created attempts must fence old work.
func TestWikiSourceAttemptCommittedBeforeAuthorizationIsRejected(t *testing.T) {
	for _, mode := range []string{
		wikiSourceAttemptRawInsert, wikiSourceAttemptRepoWrite, wikiSourceAttemptRawUpdate,
	} {
		for _, dialect := range []string{"sqlite", "postgres"} {
			for _, operation := range []string{"publication", "handoff"} {
				t.Run(mode+"/"+dialect+"/"+operation, func(t *testing.T) {
					db := wikiSourceAttemptAuthorizationDB(t, dialect)
					claim, page, publication := seedWikiSourceAttemptAuthorization(t, db)
					ctx, cancel := context.WithTimeout(
						context.Background(), wikiAuthorizationTestTimeout,
					)
					defer cancel()
					require.NoError(t, appendWikiSourceAttempt(ctx, db, claim, mode))
					err := runWikiSourceAttemptAuthorizedWrite(
						ctx, db, operation, claim, page, publication,
					)
					require.ErrorIs(t, err, types.ErrWikiProgressOwnerGone)
					assertWikiSourceAttemptWriteResult(ctx, t, db, operation, page, false)
				})
			}
		}
	}
}

// This is a persistence-boundary regression using actual NextAttempt,
// CancelOpenSpansBeforeAttempt and Upsert, not an end-to-end OpenAttempt test.
func TestWikiSourceAttemptRepositoryInsertCannotCrossAuthorizedWrite(t *testing.T) {
	checkWikiSourceAttemptWriteInterleavings(t, wikiSourceAttemptRepoWrite)
}

// Raw SQL is deliberately a separate protocol-boundary probe. A failure here
// alone is not evidence that production OpenAttempt or a full reparse can race.
func TestWikiSourceAttemptRawInsertCannotCrossAuthorizedWrite(t *testing.T) {
	checkWikiSourceAttemptWriteInterleavings(t, wikiSourceAttemptRawInsert)
}

// Changing an existing span's attempt must fence authorization just like INSERT.
func TestWikiSourceAttemptRawUpdateCannotCrossAuthorizedWrite(t *testing.T) {
	checkWikiSourceAttemptWriteInterleavings(t, wikiSourceAttemptRawUpdate)
}

type wikiSourceAttemptWriteObservation struct {
	writeBackend int
	rivalBackend int
	err          error
}

// A second SQLite pool avoids the fixture's one-connection serialization while
// retaining its real WAL file. PostgreSQL obtains another backend from its pool.
func wikiSourceAttemptRivalDB(t *testing.T, db *gorm.DB, dialect string) *gorm.DB {
	t.Helper()
	if dialect == "postgres" {
		return db
	}
	var sequence int
	var databaseName, databasePath string
	require.NoError(t, db.Raw("PRAGMA database_list").Row().Scan(&sequence, &databaseName, &databasePath))
	require.Equal(t, "main", databaseName)
	require.NotEmpty(t, databasePath)
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=%d&_foreign_keys=on",
		databasePath, wikiSourceAttemptBusyMS)
	rival, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	rivalSQL, err := rival.DB()
	require.NoError(t, err)
	rivalSQL.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, rivalSQL.Close()) })
	return rival
}

func challengeWikiSourceAttemptInsert(
	ctx context.Context, rivalDB, write *gorm.DB, claim types.WikiProgressClaim, dialect, mode string,
) wikiSourceAttemptWriteObservation {
	observed := wikiSourceAttemptWriteObservation{}
	writer, ok := write.Statement.ConnPool.(*sql.Tx)
	if !ok {
		observed.err = errors.New("protected write is not in a SQL transaction")
		return observed
	}
	if dialect == "postgres" {
		if err := writer.QueryRowContext(ctx, "SELECT pg_backend_pid()").
			Scan(&observed.writeBackend); err != nil {
			observed.err = err
			return observed
		}
	}
	observed.err = rivalDB.WithContext(ctx).Connection(func(rival *gorm.DB) error {
		connection, ok := rival.Statement.ConnPool.(*sql.Conn)
		if !ok {
			return errors.New("source-attempt rival did not obtain an independent SQL connection")
		}
		if dialect == "postgres" {
			if err := connection.QueryRowContext(ctx, "SELECT pg_backend_pid()").
				Scan(&observed.rivalBackend); err != nil {
				return err
			}
			_, err := connection.ExecContext(ctx, "SET lock_timeout = '100ms'")
			if err != nil {
				return err
			}
			defer func() { _, _ = connection.ExecContext(ctx, "RESET lock_timeout") }()
		}
		return appendWikiSourceAttempt(ctx, rival, claim, mode)
	})
	return observed
}

func checkWikiSourceAttemptWriteInterleavings(t *testing.T, mode string) {
	t.Helper()
	for _, dialect := range []string{"sqlite", "postgres"} {
		for _, operation := range []string{"publication", "handoff"} {
			t.Run(dialect+"/"+operation, func(t *testing.T) {
				db := wikiSourceAttemptAuthorizationDB(t, dialect)
				claim, page, publication := seedWikiSourceAttemptAuthorization(t, db)
				rivalDB := wikiSourceAttemptRivalDB(t, db, dialect)
				ctx, cancel := context.WithTimeout(context.Background(), wikiAuthorizationTestTimeout)
				defer cancel()
				var maxReadPool *sql.Tx
				injected := false
				var observed wikiSourceAttemptWriteObservation
				readCallback := "test:source-attempt-max-read:" + uuid.NewString()
				maxReadCallbacks := db.Callback().Row().After("gorm:row")
				require.NoError(t, maxReadCallbacks.Register(readCallback, func(tx *gorm.DB) {
					if !injected && strings.Contains(tx.Statement.SQL.String(), "MAX(attempt)") {
						maxReadPool, _ = tx.Statement.ConnPool.(*sql.Tx)
					}
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Row().Remove(readCallback)) })
				challenge := func(tx *gorm.DB) {
					injected = true
					writePool, ok := tx.Statement.ConnPool.(*sql.Tx)
					require.True(t, ok)
					require.NotNil(t, maxReadPool,
						"challenge must follow the real source MAX(attempt) read")
					require.Same(t, maxReadPool, writePool)
					observed = challengeWikiSourceAttemptInsert(
						ctx, rivalDB, tx, claim, dialect, mode,
					)
				}
				writeCallback := "test:source-attempt-authorized-write:" + uuid.NewString()
				if operation == "publication" {
					require.NoError(t, db.Callback().Update().Before("gorm:update").
						Register(writeCallback, func(tx *gorm.DB) {
							fields, ok := tx.Statement.Dest.(map[string]interface{})
							if !injected && ok && tx.Statement.Table == "wiki_pages" &&
								fields["status"] == types.WikiPageStatusPublished {
								challenge(tx)
							}
						}))
					t.Cleanup(func() {
						require.NoError(t, db.Callback().Update().Remove(writeCallback))
					})
				} else {
					require.NoError(t, db.Callback().Create().Before("gorm:create").
						Register(writeCallback, func(tx *gorm.DB) {
							op, ok := tx.Statement.Dest.(*types.TaskPendingOp)
							if !injected && ok &&
								tx.Statement.Table == "task_pending_ops" &&
								op.TaskType == types.TypeWikiFinalize {
								challenge(tx)
							}
						}))
					t.Cleanup(func() {
						require.NoError(t, db.Callback().Create().Remove(writeCallback))
					})
				}
				err := runWikiSourceAttemptAuthorizedWrite(ctx, db, operation, claim, page, publication)
				require.True(t, injected)
				if dialect == "postgres" {
					require.NotZero(t, observed.writeBackend)
					require.NotZero(t, observed.rivalBackend)
					require.NotEqual(t, observed.writeBackend, observed.rivalBackend)
				}
				t.Logf("mode=%s dialect=%s operation=%s writer=%d rival=%d "+
					"insertErr=%v protectedErr=%v", mode, dialect, operation,
					observed.writeBackend, observed.rivalBackend, observed.err, err)
				if observed.err == nil {
					// An independently committed newer attempt must prevent this old write.
					if dialect == "postgres" {
						require.ErrorIs(t, err, types.ErrWikiProgressOwnerGone)
					} else {
						requireWikiSourceAttemptSQLiteRejection(t, err)
					}
					assertWikiSourceAttemptWriteResult(ctx, t, db, operation, page, false)
					return
				}
				if dialect == "postgres" {
					var state interface{ SQLState() string }
					require.ErrorAs(t, observed.err, &state)
					require.Equal(t, wikiAuthorizationLockSQLState, state.SQLState())
				} else {
					requireWikiSourceAttemptSQLiteRejection(t, observed.err)
				}
				require.NoError(t, err)
				assertWikiSourceAttemptWriteResult(ctx, t, db, operation, page, true)
				// The same valid insertion succeeds after the authorized transaction releases its lock.
				require.NoError(t, appendWikiSourceAttempt(ctx, rivalDB, claim, mode))
			})
		}
	}
}

func requireWikiSourceAttemptSQLiteRejection(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	if errors.Is(err, types.ErrWikiProgressOwnerGone) {
		return
	}
	var sqliteErr sqlite3.Error
	require.ErrorAs(t, err, &sqliteErr)
	require.Contains(t, []sqlite3.ErrNo{sqlite3.ErrBusy, sqlite3.ErrLocked}, sqliteErr.Code,
		"only a source-authority rejection or an actual SQLite lock/snapshot conflict is accepted")
}

func assertWikiSourceAttemptWriteResult(
	ctx context.Context, t *testing.T, db *gorm.DB, operation string, page *types.WikiPage, committed bool,
) {
	t.Helper()
	stored, err := NewWikiPageRepository(db).GetByID(ctx, page.ID)
	require.NoError(t, err)
	if operation == "publication" && committed {
		require.Equal(t, types.WikiPageStatusPublished, stored.Status)
	} else {
		require.Equal(t, types.WikiPageStatusDraft, stored.Status)
	}
	var count int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).
		Where("task_type = ?", types.TypeWikiFinalize).Count(&count).Error)
	if operation == "handoff" && committed {
		require.EqualValues(t, 1, count)
	} else {
		require.Zero(t, count)
	}
}

// Keep the protected transaction open while a different backend challenges it.
func challengeWikiSourceAttemptPostgresStatement(
	ctx context.Context, db, write *gorm.DB, statement string, arguments ...interface{},
) wikiSourceAttemptWriteObservation {
	observed := wikiSourceAttemptWriteObservation{}
	writer, ok := write.Statement.ConnPool.(*sql.Tx)
	if !ok {
		observed.err = errors.New("source fence challenge is not inside a SQL transaction")
		return observed
	}
	if err := writer.QueryRowContext(ctx, "SELECT pg_backend_pid()").
		Scan(&observed.writeBackend); err != nil {
		observed.err = err
		return observed
	}
	observed.err = db.WithContext(ctx).Transaction(func(rival *gorm.DB) error {
		connection, ok := rival.Statement.ConnPool.(*sql.Tx)
		if !ok || connection == writer {
			return errors.New("source fence challenge did not obtain an independent transaction")
		}
		if err := connection.QueryRowContext(ctx, "SELECT pg_backend_pid()").
			Scan(&observed.rivalBackend); err != nil {
			return err
		}
		if err := rival.Exec(wikiAuthorizationLockTimeoutSQL).Error; err != nil {
			return err
		}
		return rival.Exec(statement, arguments...).Error
	})
	return observed
}

func requireWikiSourceAttemptIndependentBackends(t *testing.T, observed wikiSourceAttemptWriteObservation) {
	t.Helper()
	require.NotZero(t, observed.writeBackend)
	require.NotZero(t, observed.rivalBackend)
	require.NotEqual(t, observed.writeBackend, observed.rivalBackend)
}

// These callbacks run immediately before each real authorized SQL mutation.
func onWikiSourceAttemptProtectedWrite(
	t *testing.T, db *gorm.DB, operation string, challenge func(*gorm.DB),
) *bool {
	t.Helper()
	injected := new(bool)
	name := "test:source-attempt-fence:" + uuid.NewString()
	if operation == "publication" || operation == "settlement" {
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
			fields, ok := tx.Statement.Dest.(map[string]interface{})
			if *injected || !ok {
				return
			}
			_, releasesSlot := fields["pending_subtasks_count"]
			publication := operation == "publication" && tx.Statement.Table == "wiki_pages" &&
				fields["status"] == types.WikiPageStatusPublished
			settlement := operation == "settlement" && tx.Statement.Table == "knowledges" && releasesSlot
			if publication || settlement {
				*injected = true
				challenge(tx)
			}
		}))
		t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(name)) })
	} else {
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
			op, ok := tx.Statement.Dest.(*types.TaskPendingOp)
			if !*injected && ok && tx.Statement.Table == "task_pending_ops" &&
				op.TaskType == types.TypeWikiFinalize {
				*injected = true
				challenge(tx)
			}
		}))
		t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove(name)) })
	}
	return injected
}

func assertWikiSourceAttemptAuthorizationUnchanged(
	ctx context.Context, t *testing.T, db *gorm.DB, operation string,
	claim types.WikiProgressClaim, page *types.WikiPage,
) {
	t.Helper()
	assertWikiSourceAttemptWriteResult(ctx, t, db, operation, page, false)
	var pending, slots int64
	require.NoError(t, db.WithContext(ctx).Model(&types.TaskPendingOp{}).
		Where("id = ?", claim.PendingOpID).Count(&pending).Error)
	require.EqualValues(t, 1, pending, "rejected settlement must preserve the original claim")
	require.NoError(t, db.WithContext(ctx).Table("knowledges").Select("pending_subtasks_count").
		Where("id = ?", claim.KnowledgeID).Scan(&slots).Error)
	require.EqualValues(t, 2, slots, "rejected settlement must not release a finalizing slot")
}

func TestWikiSourceAttemptFenceRequiresInstalledAlwaysTrigger(t *testing.T) {
	for _, state := range []string{"missing", "disabled", "origin-only"} {
		for _, operation := range []string{"publication", "handoff", "settlement"} {
			t.Run(state+"/"+operation, func(t *testing.T) {
				db := wikiSourceAttemptAuthorizationDB(t, "postgres")
				claim, page, publication := seedWikiSourceAttemptAuthorization(t, db)
				statement := "DROP TRIGGER " + wikiSourceAttemptTestTrigger +
					" ON knowledge_processing_spans"
				switch state {
				case "disabled":
					statement = "ALTER TABLE knowledge_processing_spans DISABLE TRIGGER " +
						wikiSourceAttemptTestTrigger
				case "origin-only":
					statement = "ALTER TABLE knowledge_processing_spans ENABLE TRIGGER " +
						wikiSourceAttemptTestTrigger
				}
				require.NoError(t, db.Exec(statement).Error)
				var enabled int64
				require.NoError(t, db.Raw(`SELECT COUNT(*) FROM pg_trigger
					WHERE tgrelid = 'knowledge_processing_spans'::regclass
					AND tgname = ? AND tgenabled = 'A'`,
					wikiSourceAttemptTestTrigger).Scan(&enabled).Error)
				require.Zero(t, enabled, "the test must remove the always-on writer fence")
				ctx, cancel := context.WithTimeout(context.Background(), wikiAuthorizationTestTimeout)
				defer cancel()
				require.Error(t, runWikiSourceAttemptAuthorizedWrite(
					ctx, db, operation, claim, page, publication,
				))
				assertWikiSourceAttemptAuthorizationUnchanged(ctx, t, db, operation, claim, page)
			})
		}
	}
}

func TestWikiSourceAttemptFenceRejectsRepeatableRead(t *testing.T) {
	for _, operation := range []string{"publication", "handoff", "settlement"} {
		t.Run(operation, func(t *testing.T) {
			db := wikiSourceAttemptAuthorizationDB(t, "postgres")
			claim, page, publication := seedWikiSourceAttemptAuthorization(t, db)
			ctx, cancel := context.WithTimeout(context.Background(), wikiAuthorizationTestTimeout)
			defer cancel()
			err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				var isolation string
				if err := tx.Raw("SELECT current_setting('transaction_isolation')").
					Scan(&isolation).Error; err != nil {
					return err
				}
				require.Equal(t, "repeatable read", isolation)
				// The repository must use this real RR transaction, not the base DB.
				return runWikiSourceAttemptAuthorizedWrite(ctx, tx, operation, claim, page, publication)
			}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			require.Error(t, err)
			assertWikiSourceAttemptAuthorizationUnchanged(ctx, t, db, operation, claim, page)
		})
	}
}

func TestWikiSourceAttemptFenceAllowsConcurrentSpanWriters(t *testing.T) {
	db := wikiSourceAttemptAuthorizationDB(t, "postgres")
	claim, _, _ := seedWikiSourceAttemptAuthorization(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), wikiAuthorizationTestTimeout)
	defer cancel()
	var observed wikiSourceAttemptWriteObservation
	err := db.WithContext(ctx).Transaction(func(first *gorm.DB) error {
		// The first writer stays uncommitted while the second backend writes
		// the same knowledge/attempt with another span ID and commits.
		if err := appendWikiSourceAttempt(ctx, first, claim, wikiSourceAttemptRawInsert); err != nil {
			return err
		}
		observed = challengeWikiSourceAttemptPostgresStatement(ctx, db, first,
			`INSERT INTO knowledge_processing_spans (knowledge_id, attempt, span_id, name, kind, status)
			VALUES (?, ?, ?, 'parallel-span', 'subspan', 'running')`,
			claim.KnowledgeID, claim.Attempt+1, uuid.NewString())
		if observed.err != nil {
			return observed.err
		}
		var committed int64
		if err := db.WithContext(ctx).Table("knowledge_processing_spans").
			Where("knowledge_id = ?", claim.KnowledgeID).Count(&committed).Error; err != nil {
			return err
		}
		require.EqualValues(t, 2, committed, "only original and rival are committed before the first TX ends")
		return nil
	})
	require.NoError(t, err)
	requireWikiSourceAttemptIndependentBackends(t, observed)
	var count int64
	require.NoError(t, db.Table("knowledge_processing_spans").Where("knowledge_id = ?", claim.KnowledgeID).
		Count(&count).Error)
	require.EqualValues(t, 3, count)
}

func TestWikiSourceAttemptFenceBlocksDisableUntilAuthorizedWriteCommits(t *testing.T) {
	for _, operation := range []string{"publication", "handoff"} {
		t.Run(operation, func(t *testing.T) {
			db := wikiSourceAttemptAuthorizationDB(t, "postgres")
			claim, page, publication := seedWikiSourceAttemptAuthorization(t, db)
			ctx, cancel := context.WithTimeout(context.Background(), wikiAuthorizationTestTimeout)
			defer cancel()
			statement := "ALTER TABLE knowledge_processing_spans DISABLE TRIGGER " +
				wikiSourceAttemptTestTrigger
			var observed wikiSourceAttemptWriteObservation
			injected := onWikiSourceAttemptProtectedWrite(t, db, operation, func(tx *gorm.DB) {
				observed = challengeWikiSourceAttemptPostgresStatement(ctx, db, tx, statement)
			})
			require.NoError(t, runWikiSourceAttemptAuthorizedWrite(
				ctx, db, operation, claim, page, publication,
			))
			require.True(t, *injected)
			requireWikiSourceAttemptIndependentBackends(t, observed)
			var state interface{ SQLState() string }
			require.ErrorAs(t, observed.err, &state)
			require.Equal(t, wikiAuthorizationLockSQLState, state.SQLState())
			assertWikiSourceAttemptWriteResult(ctx, t, db, operation, page, true)
			// Prove the same DDL is valid after the protected transaction releases its lock.
			require.NoError(t, db.WithContext(ctx).Exec(statement).Error)
			var enabled string
			require.NoError(t, db.Raw(`SELECT tgenabled::text FROM pg_trigger
				WHERE tgrelid = 'knowledge_processing_spans'::regclass AND tgname = ?`,
				wikiSourceAttemptTestTrigger).Scan(&enabled).Error)
			require.Equal(t, "D", enabled)
		})
	}
}

func TestWikiSourceAttemptFenceSeparatesSchemas(t *testing.T) {
	firstDB := wikiSourceAttemptAuthorizationDB(t, "postgres")
	secondDB := wikiSourceAttemptAuthorizationDB(t, "postgres")
	claim, page, publication := seedWikiSourceAttemptAuthorization(t, firstDB)
	// The same source ID in both schemas exposes a missing relation-OID key.
	otherClaim := seedWikiProgressClaim(t, secondDB, claim.KnowledgeID)
	require.NoError(t, NewKnowledgeSpanRepository(secondDB).Upsert(context.Background(),
		wikiSourceAttemptRoot(otherClaim.KnowledgeID, otherClaim.Attempt)))
	var firstOID, secondOID int64
	require.NoError(t, firstDB.Raw("SELECT 'knowledge_processing_spans'::regclass::oid").Scan(&firstOID).Error)
	require.NoError(t, secondDB.Raw("SELECT 'knowledge_processing_spans'::regclass::oid").Scan(&secondOID).Error)
	require.NotEqual(t, firstOID, secondOID)
	ctx, cancel := context.WithTimeout(context.Background(), wikiAuthorizationTestTimeout)
	defer cancel()
	var observed wikiSourceAttemptWriteObservation
	injected := onWikiSourceAttemptProtectedWrite(t, firstDB, "publication", func(tx *gorm.DB) {
		observed = challengeWikiSourceAttemptPostgresStatement(ctx, secondDB, tx,
			`INSERT INTO knowledge_processing_spans (knowledge_id, attempt, span_id, name, kind, status)
			VALUES (?, ?, ?, 'other-schema', 'root', 'running')`,
			otherClaim.KnowledgeID, otherClaim.Attempt+1, uuid.NewString())
	})
	require.NoError(t, runWikiSourceAttemptAuthorizedWrite(ctx, firstDB, "publication", claim, page, publication))
	require.True(t, *injected)
	requireWikiSourceAttemptIndependentBackends(t, observed)
	require.NoError(t, observed.err, "different source relations must not share one advisory gate")
	assertWikiSourceAttemptWriteResult(ctx, t, firstDB, "publication", page, true)
	var latest int
	require.NoError(t, secondDB.Raw("SELECT MAX(attempt) FROM knowledge_processing_spans WHERE knowledge_id = ?",
		otherClaim.KnowledgeID).Scan(&latest).Error)
	require.Equal(t, otherClaim.Attempt+1, latest)
}

func TestWikiSourceAttemptSettlementHoldsFenceThroughFinalize(t *testing.T) {
	db := wikiSourceAttemptAuthorizationDB(t, "postgres")
	claim, page, publication := seedWikiSourceAttemptAuthorization(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), wikiAuthorizationTestTimeout)
	defer cancel()
	var observed wikiSourceAttemptWriteObservation
	injected := onWikiSourceAttemptProtectedWrite(t, db, "settlement", func(tx *gorm.DB) {
		observed = challengeWikiSourceAttemptInsert(ctx, db, tx, claim, "postgres", wikiSourceAttemptRawInsert)
	})
	require.NoError(t, runWikiSourceAttemptAuthorizedWrite(ctx, db, "settlement", claim, page, publication))
	require.True(t, *injected, "challenge must run immediately before the real finalizing decrement")
	requireWikiSourceAttemptIndependentBackends(t, observed)
	var state interface{ SQLState() string }
	require.ErrorAs(t, observed.err, &state)
	require.Equal(t, wikiAuthorizationLockSQLState, state.SQLState())
	var slots, pending int64
	require.NoError(t, db.Table("knowledges").Select("pending_subtasks_count").
		Where("id = ?", claim.KnowledgeID).Scan(&slots).Error)
	require.EqualValues(t, 1, slots)
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("id = ?", claim.PendingOpID).Count(&pending).Error)
	require.Zero(t, pending)
	// The blocked, valid attempt can commit after settlement releases the fence.
	require.NoError(t, appendWikiSourceAttempt(ctx, db, claim, wikiSourceAttemptRawInsert))
}
