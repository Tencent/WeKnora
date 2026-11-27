package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	wikiAuthorizationLockTimeoutSQL = "SET LOCAL lock_timeout = '100ms'"
	wikiAuthorizationLockSQLState   = "55P03"
	wikiAuthorizationTestTimeout    = 10 * time.Second
)

// seedWikiPublicationAuthorization creates a committed contribution and completed
// effects receipt, so publication can only fail for the authorization under test.
func seedWikiPublicationAuthorization(
	t *testing.T, db *gorm.DB,
) (types.WikiProgressClaim, *types.WikiPage, types.WikiPagePublication) {
	t.Helper()
	claim := seedWikiProgressClaim(t, db, "authorization-"+uuid.NewString())
	slug := "entity/" + claim.KnowledgeID
	saveWikiProgressPlan(t, db, claim, slug)
	page := wikiProgressPage(slug)
	page.Status = types.WikiPageStatusDraft
	commit := wikiProgressCommit(claim, slug)
	require.NoError(t, NewWikiPageRepository(db).Create(
		types.WithWikiPageProgressCommit(context.Background(), &commit), page,
	))
	require.NoError(t, wikiProgressRepo(t, db).FinishWikiPageEffects(context.Background(), commit))
	publication := wikiPublicationForPage(page)
	publication.Claim = claim
	return claim, page, publication
}

func wikiAuthorizationFinalizeOp() *types.TaskPendingOp {
	return &types.TaskPendingOp{
		TenantID: 1, TaskType: types.TypeWikiFinalize, Scope: types.TaskScopeKnowledgeBase,
		ScopeID: "kb", Op: "finalize", DedupKey: uuid.NewString(), EnqueuedAt: time.Now(),
	}
}

// Both entry points must preserve explicit claim authorization, including wrappers
// that expose only the basic Enqueue method.
func enqueueWikiAuthorization(
	ctx context.Context, db *gorm.DB, op *types.TaskPendingOp, guardedEntry bool,
) (bool, error) {
	queue := &taskPendingOpsRepository{db: db}
	if guardedEntry {
		return queue.EnqueueIfKnowledgeBaseActive(ctx, op)
	}
	err := queue.Enqueue(ctx, op)
	return err == nil, err
}

func assertNoWikiAuthorizationFinalize(t *testing.T, db *gorm.DB) {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).
		Where("task_type = ?", types.TypeWikiFinalize).Count(&count).Error)
	require.Zero(t, count)
}

func wikiAuthorizationMutation(claim types.WikiProgressClaim, mutation string) (string, []interface{}) {
	if mutation == "epoch" {
		return "UPDATE task_pending_ops SET claim_epoch = claim_epoch + 1 WHERE id = ?",
			[]interface{}{claim.PendingOpID}
	}
	return "UPDATE knowledges SET parse_status = ? WHERE id = ?",
		[]interface{}{types.ParseStatusCancelled, claim.KnowledgeID}
}

type wikiAuthorizationLockObservation struct {
	writeBackend int
	rivalBackend int
	err          error
}

// The write callback still owns the original transaction. A second pool connection
// must time out on its locks rather than crossing the authorization/write boundary.
func challengeWikiAuthorizationLock(
	ctx context.Context, db, write *gorm.DB, claim types.WikiProgressClaim, mutation string,
) wikiAuthorizationLockObservation {
	observed := wikiAuthorizationLockObservation{}
	writeSQL, ok := write.Statement.ConnPool.(*sql.Tx)
	if !ok {
		observed.err = errors.New("publication or handoff is not inside a SQL transaction")
		return observed
	}
	if err := writeSQL.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&observed.writeBackend); err != nil {
		observed.err = err
		return observed
	}
	statement, arguments := wikiAuthorizationMutation(claim, mutation)
	observed.err = db.WithContext(ctx).Transaction(func(rival *gorm.DB) error {
		rivalSQL, ok := rival.Statement.ConnPool.(*sql.Tx)
		if !ok || rivalSQL == writeSQL {
			return errors.New("lock challenge did not obtain an independent SQL transaction")
		}
		if err := rivalSQL.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&observed.rivalBackend); err != nil {
			return err
		}
		if err := rival.Exec(wikiAuthorizationLockTimeoutSQL).Error; err != nil {
			return err
		}
		return rival.Exec(statement, arguments...).Error
	})
	return observed
}

func assertWikiAuthorizationLock(
	t *testing.T, db *gorm.DB, claim types.WikiProgressClaim, mutation string,
	observed wikiAuthorizationLockObservation,
) {
	t.Helper()
	require.NotZero(t, observed.writeBackend)
	require.NotZero(t, observed.rivalBackend)
	require.NotEqual(t, observed.writeBackend, observed.rivalBackend)
	var sqlState interface{ SQLState() string }
	require.ErrorAs(t, observed.err, &sqlState)
	require.Equal(t, wikiAuthorizationLockSQLState, sqlState.SQLState())

	var current types.TaskPendingOp
	require.NoError(t, db.Where("id = ?", claim.PendingOpID).Take(&current).Error)
	require.Equal(t, claim.Epoch, current.ClaimEpoch, "the blocked rival must not change the claim")
	var status string
	require.NoError(t, db.Table("knowledges").Select("parse_status").
		Where("id = ?", claim.KnowledgeID).Scan(&status).Error)
	require.Equal(t, types.ParseStatusFinalizing, status, "the blocked rival must not cancel the source")

	// This same mutation can commit after the protected operation, proving that
	// the failure above came from the held lock rather than an invalid statement.
	statement, arguments := wikiAuthorizationMutation(claim, mutation)
	result := db.Exec(statement, arguments...)
	require.NoError(t, result.Error)
	require.EqualValues(t, 1, result.RowsAffected)
}

func TestWikiPublicationAuthorizationHoldsPostgresLocksThroughUpdate(t *testing.T) {
	for _, mutation := range []string{"epoch", "cancel-source"} {
		t.Run(mutation, func(t *testing.T) {
			db := wikiProgressTestDB(t, "postgres")
			claim, page, publication := seedWikiPublicationAuthorization(t, db)
			ctx, cancel := context.WithTimeout(context.Background(), wikiAuthorizationTestTimeout)
			defer cancel()
			var claimRead, receiptRead, injected bool
			var observed wikiAuthorizationLockObservation
			queryCallback := "test:publication-authorized-read:" + uuid.NewString()
			require.NoError(t, db.Callback().Query().After("gorm:query").Register(queryCallback, func(tx *gorm.DB) {
				if tx.Error == nil {
					claimRead = claimRead || tx.Statement.Table == "task_pending_ops"
					receiptRead = receiptRead || tx.Statement.Table == "wiki_ingest_page_progress"
				}
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(queryCallback)) })
			writeCallback := "test:publication-authorized-update:" + uuid.NewString()
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register(writeCallback, func(tx *gorm.DB) {
				fields, ok := tx.Statement.Dest.(map[string]interface{})
				if !ok || tx.Statement.Table != "wiki_pages" ||
					fields["status"] != types.WikiPageStatusPublished || injected {
					return
				}
				injected = true
				observed = challengeWikiAuthorizationLock(ctx, db, tx, claim, mutation)
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(writeCallback)) })

			err := NewWikiPageRepository(db).PublishDraftPage(
				types.WithWikiPagePublication(ctx, publication),
				page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug,
			)
			require.NoError(t, err)
			require.True(t, injected)
			require.True(t, claimRead, "claim authorization must precede the actual UPDATE")
			require.True(t, receiptRead, "receipt authorization must precede the actual UPDATE")
			stored, err := NewWikiPageRepository(db).GetByID(ctx, page.ID)
			require.NoError(t, err)
			require.Equal(t, types.WikiPageStatusPublished, stored.Status)
			assertWikiAuthorizationLock(t, db, claim, mutation, observed)
		})
	}
}

func TestWikiHandoffAuthorizationHoldsPostgresLocksThroughInsert(t *testing.T) {
	for _, guardedEntry := range []bool{false, true} {
		for _, mutation := range []string{"epoch", "cancel-source"} {
			t.Run(fmt.Sprintf("guarded-entry=%t/%s", guardedEntry, mutation), func(t *testing.T) {
				db := wikiProgressTestDB(t, "postgres")
				claim, _, _ := seedWikiPublicationAuthorization(t, db)
				ctx, cancel := context.WithTimeout(context.Background(), wikiAuthorizationTestTimeout)
				defer cancel()
				var claimRead, planRead, injected bool
				var observed wikiAuthorizationLockObservation
				queryCallback := "test:handoff-authorized-read:" + uuid.NewString()
				require.NoError(t, db.Callback().Query().After("gorm:query").Register(queryCallback, func(tx *gorm.DB) {
					if tx.Error == nil {
						claimRead = claimRead || tx.Statement.Table == "task_pending_ops"
						planRead = planRead || tx.Statement.Table == "wiki_ingest_plans"
					}
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(queryCallback)) })
				writeCallback := "test:handoff-authorized-insert:" + uuid.NewString()
				require.NoError(t, db.Callback().Create().Before("gorm:create").
					Register(writeCallback, func(tx *gorm.DB) {
						op, ok := tx.Statement.Dest.(*types.TaskPendingOp)
						if !ok || tx.Statement.Table != "task_pending_ops" ||
							op.TaskType != types.TypeWikiFinalize || injected {
							return
						}
						injected = true
						observed = challengeWikiAuthorizationLock(ctx, db, tx, claim, mutation)
					}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove(writeCallback)) })
				op := wikiAuthorizationFinalizeOp()
				accepted, err := enqueueWikiAuthorization(
					types.WithWikiProgressClaims(ctx, []types.WikiProgressClaim{claim}), db, op, guardedEntry,
				)
				require.NoError(t, err)
				require.True(t, accepted)
				require.True(t, injected)
				require.True(t, claimRead, "claim authorization must precede the actual INSERT")
				require.True(t, planRead, "source-plan authorization must precede the actual INSERT")
				var count int64
				require.NoError(t, db.Model(&types.TaskPendingOp{}).
					Where("id = ? AND task_type = ?", op.ID, types.TypeWikiFinalize).Count(&count).Error)
				require.EqualValues(t, 1, count)
				assertWikiAuthorizationLock(t, db, claim, mutation, observed)
			})
		}
	}
}

// No second SQLite connection is needed: capture authorization ConnPool pointers
// and fail the final write, then verify that the real outer transaction rolls back.
func TestWikiPublicationAndHandoffAuthorizationUseOneSQLiteTransaction(t *testing.T) {
	for _, operation := range []string{"publication", "handoff"} {
		t.Run(operation, func(t *testing.T) {
			db := wikiProgressTestDB(t, "sqlite")
			claim, page, publication := seedWikiPublicationAuthorization(t, db)
			pools := make(map[string]*sql.Tx)
			var writePool *sql.Tx
			injected := errors.New("injected authorized write failure")
			queryCallback := "test:authorization-sqlite-reads:" + uuid.NewString()
			require.NoError(t, db.Callback().Query().After("gorm:query").Register(queryCallback, func(tx *gorm.DB) {
				if pool, ok := tx.Statement.ConnPool.(*sql.Tx); ok && tx.Error == nil {
					pools[tx.Statement.Table] = pool
				}
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(queryCallback)) })
			callback := "test:authorization-sqlite-write:" + uuid.NewString()
			if operation == "publication" {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "wiki_pages" {
						writePool, _ = tx.Statement.ConnPool.(*sql.Tx)
						_ = tx.AddError(injected)
					}
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
			} else {
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "task_pending_ops" {
						writePool, _ = tx.Statement.ConnPool.(*sql.Tx)
						_ = tx.AddError(injected)
					}
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove(callback)) })
			}
			ctx := context.Background()
			var err error
			if operation == "publication" {
				err = NewWikiPageRepository(db).PublishDraftPage(
					types.WithWikiPagePublication(ctx, publication),
					page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug,
				)
			} else {
				_, err = enqueueWikiAuthorization(
					types.WithWikiProgressClaims(ctx, []types.WikiProgressClaim{claim}),
					db, wikiAuthorizationFinalizeOp(), false,
				)
			}
			require.ErrorIs(t, err, injected)
			require.NotNil(t, writePool)
			for _, table := range []string{
				"tenants", "knowledge_bases", "knowledges", "task_pending_ops",
				"wiki_ingest_plans", "wiki_ingest_page_progress",
			} {
				require.NotNil(t, pools[table], "missing authorization query for %s", table)
				require.Same(t, writePool, pools[table], "authorization and write must share sql.Tx: %s", table)
			}
			stored, err := NewWikiPageRepository(db).GetByID(ctx, page.ID)
			require.NoError(t, err)
			require.Equal(t, types.WikiPageStatusDraft, stored.Status)
			assertNoWikiAuthorizationFinalize(t, db)
		})
	}
}

type wikiAuthorizationInvalidClaimCase struct {
	name   string
	change func(*testing.T, *gorm.DB, *types.WikiProgressClaim)
	want   error
}

func wikiAuthorizationInvalidClaims() []wikiAuthorizationInvalidClaimCase {
	return []wikiAuthorizationInvalidClaimCase{
		{"stale-epoch", func(t *testing.T, db *gorm.DB, claim *types.WikiProgressClaim) {
			require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("id = ?", claim.PendingOpID).
				Update("claim_epoch", gorm.Expr("claim_epoch + 1")).Error)
		}, types.ErrWikiProgressClaimLost},
		{"foreign-tenant", func(_ *testing.T, _ *gorm.DB, claim *types.WikiProgressClaim) {
			claim.TenantID++
		}, types.ErrWikiProgressClaimLost},
		{"foreign-knowledge-base", func(_ *testing.T, _ *gorm.DB, claim *types.WikiProgressClaim) {
			claim.KnowledgeBaseID = "other-kb"
		}, types.ErrWikiProgressClaimLost},
		{"cancelled-source", func(t *testing.T, db *gorm.DB, claim *types.WikiProgressClaim) {
			require.NoError(t, db.Table("knowledges").Where("id = ?", claim.KnowledgeID).
				Update("parse_status", types.ParseStatusCancelled).Error)
		}, types.ErrWikiProgressOwnerGone},
		{"newer-source-attempt", func(t *testing.T, db *gorm.DB, claim *types.WikiProgressClaim) {
			insertWikiProgressSourceAttempt(t, db, *claim, claim.Attempt+1)
		}, types.ErrWikiProgressOwnerGone},
		{"missing-plan", func(t *testing.T, db *gorm.DB, claim *types.WikiProgressClaim) {
			require.NoError(t, db.Where("pending_op_id = ?", claim.PendingOpID).
				Delete(&types.WikiIngestPlan{}).Error)
		}, types.ErrWikiProgressPlanMissing},
	}
}

func TestWikiPublicationAndHandoffAuthorizationRejectInvalidClaims(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		for _, tc := range wikiAuthorizationInvalidClaims() {
			for _, operation := range []string{"publication", "enqueue", "guarded-enqueue"} {
				t.Run(tc.name+"/"+operation, func(t *testing.T) {
					claim, page, publication := seedWikiPublicationAuthorization(t, db)
					tc.change(t, db, &claim)
					ctx := context.Background()
					var err error
					if operation == "publication" {
						publication.Claim = claim
						err = NewWikiPageRepository(db).PublishDraftPage(
							types.WithWikiPagePublication(ctx, publication),
							page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug,
						)
					} else {
						var accepted bool
						accepted, err = enqueueWikiAuthorization(
							types.WithWikiProgressClaims(ctx, []types.WikiProgressClaim{claim}),
							db, wikiAuthorizationFinalizeOp(), operation == "guarded-enqueue",
						)
						require.False(t, accepted)
					}
					require.ErrorIs(t, err, tc.want)
					stored, loadErr := NewWikiPageRepository(db).GetByID(ctx, page.ID)
					require.NoError(t, loadErr)
					require.Equal(t, types.WikiPageStatusDraft, stored.Status)
					assertNoWikiAuthorizationFinalize(t, db)
				})
			}
		}
	})
}

func TestWikiPublicationAuthorizationRequiresMatchingCompletedReceipt(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		cases := []struct {
			name   string
			fields map[string]interface{}
			remove bool
			want   error
		}{
			{"not-applied", map[string]interface{}{"applied": false}, false, types.ErrWikiProgressIncomplete},
			{"effects-pending", map[string]interface{}{"effects_done": false}, false, types.ErrWikiProgressIncomplete},
			{"unchanged", map[string]interface{}{"changed": false}, false, types.ErrWikiProgressIncomplete},
			{
				"different-page",
				map[string]interface{}{"page_id": uuid.NewString()},
				false,
				types.ErrWikiProgressIncomplete,
			},
			{"different-version", map[string]interface{}{"page_version": 2}, false, types.ErrWikiProgressIncomplete},
			{"missing", nil, true, types.ErrWikiProgressPlanMissing},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				claim, page, publication := seedWikiPublicationAuthorization(t, db)
				receipt := db.Model(&types.WikiIngestPageProgress{}).
					Where("pending_op_id = ? AND slug = ?", claim.PendingOpID, page.Slug)
				if tc.remove {
					require.NoError(t, receipt.Delete(&types.WikiIngestPageProgress{}).Error)
				} else {
					require.NoError(t, receipt.Updates(tc.fields).Error)
				}
				err := NewWikiPageRepository(db).PublishDraftPage(
					types.WithWikiPagePublication(context.Background(), publication),
					page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug,
				)
				require.ErrorIs(t, err, tc.want)
				stored, loadErr := NewWikiPageRepository(db).GetByID(context.Background(), page.ID)
				require.NoError(t, loadErr)
				require.Equal(t, types.WikiPageStatusDraft, stored.Status)
			})
		}
	})
}

func TestWikiHandoffAuthorizationExplicitEmptyClaimsAndScopeMismatchFailClosed(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		for _, guardedEntry := range []bool{false, true} {
			for _, invalid := range []string{"nil-claims", "empty-claims", "op-tenant", "op-knowledge-base"} {
				t.Run(fmt.Sprintf("guarded-entry=%t/%s", guardedEntry, invalid), func(t *testing.T) {
					claim, _, _ := seedWikiPublicationAuthorization(t, db)
					claims := []types.WikiProgressClaim{claim}
					op := wikiAuthorizationFinalizeOp()
					want := types.ErrWikiProgressClaimLost
					switch invalid {
					case "nil-claims":
						claims = nil
						want = types.ErrWikiProgressPlanMissing
					case "empty-claims":
						claims = []types.WikiProgressClaim{}
						want = types.ErrWikiProgressPlanMissing
					case "op-tenant":
						op.TenantID++
					case "op-knowledge-base":
						op.ScopeID = "other-kb"
					}
					accepted, err := enqueueWikiAuthorization(
						types.WithWikiProgressClaims(context.Background(), claims), db, op, guardedEntry,
					)
					require.False(t, accepted)
					require.ErrorIs(t, err, want)
					assertNoWikiAuthorizationFinalize(t, db)
				})
			}
		}
	})
}
