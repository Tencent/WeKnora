package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// publicationWaitContext 在等待分支读取 Done 时发信号，不靠延时猜测等待者已进入。
// publish 的首次检查只读取 Err；因此 entered 明确对应共享结果的等待 select。
type publicationWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *publicationWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

// newPublicationWaitFixture 从真实页面事务与完成副作用回执构造发布目标。
func newPublicationWaitFixture(
	t *testing.T, db *gorm.DB, slug string,
) (*wikiSettlementFixture, types.WikiPagePublication) {
	t.Helper()
	f := newWikiSettlementFixture(t, db, map[string][]string{"doc-publication-wait": {slug}})
	f.batch.settlement = f.c
	f.apply(t, slug)
	claim := f.batch.claims[f.id("doc-publication-wait")]
	state := loadWikiIngestProgress(t, f.svc, claim)
	require.Len(t, state.Pages, 1)
	require.True(t, state.Pages[0].Applied && state.Pages[0].EffectsDone)
	target, err := wikiPublicationFromReceipt(claim, state.Pages[0])
	require.NoError(t, err)
	return f, target
}

func TestWikiPublicationCanceledWaiterDoesNotCancelOwner(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const slug = "summary/publication-wait-cancel"
		f, target := newPublicationWaitFixture(t, db, slug)
		ownerEntered, releaseOwner := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		var calls atomic.Int32
		f.svc.wikiService = &sharedPublicationHook{
			WikiPageService: f.svc.wikiService,
			before: func(context.Context, string) error {
				calls.Add(1)
				return nil
			},
			after: func(ctx context.Context, _ string) error {
				close(ownerEntered)
				select {
				case <-releaseOwner:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
		}
		ownerCtx, cancelOwner := context.WithCancel(context.Background())
		ownerResult, ownerDone := make(chan error, 1), make(chan struct{})
		t.Cleanup(func() {
			releaseOnce.Do(func() { close(releaseOwner) })
			cancelOwner()
			<-ownerDone
		})
		go func() {
			defer close(ownerDone)
			ownerResult <- f.batch.publications.publish(ownerCtx, f.svc, target)
		}()
		awaitEarlySettlement(t, ownerEntered, "发布已落库、拥有者尚未返回")
		waitCtx, cancelWaiter := context.WithCancel(context.Background())
		observedWait := &publicationWaitContext{Context: waitCtx, entered: make(chan struct{})}
		waiterResult, waiterDone := make(chan error, 1), make(chan struct{})
		t.Cleanup(func() { cancelWaiter(); <-waiterDone })
		go func() {
			defer close(waiterDone)
			waiterResult <- f.batch.publications.publish(observedWait, f.svc, target)
		}()
		awaitEarlySettlement(t, observedWait.entered, "第二调用已进入共享结果等待")
		draft, err := sharedPublicationEditDraft(context.Background(), db, slug)
		require.NoError(t, err)
		cancelWaiter()
		require.ErrorIs(t, <-waiterResult, context.Canceled)
		select {
		case err := <-ownerResult:
			t.Fatalf("取消等待者不应提前结束拥有者，得到 %v", err)
		default:
		}
		releaseOnce.Do(func() { close(releaseOwner) })
		require.NoError(t, <-ownerResult)
		require.NoError(t, f.batch.publications.publish(context.Background(), f.svc, target))
		require.EqualValues(t, 1, calls.Load(), "同一回执的等待、取消和重试均不能再次调用页面发布")
		assertWikiSettlementDraftPreserved(t, f, draft)
	})
}

func TestWikiPublicationFailedOwnerWakesWaiterAndAllowsRetry(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const slug = "summary/publication-wait-failure"
		f, target := newPublicationWaitFixture(t, db, slug)
		ownerEntered, releaseOwner := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		var calls atomic.Int32
		failure := errors.New("synthetic publication owner failure")
		f.svc.wikiService = &sharedPublicationHook{
			WikiPageService: f.svc.wikiService,
			before: func(ctx context.Context, _ string) error {
				if calls.Add(1) != 1 {
					return nil
				}
				close(ownerEntered)
				select {
				case <-releaseOwner:
					return failure
				case <-ctx.Done():
					return ctx.Err()
				}
			},
		}
		ownerCtx, cancelOwner := context.WithCancel(context.Background())
		ownerResult, ownerDone := make(chan error, 1), make(chan struct{})
		t.Cleanup(func() {
			releaseOnce.Do(func() { close(releaseOwner) })
			cancelOwner()
			<-ownerDone
		})
		go func() {
			defer close(ownerDone)
			ownerResult <- f.batch.publications.publish(ownerCtx, f.svc, target)
		}()
		awaitEarlySettlement(t, ownerEntered, "拥有者在受控失败前暂停")
		waitCtx, cancelWaiter := context.WithCancel(context.Background())
		observedWait := &publicationWaitContext{Context: waitCtx, entered: make(chan struct{})}
		waiterResult, waiterDone := make(chan error, 1), make(chan struct{})
		t.Cleanup(func() { cancelWaiter(); <-waiterDone })
		go func() {
			defer close(waiterDone)
			waiterResult <- f.batch.publications.publish(observedWait, f.svc, target)
		}()
		awaitEarlySettlement(t, observedWait.entered, "失败前等待者已经进入等待")
		releaseOnce.Do(func() { close(releaseOwner) })
		require.ErrorIs(t, <-ownerResult, failure)
		require.ErrorIs(t, <-waiterResult, failure)
		require.EqualValues(t, 1, calls.Load(), "等待者必须复用拥有者的本次失败")
		page, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, slug)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusDraft, page.Status)
		require.NoError(t, f.batch.publications.publish(context.Background(), f.svc, target))
		require.EqualValues(t, 2, calls.Load(), "失败结果不能永久阻止后续重试")
		page, err = f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, slug)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusPublished, page.Status)
		require.Equal(t, target.PageID, page.ID)
		require.Equal(t, target.PageVersion, page.Version)
	})
}
