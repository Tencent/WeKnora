package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

// wikiDocumentSettlement 只管理本批次的文档结算；页面与回执仍以数据库为准。
type wikiDocumentSettlement struct {
	mu      sync.Mutex
	batch   *wikiProgressBatch
	service *wikiIngestService
	payload WikiIngestPayload
	docs    map[string]*docIngestResult
	entries map[int64]*wikiDocumentSettlementEntry
	owners  map[int64]int64
}

type wikiDocumentSettlementEntry struct {
	claim         types.WikiProgressClaim
	waiting       map[string]bool
	busy          bool
	settled       bool
	handedOff     bool
	discarded     map[int64]bool
	settledResult *types.WikiIngestPlanState
}

// newWikiDocumentSettlement 仅纳入已冻结计划的 ingest；撤回及目录剪枝保留原批次收尾顺序。
func newWikiDocumentSettlement(
	b *wikiProgressBatch,
	s *wikiIngestService,
	payload WikiIngestPayload,
	docs []*docIngestResult,
	pending map[string][]SlugUpdate,
) *wikiDocumentSettlement {
	c := &wikiDocumentSettlement{
		batch: b, service: s, payload: payload,
		docs: make(map[string]*docIngestResult), entries: make(map[int64]*wikiDocumentSettlementEntry),
		owners: make(map[int64]int64),
	}
	for _, doc := range docs {
		if doc != nil {
			c.docs[doc.KnowledgeID] = doc
		}
	}
	if b == nil {
		return c
	}
	for knowledgeID, op := range b.canonical {
		state := b.states[op.dbID]
		claim, exists := b.claims[op.dbID]
		if !exists || op.Op != WikiOpIngest || state == nil || state.Plan == nil {
			continue
		}
		c.entries[op.dbID] = &wikiDocumentSettlementEntry{
			claim: claim, waiting: make(map[string]bool), discarded: make(map[int64]bool),
		}
		for id, sibling := range b.claims {
			if sibling.KnowledgeID == knowledgeID {
				c.owners[id] = op.dbID
			}
		}
	}
	for slug, updates := range pending {
		for _, update := range updates {
			if op, ok := b.canonical[update.KnowledgeID]; ok {
				if entry := c.entries[op.dbID]; entry != nil {
					entry.waiting[slug] = true
				}
			}
		}
	}
	return c
}

// completeSlug 必须在整组贡献及必要反链都成功后调用，不能把单个贡献成功当成整页完成。
func (c *wikiDocumentSettlement) completeSlug(ctx context.Context, slug string) error {
	c.mu.Lock()
	var ready []int64
	for id, entry := range c.entries {
		if !entry.waiting[slug] {
			continue
		}
		delete(entry.waiting, slug)
		if !entry.busy && !entry.settled && len(entry.waiting) == 0 {
			entry.busy = true
			ready = append(ready, id)
		}
	}
	c.mu.Unlock()
	return c.settleClaimed(ctx, ready)
}

// settleReady 处理恢复后没有待写页的文档，也允许重试本进程内失败的发布或结算。
func (c *wikiDocumentSettlement) settleReady(ctx context.Context) error {
	c.mu.Lock()
	var ready []int64
	for id, entry := range c.entries {
		if !entry.busy && !entry.settled && len(entry.waiting) == 0 {
			entry.busy = true
			ready = append(ready, id)
		}
	}
	c.mu.Unlock()
	return c.settleClaimed(ctx, ready)
}

// settleClaimed 不持锁进行数据库操作，慢文档不会阻塞其他回调更新依赖集合。
func (c *wikiDocumentSettlement) settleClaimed(ctx context.Context, ready []int64) error {
	sort.Slice(ready, func(i, j int) bool { return ready[i] < ready[j] })
	var errs []error
	for _, id := range ready {
		state, err := c.settleDocument(ctx, id)
		c.mu.Lock()
		entry := c.entries[id]
		entry.busy = false
		if err == nil {
			entry.settled = true
			entry.settledResult = state
		}
		c.mu.Unlock()
		if err != nil {
			errs = append(errs, fmt.Errorf("settle wiki document %s: %w", entry.claim.KnowledgeID, err))
			continue
		}
		if doc := c.docs[entry.claim.KnowledgeID]; doc != nil {
			c.service.endWikiDocumentSpan(ctx, doc, nil)
		}
	}
	return errors.Join(errs...)
}

// settleDocument 先发布本回执的页面并持久交接全局收尾，再原子扣计数和删除当前领取。
func (c *wikiDocumentSettlement) settleDocument(
	ctx context.Context, id int64,
) (*types.WikiIngestPlanState, error) {
	entry := c.entries[id]
	claim := entry.claim
	state, err := c.batch.repo.LoadWikiIngestPlan(ctx, claim)
	if err != nil {
		return nil, err
	}
	if state == nil || state.Plan == nil {
		return nil, types.ErrWikiProgressPlanMissing
	}
	for _, page := range state.Pages {
		if !page.Applied || !page.EffectsDone {
			return nil, types.ErrWikiProgressIncomplete
		}
	}

	// 不借用同批其他文档的回执，也不发布删除后以相同 slug 新建的页面。
	var slugs []string
	expectedIDs := make(map[string]string)
	for _, receipt := range state.Pages {
		if !receipt.Changed {
			continue
		}
		page, loadErr := c.service.wikiService.GetPageBySlug(ctx, claim.KnowledgeBaseID, receipt.Slug)
		if errors.Is(loadErr, apprepo.ErrWikiPageNotFound) {
			continue
		}
		if loadErr != nil {
			return nil, loadErr
		}
		if page == nil || page.ID != receipt.PageID {
			continue
		}
		slugs = append(slugs, receipt.Slug)
		expectedIDs[receipt.Slug] = receipt.PageID
	}
	// 发布授权取自写入时的持久回执；共享页由本批协调，不能借当前页面版本重新授权。
	var publishErrors []error
	for _, receipt := range state.Pages {
		if !receipt.Changed || expectedIDs[receipt.Slug] != receipt.PageID {
			continue
		}
		target, targetErr := wikiPublicationFromReceipt(claim, receipt)
		if targetErr != nil {
			return nil, targetErr
		}
		publishErrors = append(publishErrors, c.batch.publications.publish(ctx, c.service, target))
	}
	if err := errors.Join(publishErrors...); err != nil {
		return nil, err
	}

	c.mu.Lock()
	handedOff := entry.handedOff
	c.mu.Unlock()
	if !handedOff {
		titles := make(map[string]string)
		var changes []wikiFinalizeChange
		if doc := c.docs[claim.KnowledgeID]; doc != nil {
			for _, page := range doc.Pages {
				if expectedIDs[page.Slug] != "" && page.Title != "" {
					titles[page.Slug] = page.Title
				}
			}
			changes = append(changes, wikiFinalizeChange{
				Action: wikiFinalizeAdded, DocTitle: doc.DocTitle, DocSummary: doc.Summary,
			})
		}
		_, enqueueErr := c.service.enqueueFinalizeRows(ctx, c.payload, slugs, titles, changes, nil)
		c.mu.Lock()
		// 部分写入失败仍由恢复路径重交；这里只避免成功交接后的进程内重复 change。
		if enqueueErr == nil {
			entry.handedOff = true
		}
		c.mu.Unlock()
		if enqueueErr != nil {
			return nil, enqueueErr
		}
	}

	// 先清同文档旧行，最后消费 canonical，避免成功结算后遗留旧行再次重放。
	var siblings []int64
	for siblingID, sibling := range c.batch.claims {
		if siblingID != id && sibling.KnowledgeID == claim.KnowledgeID {
			siblings = append(siblings, siblingID)
		}
	}
	sort.Slice(siblings, func(i, j int) bool { return siblings[i] < siblings[j] })
	for _, siblingID := range siblings {
		c.mu.Lock()
		discarded := entry.discarded[siblingID]
		c.mu.Unlock()
		if discarded {
			continue
		}
		ok, settleErr := c.batch.repo.SettleWikiIngestClaim(ctx, c.batch.claims[siblingID], false, true)
		if settleErr != nil {
			return nil, settleErr
		}
		if !ok {
			return nil, types.ErrWikiProgressClaimLost
		}
		c.mu.Lock()
		entry.discarded[siblingID] = true
		c.mu.Unlock()
	}
	finalize := !attemptSuperseded(ctx, c.service.tracker(), claim.KnowledgeID, claim.Attempt)
	ok, err := c.batch.repo.SettleWikiIngestClaim(ctx, claim, finalize, false)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, types.ErrWikiProgressClaimLost
	}
	return state, nil
}

func (c *wikiDocumentSettlement) isSettled(id int64) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[c.owners[id]]
	return entry != nil && entry.settled
}

func (c *wikiDocumentSettlement) isHandedOff(id int64) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[c.owners[id]]
	return entry != nil && entry.handedOff
}

// settledState 返回只读快照，供批尾继续做身份过滤；原任务已消费，不能再次从队列加载。
func (c *wikiDocumentSettlement) settledState(id int64) (*types.WikiIngestPlanState, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[c.owners[id]]
	if entry == nil || !entry.settled {
		return nil, false
	}
	return entry.settledResult, true
}
