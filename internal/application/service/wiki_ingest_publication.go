package service

import (
	"context"
	"errors"
	"fmt"
	"sync"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

// wikiProgressPublisher 在一次领取内共享发布结果；持久回执的版本约束负责跨领取恢复时的保护。
// key 包含贡献版本，不能用永久 PageID 黑名单阻止同页后续合法贡献发布。
type wikiProgressPublisher struct {
	mu      sync.Mutex
	results map[types.WikiPagePublication]*wikiPublicationResult
}

type wikiPublicationResult struct {
	done chan struct{}
	err  error
}

// publish 合并同一目标的并发调用，数据库操作期间不持锁；失败目标允许后续重新尝试。
func (p *wikiProgressPublisher) publish(
	ctx context.Context, s *wikiIngestService, target types.WikiPagePublication,
) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	if existing := p.results[target]; existing != nil {
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-existing.done:
			return existing.err
		}
	}
	if p.results == nil {
		p.results = make(map[types.WikiPagePublication]*wikiPublicationResult)
	}
	result := &wikiPublicationResult{done: make(chan struct{})}
	p.results[target] = result
	p.mu.Unlock()
	// 异常退出也要唤醒同页等待者，不能留下永远无法完成的发布标记。
	err = errors.New("wiki publication interrupted")
	defer func() {
		p.mu.Lock()
		result.err = err
		if err != nil {
			delete(p.results, target)
		}
		close(result.done)
		p.mu.Unlock()
	}()
	err = s.publishReceiptPage(ctx, target)
	return err
}

// wikiPublicationFromReceipt 只接受已完成副作用的写入回执；缺失版本不得降级为按 ID 发布。
func wikiPublicationFromReceipt(
	claim types.WikiProgressClaim, receipt types.WikiIngestPageProgress,
) (types.WikiPagePublication, error) {
	if !receipt.Applied || !receipt.EffectsDone || !receipt.Changed ||
		receipt.PageID == "" || receipt.Slug == "" || receipt.PageVersion <= 0 {
		return types.WikiPagePublication{}, types.ErrWikiProgressIncomplete
	}
	return types.WikiPagePublication{
		TenantID: claim.TenantID, KnowledgeBaseID: claim.KnowledgeBaseID,
		PageID: receipt.PageID, Slug: receipt.Slug, PageVersion: receipt.PageVersion,
	}, nil
}

// publishReceiptPage 不借用读取到的新版本。最终 UPDATE 仍须匹配原回执版本，封住读写之间的编辑窗口。
func (s *wikiIngestService) publishReceiptPage(ctx context.Context, target types.WikiPagePublication) error {
	page, err := s.wikiService.GetPageBySlug(ctx, target.KnowledgeBaseID, target.Slug)
	if errors.Is(err, apprepo.ErrWikiPageNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if page == nil || page.ID != target.PageID || page.TenantID != target.TenantID ||
		page.KnowledgeBaseID != target.KnowledgeBaseID || page.Version != target.PageVersion ||
		page.Status != types.WikiPageStatusDraft {
		return nil
	}
	return s.wikiService.PublishDraftPage(types.WithWikiPagePublication(ctx, target),
		target.TenantID, target.KnowledgeBaseID, target.PageID, target.Slug)
}

// publishUnsettledPages 与文档提前结算共用发布协调器；失败文档只能发布自己已完成贡献的版本。
func (b *wikiProgressBatch) publishUnsettledPages(
	ctx context.Context, s *wikiIngestService, slugs []string,
) error {
	allowed := make(map[string]bool, len(slugs))
	for _, slug := range slugs {
		allowed[slug] = true
	}
	var errs []error
	for _, op := range b.canonical {
		if b.settlement.isSettled(op.dbID) {
			continue
		}
		claim := b.claims[op.dbID]
		state, err := b.repo.LoadWikiIngestPlan(ctx, claim)
		if err != nil {
			return err
		}
		if state == nil || state.Plan == nil {
			return types.ErrWikiProgressPlanMissing
		}
		for _, receipt := range state.Pages {
			if !allowed[receipt.Slug] || !receipt.Applied || !receipt.EffectsDone || !receipt.Changed {
				continue
			}
			target, err := wikiPublicationFromReceipt(claim, receipt)
			if err != nil {
				return err
			}
			if err := b.publications.publish(ctx, s, target); err != nil {
				errs = append(errs, fmt.Errorf("publish checkpointed wiki page %s: %w", receipt.Slug, err))
			}
		}
	}
	return errors.Join(errs...)
}
