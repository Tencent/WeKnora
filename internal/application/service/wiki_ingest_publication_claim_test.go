package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 持久回执入口不能因漏传领取而降级为普通的按版本发布。
func TestWikiReceiptPublicationRequiresClaim(t *testing.T) {
	receipt := types.WikiIngestPageProgress{
		Applied: true, EffectsDone: true, Changed: true, Slug: "summary/claim-required",
		PageID: "claim-required-page", PageVersion: 1,
	}
	for _, claim := range []types.WikiProgressClaim{{}, {PendingOpID: 1}, {Epoch: 1}} {
		_, err := wikiPublicationFromReceipt(claim, receipt)
		require.ErrorIs(t, err, types.ErrWikiProgressClaimLost)
	}
}

// TestWikiDocumentSettlementClaimLostBeforePublication retains the adversarial
// window after plan validation and immediately before the real publication write.
func TestWikiDocumentSettlementClaimLostBeforePublication(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const slug = "summary/lost-claim-publication"
		const knowledgeID = "lost-claim-publication-doc"
		f := newWikiSettlementFixture(t, db, map[string][]string{knowledgeID: {slug}})
		f.apply(t, slug)
		injected := false
		f.svc.wikiService = &sharedPublicationHook{
			WikiPageService: f.svc.wikiService,
			before: func(_ context.Context, _ string) error {
				if !injected {
					injected = true
					return db.Model(&types.TaskPendingOp{}).Where("id = ?", f.id(knowledgeID)).
						Update("claim_epoch", gorm.Expr("claim_epoch + 1")).Error
				}
				return nil
			},
		}
		err := f.c.completeSlug(context.Background(), slug)
		require.True(t, injected)
		require.ErrorIs(t, err, types.ErrWikiProgressClaimLost)
		assertWikiPublicationClaimStopped(t, db, f, knowledgeID, slug, types.WikiPageStatusDraft)
	})
}

// TestWikiDocumentSettlementOwnerCancelledBeforePublication keeps cancellation
// inside the original publication hook, after the plan's owner check has returned.
func TestWikiDocumentSettlementOwnerCancelledBeforePublication(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const slug = "summary/cancelled-owner-publication"
		const knowledgeID = "cancelled-owner-publication-doc"
		f := newWikiSettlementFixture(t, db, map[string][]string{knowledgeID: {slug}})
		f.apply(t, slug)
		injected := false
		f.svc.wikiService = &sharedPublicationHook{
			WikiPageService: f.svc.wikiService,
			before: func(_ context.Context, _ string) error {
				if !injected {
					injected = true
					return db.Table("knowledges").Where("id = ?", knowledgeID).
						Update("parse_status", types.ParseStatusCancelled).Error
				}
				return nil
			},
		}
		settleErr := f.c.completeSlug(context.Background(), slug)
		require.True(t, injected)
		t.Logf("settlement returned: %v", settleErr)
		assertWikiPublicationClaimStopped(t, db, f, knowledgeID, slug, types.WikiPageStatusDraft)
		assertWikiPublicationOwnerCancelled(t, db, knowledgeID)
	})
}

type wikiPublicationClaimFault struct {
	name       string
	wantErr    error
	invalidate func(*gorm.DB, int64, string) error
}

func wikiPublicationClaimFaults() []wikiPublicationClaimFault {
	return []wikiPublicationClaimFault{
		{
			name: "claim-lost", wantErr: types.ErrWikiProgressClaimLost,
			invalidate: func(db *gorm.DB, pendingID int64, _ string) error {
				return db.Model(&types.TaskPendingOp{}).Where("id = ?", pendingID).
					Update("claim_epoch", gorm.Expr("claim_epoch + 1")).Error
			},
		},
		{
			name: "owner-cancelled", wantErr: types.ErrWikiProgressOwnerGone,
			invalidate: func(db *gorm.DB, _ int64, knowledgeID string) error {
				return db.Table("knowledges").Where("id = ?", knowledgeID).
					Update("parse_status", types.ParseStatusCancelled).Error
			},
		},
	}
}

// TestWikiResumedPublicationRejectsInactiveClaim exercises batch-tail recovery
// with durable receipts loaded by a fresh service, without another reduce/model write.
func TestWikiResumedPublicationRejectsInactiveClaim(t *testing.T) {
	for _, fault := range wikiPublicationClaimFaults() {
		t.Run(fault.name, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				const slug = "summary/resumed-publication-claim"
				const knowledgeID = "resumed-publication-doc"
				f := resumedWikiPublicationClaimFixture(t, db, knowledgeID, slug)
				injected := false
				f.svc.wikiService = &sharedPublicationHook{
					WikiPageService: f.svc.wikiService,
					before: func(_ context.Context, _ string) error {
						if !injected {
							injected = true
							return fault.invalidate(db, f.id(knowledgeID), knowledgeID)
						}
						return nil
					},
				}
				err := f.batch.publishUnsettledPages(context.Background(), f.svc, []string{slug})
				require.True(t, injected, "fault must occur after the recovered plan load")
				require.ErrorIs(t, err, fault.wantErr)
				assertWikiPublicationClaimStopped(t, db, f, knowledgeID, slug, types.WikiPageStatusDraft)
				if fault.name == "owner-cancelled" {
					assertWikiPublicationOwnerCancelled(t, db, knowledgeID)
				}
			})
		})
	}
}

// TestWikiDocumentSettlementRejectsInactiveClaimAfterPublication separates the
// publication boundary from durable handoff: valid publication cannot authorize
// later finalize rows once its owner or claim has become invalid.
func TestWikiDocumentSettlementRejectsInactiveClaimAfterPublication(t *testing.T) {
	for _, fault := range wikiPublicationClaimFaults() {
		t.Run(fault.name, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				const slug = "summary/publication-before-claim-loss"
				const knowledgeID = "publication-before-claim-loss-doc"
				f := newWikiSettlementFixture(t, db, map[string][]string{knowledgeID: {slug}})
				f.apply(t, slug)
				injected := false
				f.svc.wikiService = &sharedPublicationHook{
					WikiPageService: f.svc.wikiService,
					after: func(_ context.Context, _ string) error {
						if !injected {
							injected = true
							return fault.invalidate(db, f.id(knowledgeID), knowledgeID)
						}
						return nil
					},
				}
				err := f.c.completeSlug(context.Background(), slug)
				require.True(t, injected, "fault must occur after the real page publication")
				require.ErrorIs(t, err, fault.wantErr)
				assertWikiPublicationClaimStopped(t, db, f, knowledgeID, slug, types.WikiPageStatusPublished)
				if fault.name == "owner-cancelled" {
					assertWikiPublicationOwnerCancelled(t, db, knowledgeID)
				}
			})
		})
	}
}

func resumedWikiPublicationClaimFixture(
	t *testing.T, db *gorm.DB, knowledgeID, slug string,
) *wikiSettlementFixture {
	t.Helper()
	seed := newWikiSettlementFixture(t, db, map[string][]string{knowledgeID: {slug}})
	seed.apply(t, slug)
	require.NoError(t, seed.batch.release(context.Background()))
	restarted := newWikiIngestProgressService(t, db)
	ops, _, batch := claimWikiIngestProgress(t, restarted, 1)
	pending, docs, _, err := batch.prepare(context.Background(), restarted, ops, nil, nil, nil, nil,
		&WikiBatchContext{PlannedFolderID: make(map[string]string)})
	require.NoError(t, err)
	require.Empty(t, pending)
	f := &wikiSettlementFixture{svc: restarted, batch: batch, pending: pending, docs: docs}
	f.c = newWikiDocumentSettlement(batch, restarted, wikiSettlementPayload(), docs, pending)
	batch.settlement = f.c
	return f
}

// assertWikiPublicationClaimStopped retains every original counterexample
// assertion and additionally checks that the receipt/queue remains recoverable.
func assertWikiPublicationClaimStopped(
	t *testing.T, db *gorm.DB, f *wikiSettlementFixture, knowledgeID, slug, expectedStatus string,
) {
	t.Helper()
	page, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, slug)
	require.NoError(t, err)
	assert.Equal(t, expectedStatus, page.Status, "an invalid claim cannot authorize a new publication")
	assert.Zero(t, wikiSettlementRows(t, db, wikiFinalizeTaskType, wikiFinalizeOpSlug),
		"an invalid claim must not enqueue finalization")
	assert.Zero(t, wikiSettlementRows(t, db, wikiFinalizeTaskType, wikiFinalizeOpChange),
		"an invalid claim must not append a finalization change")
	require.Equal(t, 2, wikiSettlementSlots(t, db, knowledgeID))
	require.False(t, f.c.isSettled(f.id(knowledgeID)))
	require.False(t, f.c.isHandedOff(f.id(knowledgeID)))
	var pendingRows int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("id = ?", f.id(knowledgeID)).Count(&pendingRows).Error)
	require.Equal(t, int64(1), pendingRows)
	var receipt types.WikiIngestPageProgress
	require.NoError(t, db.Where("pending_op_id = ? AND slug = ?", f.id(knowledgeID), slug).Take(&receipt).Error)
	require.True(t, receipt.Applied && receipt.EffectsDone)
	require.Equal(t, page.ID, receipt.PageID)
	require.Equal(t, page.Version, receipt.PageVersion)
}

func assertWikiPublicationOwnerCancelled(t *testing.T, db *gorm.DB, knowledgeID string) {
	t.Helper()
	var status string
	require.NoError(t, db.Table("knowledges").Where("id = ?", knowledgeID).Select("parse_status").Scan(&status).Error)
	require.Equal(t, types.ParseStatusCancelled, status)
}

// wikiPublicationBeforeHandoff controls the last gap before an actual queue
// insert while delegating to the real repository and its context-bound guard.
type wikiPublicationBeforeHandoff struct {
	interfaces.TaskPendingOpsRepository
	before func(context.Context, *types.TaskPendingOp) error
}

func (r *wikiPublicationBeforeHandoff) Enqueue(ctx context.Context, op *types.TaskPendingOp) error {
	if r.before != nil {
		if err := r.before(ctx, op); err != nil {
			return err
		}
	}
	return r.TaskPendingOpsRepository.Enqueue(ctx, op)
}

// TestWikiSharedPublicationCacheDoesNotAuthorizeInactiveHandoff proves a
// successful same-page cache entry cannot stand in for the second owner's claim.
func TestWikiSharedPublicationCacheDoesNotAuthorizeInactiveHandoff(t *testing.T) {
	for _, fault := range wikiPublicationClaimFaults() {
		t.Run(fault.name, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				const sharedSlug = "summary/cached-shared-claim"
				const slowSlug = "summary/cached-slow-claim"
				const firstID = "cached-first-doc"
				const secondID = "cached-second-doc"
				f := newWikiSettlementFixture(t, db, map[string][]string{
					firstID: {sharedSlug}, secondID: {sharedSlug, slowSlug},
				})
				f.batch.settlement = f.c
				sharedPublishCalls := 0
				f.svc.wikiService = &sharedPublicationHook{
					WikiPageService: f.svc.wikiService,
					before: func(_ context.Context, slug string) error {
						if slug == sharedSlug {
							sharedPublishCalls++
						}
						return nil
					},
				}
				f.apply(t, sharedSlug)
				require.NoError(t, f.c.completeSlug(context.Background(), sharedSlug))
				require.True(t, f.c.isSettled(f.id(firstID)))
				require.False(t, f.c.isSettled(f.id(secondID)))
				require.Equal(t, 1, sharedPublishCalls)
				before := wikiPublicationFinalizeSnapshot(t, db)
				require.Len(t, before, 2, "the first document has one slug and one change handoff")
				f.apply(t, slowSlug)
				injected := false
				f.svc.pendingRepo = &wikiPublicationBeforeHandoff{
					TaskPendingOpsRepository: f.svc.pendingRepo,
					before: func(_ context.Context, op *types.TaskPendingOp) error {
						if op.TaskType == wikiFinalizeTaskType && !injected {
							injected = true
							return fault.invalidate(db, f.id(secondID), secondID)
						}
						return nil
					},
				}
				err := f.c.completeSlug(context.Background(), slowSlug)
				require.True(t, injected, "the second plan loaded and cached publication returned before handoff")
				require.ErrorIs(t, err, fault.wantErr)
				require.Equal(t, 1, sharedPublishCalls, "the second owner used the shared-page success cache")
				require.Equal(t, before, wikiPublicationFinalizeSnapshot(t, db),
					"all previously valid rows survive and no second-owner row is appended")
				require.Equal(t, 1, wikiSettlementSlots(t, db, firstID))
				require.Equal(t, 2, wikiSettlementSlots(t, db, secondID))
				require.False(t, f.c.isSettled(f.id(secondID)))
				require.False(t, f.c.isHandedOff(f.id(secondID)))
				var pending int64
				require.NoError(t, db.Model(&types.TaskPendingOp{}).
					Where("id = ?", f.id(secondID)).Count(&pending).Error)
				require.Equal(t, int64(1), pending)
				if fault.name == "owner-cancelled" {
					assertWikiPublicationOwnerCancelled(t, db, secondID)
				}
			})
		})
	}
}

// TestWikiBatchHandoffRejectsOneInactivePlan prevents aggregate tail work from
// borrowing one live document's claim to publish another document's effects.
func TestWikiBatchHandoffRejectsOneInactivePlan(t *testing.T) {
	for _, fault := range wikiPublicationClaimFaults() {
		t.Run(fault.name, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				const liveID = "tail-live-doc"
				const stoppedID = "tail-stopped-doc"
				f := newWikiSettlementFixture(t, db, map[string][]string{
					liveID: {"summary/tail-live"}, stoppedID: {"summary/tail-stopped"},
				})
				f.batch.settlement = f.c
				ctx := f.batch.handoffContext(context.Background())
				claims, guarded := types.WikiProgressClaimsFromContext(ctx)
				require.True(t, guarded)
				require.Len(t, claims, 2)
				require.NoError(t, fault.invalidate(db, f.id(stoppedID), stoppedID))
				accepted, err := f.svc.enqueueFinalizeRows(ctx, wikiSettlementPayload(),
					[]string{"summary/tail-live", "summary/tail-stopped"}, nil,
					[]wikiFinalizeChange{{Action: wikiFinalizeAdded, DocTitle: stoppedID}},
					[]string{"tail-folder"})
				require.ErrorIs(t, err, fault.wantErr)
				require.False(t, accepted)
				require.Empty(t, wikiPublicationFinalizeSnapshot(t, db),
					"no slug, change or folder row may borrow the remaining live authorization")
				require.Equal(t, 2, wikiSettlementSlots(t, db, liveID))
				require.Equal(t, 2, wikiSettlementSlots(t, db, stoppedID))
			})
		})
	}
}

func TestWikiBatchHandoffRejectsEmptyAuthorization(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		svc := newWikiIngestProgressService(t, db)
		batch := &wikiProgressBatch{}
		ctx := batch.handoffContext(context.Background())
		claims, guarded := types.WikiProgressClaimsFromContext(ctx)
		require.True(t, guarded, "an empty durable authorization is still an authorization requirement")
		require.Empty(t, claims)
		accepted, err := svc.enqueueFinalizeRows(ctx, wikiSettlementPayload(), []string{"summary/empty-claims"}, nil,
			[]wikiFinalizeChange{{Action: wikiFinalizeAdded, DocTitle: "unauthorized"}},
			[]string{"empty-claims-folder"})
		require.ErrorIs(t, err, types.ErrWikiProgressPlanMissing)
		require.False(t, accepted)
		require.Empty(t, wikiPublicationFinalizeSnapshot(t, db))
	})
}

// TestWikiBatchHandoffExcludesSettledDocuments ensures a fast document's
// consumed queue row does not invalidate legitimate work for the remaining tail.
func TestWikiBatchHandoffExcludesSettledDocuments(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const fastID = "tail-settled-fast-doc"
		const slowID = "tail-valid-slow-doc"
		const fastSlug = "summary/tail-fast"
		const slowSlug = "summary/tail-slow"
		f := newWikiSettlementFixture(t, db, map[string][]string{fastID: {fastSlug}, slowID: {slowSlug}})
		f.batch.settlement = f.c
		f.apply(t, fastSlug)
		require.NoError(t, f.c.completeSlug(context.Background(), fastSlug))
		require.True(t, f.c.isSettled(f.id(fastID)))
		_, err := f.batch.repo.LoadWikiIngestPlan(context.Background(), f.batch.claims[f.id(fastID)])
		require.ErrorIs(t, err, types.ErrWikiProgressClaimLost, "the fast document's claim was consumed")
		before := wikiPublicationFinalizeSnapshot(t, db)
		require.Len(t, before, 2)
		ctx := f.batch.handoffContext(context.Background())
		claims, guarded := types.WikiProgressClaimsFromContext(ctx)
		require.True(t, guarded)
		require.Equal(t, []types.WikiProgressClaim{f.batch.claims[f.id(slowID)]}, claims)
		accepted, err := f.svc.enqueueFinalizeRows(ctx, wikiSettlementPayload(), []string{slowSlug}, nil,
			[]wikiFinalizeChange{{Action: wikiFinalizeAdded, DocTitle: slowID}}, nil)
		require.NoError(t, err)
		require.True(t, accepted)
		after := wikiPublicationFinalizeSnapshot(t, db)
		require.Len(t, after, len(before)+2)
		require.Equal(t, before, after[:len(before)], "the fast document's durable handoff stays intact")
		require.Equal(t, 1, wikiSettlementSlots(t, db, fastID))
		require.Equal(t, 2, wikiSettlementSlots(t, db, slowID), "tail enqueue is not document settlement")
	})
}

func wikiPublicationFinalizeSnapshot(t *testing.T, db *gorm.DB) []types.TaskPendingOp {
	t.Helper()
	var rows []types.TaskPendingOp
	require.NoError(t, db.Where("task_type = ?", wikiFinalizeTaskType).Order("id ASC").Find(&rows).Error)
	return rows
}
