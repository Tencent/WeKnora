package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const wikiProgressIngestOp = "ingest"

var _ interfaces.WikiIngestProgressRepository = (*taskPendingOpsRepository)(nil)

// orderedWikiClaims 固定加锁顺序，并拒绝重复 ID 携带不同领取信息的请求。
func orderedWikiClaims(claims []types.WikiProgressClaim) ([]types.WikiProgressClaim, error) {
	if len(claims) == 0 {
		return nil, types.ErrWikiProgressPlanMissing
	}
	byID := make(map[int64]types.WikiProgressClaim, len(claims))
	for _, claim := range claims {
		if claim.PendingOpID <= 0 || claim.Epoch <= 0 || claim.TenantID == 0 ||
			claim.KnowledgeBaseID == "" || claim.KnowledgeID == "" || claim.Op == "" {
			return nil, types.ErrWikiProgressClaimLost
		}
		if old, ok := byID[claim.PendingOpID]; ok && old != claim {
			return nil, types.ErrWikiProgressClaimLost
		}
		if claim.TenantID != claims[0].TenantID || claim.KnowledgeBaseID != claims[0].KnowledgeBaseID {
			return nil, types.ErrWikiProgressClaimLost
		}
		byID[claim.PendingOpID] = claim
	}
	ordered := make([]types.WikiProgressClaim, 0, len(byID))
	for _, claim := range byID {
		ordered = append(ordered, claim)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].PendingOpID < ordered[j].PendingOpID })
	return ordered, nil
}

// wikiProgressRowLock 在 PostgreSQL 上串行化提交；SQLite 由其写事务和单连接约束串行化。
func wikiProgressRowLock(db *gorm.DB, strength string) *gorm.DB {
	if db.Name() == "postgres" {
		return db.Clauses(clause.Locking{Strength: strength})
	}
	return db
}

// lockWikiClaim 检查不可变的队列归属和领取代次。失主不能读改旧计划，更不能清理新领取。
func lockWikiClaim(tx *gorm.DB, claim types.WikiProgressClaim) (*types.TaskPendingOp, error) {
	return readWikiClaim(wikiProgressRowLock(tx, "UPDATE"), claim)
}

// readWikiClaim 也用于归属失效后的错误分类；真正写入仍必须经过带锁的同一校验。
func readWikiClaim(tx *gorm.DB, claim types.WikiProgressClaim) (*types.TaskPendingOp, error) {
	var row types.TaskPendingOp
	err := tx.Where("id = ?", claim.PendingOpID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, types.ErrWikiProgressClaimLost
	}
	if err != nil {
		return nil, err
	}
	if row.ClaimEpoch != claim.Epoch || row.ClaimedAt == nil ||
		row.TenantID != claim.TenantID || row.TaskType != types.TypeWikiIngest ||
		row.Scope != types.TaskScopeKnowledgeBase || row.ScopeID != claim.KnowledgeBaseID ||
		row.DedupKey != claim.KnowledgeID || row.Op != claim.Op {
		return nil, types.ErrWikiProgressClaimLost
	}
	var payload struct {
		Attempt int `json:"attempt"`
	}
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		return nil, fmt.Errorf("decode wiki pending attempt: %w", err)
	}
	if payload.Attempt != claim.Attempt {
		return nil, types.ErrWikiProgressClaimLost
	}
	return &row, nil
}

// wikiProgressOwnersActive 在短事务中确认租户、知识库及 ingest 源文档仍有效。
// retract 允许源文档已删除，因为它正负责撤回该文档的旧贡献。
func wikiProgressOwnersActive(tx *gorm.DB, claims []types.WikiProgressClaim) error {
	claim := claims[0]
	for _, query := range []*gorm.DB{
		tx.Model(&types.Tenant{}).Select("id").Where("id = ?", claim.TenantID),
		tx.Model(&types.KnowledgeBase{}).Select("id").
			Where("id = ? AND tenant_id = ?", claim.KnowledgeBaseID, claim.TenantID),
	} {
		var row struct{ ID string }
		if err := wikiProgressRowLock(query, "SHARE").Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return types.ErrWikiProgressOwnerGone
			}
			return err
		}
	}
	// 同批可能包含同文档多个事件，去重后按文档 ID 加锁，避免与另一个批次形成反向锁序。
	ids := make(map[string]struct{})
	for _, claim := range claims {
		if claim.Op == wikiProgressIngestOp {
			ids[claim.KnowledgeID] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		var row struct {
			ID          string
			ParseStatus *string
		}
		query := tx.Model(&types.Knowledge{}).Select("id", "parse_status").
			Where("id = ? AND tenant_id = ? AND knowledge_base_id = ?", id, claim.TenantID, claim.KnowledgeBaseID)
		if err := wikiProgressRowLock(query, "SHARE").Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return types.ErrWikiProgressOwnerGone
			}
			return err
		}
		// 取消/删除先改变状态再清理队列；即使 scrub 失败，迟到结果也不能恢复写入。
		if row.ParseStatus != nil &&
			(*row.ParseStatus == types.ParseStatusCancelled || *row.ParseStatus == types.ParseStatusDeleting) {
			return types.ErrWikiProgressOwnerGone
		}
	}
	return nil
}

// lockWikiProgressClaims 先锁归属，再锁有序队列行；事务结束前删除/重建不能越过提交边界。
func lockWikiProgressClaims(
	tx *gorm.DB,
	claims []types.WikiProgressClaim,
	active bool,
) ([]types.WikiProgressClaim, error) {
	ordered, err := orderedWikiClaims(claims)
	if err != nil {
		return nil, err
	}
	if active {
		if err := wikiProgressOwnersActive(tx, ordered); err != nil {
			if errors.Is(err, types.ErrWikiProgressOwnerGone) {
				// 只有仍持有正确领取才能报告“归属已失效”；错误归属或旧代次仍是失主。
				// 此分支直接回滚，不获取队列锁，保持归属在前的写入锁序。
				for _, claim := range ordered {
					if _, claimErr := readWikiClaim(tx, claim); claimErr != nil {
						return nil, claimErr
					}
				}
			}
			return nil, err
		}
	}
	for _, claim := range ordered {
		if _, err := lockWikiClaim(tx, claim); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

// readWikiPlanTx 只在持有对应队列行锁时调用，因此计划和回执来自同一轮稳定状态。
func readWikiPlanTx(tx *gorm.DB, claim types.WikiProgressClaim) (*types.WikiIngestPlanState, error) {
	state := &types.WikiIngestPlanState{}
	var plan types.WikiIngestPlan
	if err := tx.Where("pending_op_id = ?", claim.PendingOpID).Take(&plan).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return state, nil
		}
		return nil, err
	}
	if plan.TenantID != claim.TenantID || plan.KnowledgeBaseID != claim.KnowledgeBaseID ||
		plan.KnowledgeID != claim.KnowledgeID || plan.Op != claim.Op || plan.Attempt != claim.Attempt {
		return nil, types.ErrWikiProgressClaimLost
	}
	state.Plan = &plan
	if err := tx.Where("pending_op_id = ?", claim.PendingOpID).Order("slug ASC").Find(&state.Pages).Error; err != nil {
		return nil, err
	}
	return state, nil
}

// wikiSourceAttemptSnapshot 在归属与队列行锁内核对源代次；不因 span 保留清理造成代次降低而拒绝恢复。
func wikiSourceAttemptSnapshot(tx *gorm.DB, claim types.WikiProgressClaim, allowUnbound bool) (int, error) {
	if claim.SourceAttempt < types.WikiSourceAttemptUnbound ||
		(claim.SourceAttempt == types.WikiSourceAttemptUnbound && !allowUnbound) {
		return 0, types.ErrWikiProgressClaimLost
	}
	if claim.Op != wikiProgressIngestOp {
		return 0, nil
	}
	var current int
	err := tx.Table("knowledge_processing_spans").Select("COALESCE(MAX(attempt), 0)").
		Where("knowledge_id = ?", claim.KnowledgeID).Scan(&current).Error
	if err != nil {
		return 0, err
	}
	if (claim.Attempt > 0 && current > claim.Attempt) ||
		(claim.SourceAttempt >= 0 && current > claim.SourceAttempt) {
		return 0, types.ErrWikiProgressOwnerGone
	}
	return current, nil
}

// readWikiPlanSnapshotTx 将计划绑定到 Map 前观察的代次，禁止失败 scrub 后重新接受新解析的旧任务。
func readWikiPlanSnapshotTx(
	tx *gorm.DB,
	claim types.WikiProgressClaim,
	allowUnbound bool,
) (*types.WikiIngestPlanState, error) {
	current, err := wikiSourceAttemptSnapshot(tx, claim, allowUnbound)
	if err != nil {
		return nil, err
	}
	state, err := readWikiPlanTx(tx, claim)
	if err != nil {
		return nil, err
	}
	if claim.Op == wikiProgressIngestOp && state.Plan != nil && current > state.Plan.SourceAttempt {
		return nil, types.ErrWikiProgressOwnerGone
	}
	state.SourceAttempt = max(current, claim.Attempt)
	return state, nil
}

func (r *taskPendingOpsRepository) LoadWikiIngestPlan(
	ctx context.Context,
	claim types.WikiProgressClaim,
) (*types.WikiIngestPlanState, error) {
	var state *types.WikiIngestPlanState
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockWikiProgressClaims(tx, []types.WikiProgressClaim{claim}, true); err != nil {
			return err
		}
		var err error
		state, err = readWikiPlanSnapshotTx(tx, claim, true)
		return err
	})
	return state, err
}

func (r *taskPendingOpsRepository) SaveWikiIngestPlan(
	ctx context.Context,
	claim types.WikiProgressClaim,
	payload json.RawMessage,
	slugs []string,
) (*types.WikiIngestPlanState, error) {
	if !json.Valid(payload) {
		return nil, errors.New("wiki progress plan must contain valid JSON")
	}
	unique := make(map[string]struct{}, len(slugs))
	for _, slug := range slugs {
		if strings.TrimSpace(slug) == "" {
			return nil, errors.New("wiki progress slug is required")
		}
		unique[slug] = struct{}{}
	}
	orderedSlugs := make([]string, 0, len(unique))
	for slug := range unique {
		orderedSlugs = append(orderedSlugs, slug)
	}
	sort.Strings(orderedSlugs)
	var state *types.WikiIngestPlanState
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockWikiProgressClaims(tx, []types.WikiProgressClaim{claim}, true); err != nil {
			return err
		}
		existing, err := readWikiPlanSnapshotTx(tx, claim, false)
		if err != nil {
			return err
		}
		if existing.Plan != nil {
			state = existing
			return nil
		}
		plan := &types.WikiIngestPlan{
			PendingOpID: claim.PendingOpID, TenantID: claim.TenantID, KnowledgeBaseID: claim.KnowledgeBaseID,
			KnowledgeID: claim.KnowledgeID, Op: claim.Op, Attempt: claim.Attempt, SourceAttempt: claim.SourceAttempt,
			Payload: append(json.RawMessage(nil), payload...), CreatedAt: time.Now(),
		}
		if err := tx.Create(plan).Error; err != nil {
			return err
		}
		for _, slug := range orderedSlugs {
			page := &types.WikiIngestPageProgress{
				PendingOpID: claim.PendingOpID, Slug: slug,
				OldOutLinks: types.StringArray{}, NewOutLinks: types.StringArray{}, UpdatedAt: time.Now(),
			}
			if err := tx.Create(page).Error; err != nil {
				return err
			}
		}
		state, err = readWikiPlanSnapshotTx(tx, claim, false)
		return err
	})
	return state, err
}

// lockWikiPageProgress 要求计划和指定页都存在；不能从一个迟到模型结果凭空创建进度。
func lockWikiPageProgress(
	tx *gorm.DB,
	commit types.WikiPageProgressCommit,
) ([]types.WikiProgressClaim, []types.WikiIngestPageProgress, error) {
	if strings.TrimSpace(commit.Slug) == "" {
		return nil, nil, types.ErrWikiProgressPlanMissing
	}
	claims, err := lockWikiProgressClaims(tx, commit.Claims, true)
	if err != nil {
		return nil, nil, err
	}
	pages := make([]types.WikiIngestPageProgress, 0, len(claims))
	for _, claim := range claims {
		current, err := wikiSourceAttemptSnapshot(tx, claim, false)
		if err != nil {
			return nil, nil, err
		}
		var plan types.WikiIngestPlan
		if err := tx.
			Select(
				"pending_op_id", "tenant_id", "knowledge_base_id", "knowledge_id", "op", "attempt", "source_attempt",
			).
			Where("pending_op_id = ?", claim.PendingOpID).Take(&plan).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil, types.ErrWikiProgressPlanMissing
			}
			return nil, nil, err
		}
		if plan.TenantID != claim.TenantID || plan.KnowledgeBaseID != claim.KnowledgeBaseID ||
			plan.KnowledgeID != claim.KnowledgeID || plan.Op != claim.Op || plan.Attempt != claim.Attempt {
			return nil, nil, types.ErrWikiProgressClaimLost
		}
		if claim.Op == wikiProgressIngestOp && current > plan.SourceAttempt {
			return nil, nil, types.ErrWikiProgressOwnerGone
		}
		var page types.WikiIngestPageProgress
		if err := wikiProgressRowLock(tx, "UPDATE").
			Where("pending_op_id = ? AND slug = ?", claim.PendingOpID, commit.Slug).Take(&page).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil, types.ErrWikiProgressPlanMissing
			}
			return nil, nil, err
		}
		pages = append(pages, page)
	}
	return claims, pages, nil
}

// markWikiPagesApplied 只在页写同一事务内使用，任何回执失败都会回滚页面和 revision。
func markWikiPagesApplied(
	tx *gorm.DB,
	claims []types.WikiProgressClaim,
	slug string,
	receipt types.WikiIngestPageProgress,
) error {
	ids := make([]int64, 0, len(claims))
	for _, claim := range claims {
		ids = append(ids, claim.PendingOpID)
	}
	oldLinks := receipt.OldOutLinks
	newLinks := receipt.NewOutLinks
	if oldLinks == nil {
		oldLinks = types.StringArray{}
	}
	if newLinks == nil {
		newLinks = types.StringArray{}
	}
	result := tx.Model(&types.WikiIngestPageProgress{}).
		Where("pending_op_id IN ? AND slug = ? AND applied = ?", ids, slug, false).
		Updates(map[string]interface{}{
			"applied": true, "effects_done": receipt.EffectsDone, "page_id": receipt.PageID,
			"page_version": receipt.PageVersion, "changed": receipt.Changed,
			"old_out_links": oldLinks, "new_out_links": newLinks, "updated_at": time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != int64(len(ids)) {
		return types.ErrWikiProgressAlreadyApplied
	}
	return nil
}

// withWikiPageProgressWrite 将主页面保存与所有贡献回执一起提交，不扩展普通页面写入的事务范围。
func (r *wikiPageRepository) withWikiPageProgressWrite(
	ctx context.Context,
	page *types.WikiPage,
	create bool,
	write func(*gorm.DB) error,
) error {
	commit := types.WikiPageProgressCommitFromContext(ctx)
	if commit == nil {
		return errors.New("wiki progress commit context is required")
	}
	if page == nil || page.Slug != commit.Slug {
		return types.ErrWikiProgressClaimLost
	}
	originalVersion := page.Version
	originalSnapshot := page.ProgressSnapshot
	originalInLinks := page.InLinks
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		claims, progress, err := lockWikiPageProgress(tx, *commit)
		if err != nil {
			return err
		}
		if page.TenantID != claims[0].TenantID || page.KnowledgeBaseID != claims[0].KnowledgeBaseID {
			return types.ErrWikiProgressClaimLost
		}
		for _, item := range progress {
			if item.Applied {
				return types.ErrWikiProgressAlreadyApplied
			}
		}
		oldLinks := types.StringArray{}
		if !create {
			var previous types.WikiPage
			err := wikiProgressRowLock(tx, "UPDATE").
				Where("id = ? AND tenant_id = ? AND knowledge_base_id = ? AND slug = ?",
					page.ID, page.TenantID, page.KnowledgeBaseID, page.Slug).Take(&previous).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWikiPageNotFound
			}
			if err != nil {
				return err
			}
			storedSnapshot, err := types.WikiPageProgressSnapshot(&previous)
			if err != nil {
				return err
			}
			if previous.Version != originalVersion || originalSnapshot == "" || storedSnapshot != originalSnapshot {
				return ErrWikiPageConflict
			}
			// 反链有独立的原子维护入口，不让主页面的旧快照覆盖它的新结果。
			page.InLinks = append(types.StringArray{}, previous.InLinks...)
			oldLinks = append(types.StringArray{}, previous.OutLinks...)
		}
		if err := write(tx); err != nil {
			return err
		}
		if err := markWikiPagesApplied(tx, claims, commit.Slug, types.WikiIngestPageProgress{
			PageID: page.ID, PageVersion: page.Version, Changed: true,
			OldOutLinks: oldLinks, NewOutLinks: page.OutLinks,
		}); err != nil {
			return err
		}
		return refreshWikiPageProgressSnapshot(tx, page)
	})
	if err != nil {
		page.Version = originalVersion
		page.ProgressSnapshot = originalSnapshot
		page.InLinks = originalInLinks
	}
	return err
}

// refreshWikiPageProgressSnapshot 从实际落库字段生成新快照，避免把 UpdateMeta 未写的正文算进来。
func refreshWikiPageProgressSnapshot(tx *gorm.DB, page *types.WikiPage) error {
	var stored types.WikiPage
	if err := tx.Unscoped().Where("id = ?", page.ID).Take(&stored).Error; err != nil {
		return err
	}
	snapshot, err := types.WikiPageProgressSnapshot(&stored)
	if err != nil {
		return err
	}
	page.ProgressSnapshot = snapshot
	return nil
}

// ReconcileWikiPageProgressInLink 在短事务内按当前出链维护一个反链，不覆盖目标的其他元数据。
func (r *wikiPageRepository) ReconcileWikiPageProgressInLink(
	ctx context.Context,
	tenantID uint64,
	kbID, sourceID, sourceSlug, targetID, targetSlug string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ids := []string{sourceID}
		if targetID != sourceID {
			ids = append(ids, targetID)
		}
		sort.Strings(ids)
		pages := make(map[string]*types.WikiPage, len(ids))
		// 固定页面 ID 锁序，使相互引用的页面和多个来源的收尾可以安全并发。
		for _, id := range ids {
			var page types.WikiPage
			err := wikiProgressRowLock(tx, "UPDATE").
				Where("id = ? AND tenant_id = ? AND knowledge_base_id = ?", id, tenantID, kbID).
				Take(&page).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWikiPageNotFound
			}
			if err != nil {
				return err
			}
			pages[id] = &page
		}
		source, target := pages[sourceID], pages[targetID]
		if source.Slug != sourceSlug || target.Slug != targetSlug {
			return ErrWikiPageNotFound
		}
		linked := false
		for _, slug := range source.OutLinks {
			linked = linked || slug == targetSlug
		}
		present := false
		for _, slug := range target.InLinks {
			present = present || slug == sourceSlug
		}
		if linked == present {
			return nil
		}
		links := make(types.StringArray, 0, len(target.InLinks)+1)
		for _, slug := range target.InLinks {
			if slug != sourceSlug {
				links = append(links, slug)
			}
		}
		if linked {
			links = append(links, sourceSlug)
		}
		result := tx.Model(&types.WikiPage{}).
			Where("id = ? AND tenant_id = ? AND knowledge_base_id = ? AND slug = ?",
				targetID, tenantID, kbID, targetSlug).
			Updates(map[string]interface{}{"in_links": links, "updated_at": time.Now()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrWikiPageNotFound
		}
		return nil
	})
}

func (r *taskPendingOpsRepository) CompleteWikiPageNoop(
	ctx context.Context,
	commit types.WikiPageProgressCommit,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		claims, pages, err := lockWikiPageProgress(tx, commit)
		if err != nil {
			return err
		}
		for _, page := range pages {
			if page.Applied {
				return types.ErrWikiProgressAlreadyApplied
			}
		}
		return markWikiPagesApplied(tx, claims, commit.Slug, types.WikiIngestPageProgress{EffectsDone: true})
	})
}

func (r *taskPendingOpsRepository) FinishWikiPageEffects(
	ctx context.Context,
	commit types.WikiPageProgressCommit,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		claims, pages, err := lockWikiPageProgress(tx, commit)
		if err != nil {
			return err
		}
		for _, page := range pages {
			if !page.Applied {
				return types.ErrWikiProgressIncomplete
			}
		}
		ids := make([]int64, 0, len(claims))
		for _, claim := range claims {
			ids = append(ids, claim.PendingOpID)
		}
		return tx.Model(&types.WikiIngestPageProgress{}).
			Where("pending_op_id IN ? AND slug = ?", ids, commit.Slug).
			Updates(map[string]interface{}{"effects_done": true, "updated_at": time.Now()}).Error
	})
}

func (r *taskPendingOpsRepository) ReleaseWikiIngestClaims(
	ctx context.Context,
	claims []types.WikiProgressClaim,
) error {
	if len(claims) == 0 {
		return nil
	}
	ordered, err := orderedWikiClaims(claims)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, claim := range ordered {
			if _, err := lockWikiClaim(tx, claim); err != nil {
				if errors.Is(err, types.ErrWikiProgressClaimLost) {
					continue
				}
				return err
			}
			if err := tx.Model(&types.TaskPendingOp{}).
				Where("id = ? AND claim_epoch = ?", claim.PendingOpID, claim.Epoch).
				Update("claimed_at", nil).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *taskPendingOpsRepository) IncrWikiIngestFailCount(
	ctx context.Context,
	claim types.WikiProgressClaim,
) (int, error) {
	var count int
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := orderedWikiClaims([]types.WikiProgressClaim{claim}); err != nil {
			return err
		}
		row, err := lockWikiClaim(tx, claim)
		if err != nil {
			return err
		}
		count = row.FailCount + 1
		return tx.Model(&types.TaskPendingOp{}).
			Where("id = ? AND claim_epoch = ?", claim.PendingOpID, claim.Epoch).Update("fail_count", count).Error
	})
	return count, err
}

func (r *taskPendingOpsRepository) SettleWikiIngestClaim(
	ctx context.Context,
	claim types.WikiProgressClaim,
	finalize,
	discard bool,
) (bool, error) {
	settled := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := orderedWikiClaims([]types.WikiProgressClaim{claim}); err != nil {
			return err
		}
		if finalize {
			// 与页面提交保持“文档 → 队列”锁序，避免结算和页写形成交叉等待。
			var owner struct{ ID string }
			err := wikiProgressRowLock(
				tx.Unscoped().Model(&types.Knowledge{}).Select("id").
					Where("id = ? AND tenant_id = ? AND knowledge_base_id = ?",
						claim.KnowledgeID, claim.TenantID, claim.KnowledgeBaseID),
				"UPDATE",
			).Take(&owner).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if _, err := lockWikiClaim(tx, claim); err != nil {
			if errors.Is(err, types.ErrWikiProgressClaimLost) {
				return nil
			}
			return err
		}
		shouldFinalize := finalize
		if finalize {
			if _, err := wikiSourceAttemptSnapshot(tx, claim, false); err != nil {
				if !errors.Is(err, types.ErrWikiProgressOwnerGone) {
					return err
				}
				// 事务外的代次检查之后仍可能开始重建；只清旧任务，不能释放新一轮计数。
				shouldFinalize = false
			}
		}
		if !discard {
			state, err := readWikiPlanTx(tx, claim)
			if err != nil {
				return err
			}
			if state.Plan == nil {
				return types.ErrWikiProgressPlanMissing
			}
			for _, page := range state.Pages {
				if !page.Applied || !page.EffectsDone {
					return types.ErrWikiProgressIncomplete
				}
			}
		}
		if shouldFinalize {
			// scope 条件随查询传入现有结算助手，避免仅凭全局文档 ID 跨归属扣计数。
			scoped := tx.Where("tenant_id = ? AND knowledge_base_id = ?", claim.TenantID, claim.KnowledgeBaseID)
			if _, err := finalizeSubtask(scoped, claim.KnowledgeID); err != nil {
				return err
			}
		}
		result := tx.Where("id = ? AND claim_epoch = ?", claim.PendingOpID, claim.Epoch).Delete(&types.TaskPendingOp{})
		if result.Error != nil {
			return result.Error
		}
		settled = result.RowsAffected == 1
		return nil
	})
	if err != nil {
		return false, err
	}
	return settled, nil
}
