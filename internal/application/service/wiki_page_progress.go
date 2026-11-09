package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var _ interfaces.WikiPageProgressFinisher = (*wikiPageService)(nil)

// ReconcileAppliedPage 只恢复正文落库之后的反链维护；进度标记由持有领取代次的编排层提交。
// 每个目标只原子维护本来源的反链，本方法不提供全图的跨行事务保证。
func (s *wikiPageService) ReconcileAppliedPage(
	ctx context.Context, tenantID uint64, kbID string, receipt types.WikiIngestPageProgress,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !receipt.Applied {
		return types.ErrWikiProgressIncomplete
	}
	if receipt.EffectsDone || !receipt.Changed {
		return nil
	}
	if receipt.PageID == "" || receipt.Slug == "" {
		return fmt.Errorf("wiki page progress receipt has no page identity")
	}

	// 目标页的元数据写入不能继承源页面的原子提交要求。
	ctx = types.WithoutWikiPageProgressCommit(ctx)
	page, err := s.repo.GetBySlug(ctx, kbID, receipt.Slug)
	if errors.Is(err, repository.ErrWikiPageNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get applied wiki page: %w", err)
	}
	// 删除后同 slug 的新页面属于另一次生成，不能借旧回执恢复其正文或关系。
	if page == nil || page.ID != receipt.PageID || page.TenantID != tenantID || page.KnowledgeBaseID != kbID {
		return nil
	}

	// 只处理这次写入曾涉及的目标。后来版本新增加的其他链接由它自己的收尾负责。
	targets := make([]string, 0, len(receipt.OldOutLinks)+len(receipt.NewOutLinks))
	seen := make(map[string]struct{}, cap(targets))
	for _, links := range []types.StringArray{receipt.OldOutLinks, receipt.NewOutLinks} {
		for _, slug := range links {
			if slug == "" {
				continue
			}
			if _, exists := seen[slug]; exists {
				continue
			}
			seen[slug] = struct{}{}
			targets = append(targets, slug)
		}
	}

	// 读取只用于确认目标身份，不能据此整页回写。仓储在同一目标事务中重检身份及当前源出链，
	// 避免覆盖并发来源、出链或其他元数据；中途失败仍由同一回执继续恢复。
	for _, targetSlug := range targets {
		if err := ctx.Err(); err != nil {
			return err
		}
		target, err := s.repo.GetBySlug(ctx, kbID, targetSlug)
		if errors.Is(err, repository.ErrWikiPageNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("get wiki link target %s: %w", targetSlug, err)
		}
		if target == nil || target.TenantID != tenantID || target.KnowledgeBaseID != kbID {
			return fmt.Errorf("wiki link target %s does not belong to the applied page scope", targetSlug)
		}
		err = s.repo.ReconcileWikiPageProgressInLink(
			ctx, tenantID, kbID, receipt.PageID, receipt.Slug, target.ID, targetSlug,
		)
		if err != nil && !errors.Is(err, repository.ErrWikiPageNotFound) {
			return fmt.Errorf("reconcile wiki link target %s: %w", targetSlug, err)
		}
	}
	return nil
}
