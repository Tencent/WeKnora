package service

import (
	"context"
	"errors"
	"time"
)

const (
	// wikiPageConflictMaxAttempts 包含首次归并，耗尽后沿用文档级持久重试。
	wikiPageConflictMaxAttempts = 3
	// wikiPageConflictRetryDelay 只用于页面写冲突，避免立即反复争抢同一页。
	wikiPageConflictRetryDelay = 100 * time.Millisecond
)

var errWikiPageSourceChanged = errors.New("wiki page sources changed before write")

// waitWikiPageConflictRetry 在两次归并之间退避；取消后不能继续调用模型。
func waitWikiPageConflictRetry(ctx context.Context, attempt int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(time.Duration(attempt) * wikiPageConflictRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// wikiPageRetryContext 保留本批提取和目录计划，但刷新归并时使用的页标题和摘要缓存。
// 新缓存只属于当前页，不能清空其他并发页面正在使用的批次缓存。
func (s *wikiIngestService) wikiPageRetryContext(kbID string, previous *WikiBatchContext) *WikiBatchContext {
	fresh := s.newWikiBatchContext(kbID, nil)
	if previous == nil {
		return fresh
	}
	// identityClaims/identityPages 含 sync.Map，不能复制已使用的批次上下文。
	fresh.ContentInstructions = previous.ContentInstructions
	fresh.ExtractionInstructions = previous.ExtractionInstructions
	fresh.ExtractionGranularity = previous.ExtractionGranularity
	fresh.PlannedFolderID = previous.PlannedFolderID
	return fresh
}

// checkWikiPageRetrySources 拦截模型调用期间发生的取消或来源删除。
// 此时整页结果已包含旧贡献，不能只删引用后继续保存；保留原任务等待重新处理。
func (s *wikiIngestService) checkWikiPageRetrySources(ctx context.Context, kbID string, updates []SlugUpdate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	live, err := s.filterLiveUpdates(ctx, kbID, updates)
	if err != nil {
		return err
	}
	if len(live) != len(updates) {
		return errWikiPageSourceChanged
	}
	return ctx.Err()
}
