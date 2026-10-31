package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// 计划格式单独版本化；未知版本必须保留队列，不能重新生成并覆盖已完成的贡献。
const wikiIngestPlanVersion = 1

// wikiDurablePlan 只保存可恢复的数据，不序列化进程内的 span、锁或缓存。
type wikiDurablePlan struct {
	Version        int
	Document       *wikiDurableDocument
	Updates        []SlugUpdate
	Folders        []string
	PlannedFolders map[string]string
}

type wikiDurableDocument struct {
	KnowledgeID string
	DocTitle    string
	Summary     string
	Pages       []wikiIngestPageRef
	MapStats    types.JSONMap
}

// wikiProgressBatch 仅属于本轮领取，不放进跨批共享的 service 状态。
type wikiProgressBatch struct {
	repo      interfaces.WikiIngestProgressRepository
	claims    map[int64]types.WikiProgressClaim
	canonical map[string]WikiPendingOp
	states    map[int64]*types.WikiIngestPlanState
}

// claimWikiProgressBatch 在 Lite 模式也使用持久领取，旧进程晚返回时由代次拒绝写入。
func (s *wikiIngestService) claimWikiProgressBatch(
	ctx context.Context,
	payload WikiIngestPayload,
	limit int,
) ([]WikiPendingOp, []int64, *wikiProgressBatch, error) {
	if limit <= 0 {
		limit = wikiMaxDocsPerBatch
	}
	rows, err := s.pendingRepo.ClaimBatch(
		ctx, wikiTaskType, wikiTaskScope, payload.KnowledgeBaseID, limit,
		time.Now().Add(-wikiClaimStaleAfter),
	)
	if err != nil {
		return nil, nil, nil, err
	}
	b := &wikiProgressBatch{
		repo:      s.progressRepo,
		claims:    make(map[int64]types.WikiProgressClaim),
		canonical: make(map[string]WikiPendingOp),
		states:    make(map[int64]*types.WikiIngestPlanState),
	}
	ops, ids := s.decodePendingRows(ctx, rows)
	for _, r := range rows {
		var op WikiPendingOp
		_ = json.Unmarshal(r.Payload, &op)
		b.claims[r.ID] = types.WikiProgressClaim{
			PendingOpID:     r.ID,
			SourceAttempt:   types.WikiSourceAttemptUnbound,
			Epoch:           r.ClaimEpoch,
			TenantID:        r.TenantID,
			KnowledgeBaseID: r.ScopeID,
			KnowledgeID:     r.DedupKey,
			Op:              r.Op,
			Attempt:         op.Attempt,
		}
	}
	validOps := make([]WikiPendingOp, 0, len(ops))
	for _, op := range ops {
		b.canonical[op.KnowledgeID] = op
		state, loadErr := b.repo.LoadWikiIngestPlan(ctx, b.claims[op.dbID])
		if errors.Is(loadErr, types.ErrWikiProgressOwnerGone) {
			// 删除/移走遗留的队列不能拖住同批有效文档；不释放新归属的计数。
			var discardErrors []error
			// 去重只保留最后一条执行，但失效源的同批旧行也必须清理，不能留下 fresh claim。
			for id, claim := range b.claims {
				if claim.KnowledgeID != op.KnowledgeID {
					continue
				}
				if _, err := b.repo.SettleWikiIngestClaim(ctx, claim, false, true); err != nil {
					discardErrors = append(discardErrors, err)
				} else {
					delete(b.claims, id)
				}
			}
			if len(discardErrors) == 0 {
				delete(b.canonical, op.KnowledgeID)
				continue
			}
			loadErr = errors.Join(discardErrors...)
		}
		if errors.Is(loadErr, types.ErrWikiProgressClaimLost) {
			delete(b.claims, op.dbID)
			delete(b.canonical, op.KnowledgeID)
			continue
		}
		if loadErr != nil {
			releaseCtx, cancel := wikiIngestCleanupContext(ctx)
			releaseErr := b.release(releaseCtx)
			cancel()
			return nil, nil, nil, errors.Join(loadErr, releaseErr)
		}
		claim := b.claims[op.dbID]
		claim.SourceAttempt = state.SourceAttempt
		if state.Plan != nil {
			claim.SourceAttempt = state.Plan.SourceAttempt
		}
		b.claims[op.dbID] = claim
		b.states[op.dbID] = state
		validOps = append(validOps, op)
	}
	if len(validOps) == 0 {
		// 外层空批次会在安装异常退出 defer 前返回，必须在这里释放仍归本轮的其余行。
		releaseCtx, cancel := wikiIngestCleanupContext(ctx)
		err := b.release(releaseCtx)
		cancel()
		if err != nil {
			return nil, nil, nil, err
		}
	}
	remainingIDs := ids[:0]
	for _, id := range ids {
		if _, ok := b.claims[id]; ok {
			remainingIDs = append(remainingIDs, id)
		}
	}
	return validOps, remainingIDs, b, nil
}

func (b *wikiProgressBatch) release(ctx context.Context) error {
	claims := make([]types.WikiProgressClaim, 0, len(b.claims))
	for _, c := range b.claims {
		claims = append(claims, c)
	}
	return b.repo.ReleaseWikiIngestClaims(ctx, claims)
}

func (b *wikiProgressBatch) restored(op WikiPendingOp) bool {
	state := b.states[op.dbID]
	return state != nil && state.Plan != nil
}

func decodeWikiDurablePlan(state *types.WikiIngestPlanState) (*wikiDurablePlan, error) {
	if state == nil || state.Plan == nil {
		return nil, types.ErrWikiProgressPlanMissing
	}
	var plan wikiDurablePlan
	if err := json.Unmarshal(state.Plan.Payload, &plan); err != nil {
		return nil, fmt.Errorf("decode wiki plan: %w", err)
	}
	if plan.Version != wikiIngestPlanVersion {
		return nil, fmt.Errorf("unsupported wiki plan version %d", plan.Version)
	}
	return &plan, nil
}

// prepare 冻结 Map/目录结果，然后只把未应用的贡献交给 Reduce；已完成页仍参与批次收尾。
func (b *wikiProgressBatch) prepare(
	ctx context.Context,
	s *wikiIngestService,
	ops,
	failed []WikiPendingOp,
	docs []*docIngestResult,
	updates map[string][]SlugUpdate,
	folders []string,
	batchCtx *WikiBatchContext,
) (map[string][]SlugUpdate, []*docIngestResult, []string, error) {
	if batchCtx.PlannedFolderID == nil {
		batchCtx.PlannedFolderID = make(map[string]string)
	}
	failedIDs := make(map[int64]bool, len(failed))
	for _, op := range failed {
		failedIDs[op.dbID] = true
	}
	docByID := make(map[string]*docIngestResult, len(docs))
	for _, doc := range docs {
		if doc != nil {
			docByID[doc.KnowledgeID] = doc
		}
	}
	pending := make(map[string][]SlugUpdate)
	var recoveredDocs []*docIngestResult
	affectedSet := make(map[string]bool)
	for _, op := range ops {
		if failedIDs[op.dbID] {
			continue
		}
		claim := b.claims[op.dbID]
		state := b.states[op.dbID]
		if state == nil || state.Plan == nil {
			plan := wikiDurablePlan{
				Version:        wikiIngestPlanVersion,
				Folders:        folders,
				PlannedFolders: make(map[string]string),
			}
			if doc := docByID[op.KnowledgeID]; doc != nil {
				plan.Document = &wikiDurableDocument{
					KnowledgeID: doc.KnowledgeID,
					DocTitle:    doc.DocTitle,
					Summary:     doc.Summary,
					Pages:       doc.Pages,
					MapStats:    doc.MapStats,
				}
			}
			slugs := make([]string, 0)
			for slug, group := range updates {
				included := false
				for _, u := range group {
					if u.KnowledgeID == op.KnowledgeID {
						plan.Updates = append(plan.Updates, u)
						included = true
					}
				}
				if included {
					slugs = append(slugs, slug)
					plan.PlannedFolders[slug] = batchCtx.PlannedFolderID[slug]
				}
			}
			sort.Strings(slugs)
			if plan.Document != nil {
				// 文档索引也冻结规范化之后的 slug，避免恢复时仍指向 Map 的临时拼写。
				refs := make(map[string]string)
				for _, ref := range plan.Document.Pages {
					refs[ref.Slug] = ref.Title
				}
				for _, u := range plan.Updates {
					if _, exists := refs[u.Slug]; exists {
						continue
					}
					switch u.Type {
					case types.WikiPageTypeEntity, types.WikiPageTypeConcept:
						refs[u.Slug] = u.Item.Name
					case "summary":
						refs[u.Slug] = u.DocTitle
					}
				}
				plan.Document.Pages = nil
				for _, slug := range slugs {
					if title, ok := refs[slug]; ok {
						plan.Document.Pages = append(plan.Document.Pages, wikiIngestPageRef{Slug: slug, Title: title})
					}
				}
			}
			raw, err := json.Marshal(plan)
			if err != nil {
				return nil, nil, nil, err
			}
			state, err = b.repo.SaveWikiIngestPlan(ctx, claim, raw, slugs)
			if err != nil {
				return nil, nil, nil, err
			}
			b.states[op.dbID] = state
		}
		plan, err := decodeWikiDurablePlan(state)
		if err != nil {
			return nil, nil, nil, err
		}
		if plan.Document != nil {
			d := plan.Document
			recovered := &docIngestResult{
				KnowledgeID: d.KnowledgeID,
				DocTitle:    d.DocTitle,
				Summary:     d.Summary,
				Pages:       d.Pages,
				MapStats:    d.MapStats,
			}
			if fresh := docByID[d.KnowledgeID]; fresh != nil {
				recovered.WikiSpan = fresh.WikiSpan
			}
			recoveredDocs = append(recoveredDocs, recovered)
		}
		progress := make(map[string]types.WikiIngestPageProgress, len(state.Pages))
		for _, p := range state.Pages {
			progress[p.Slug] = p
			if p.Applied {
				if err := b.finish(ctx, s, claim, p); err != nil {
					return nil, nil, nil, err
				}
				if p.Changed {
					affectedSet[p.Slug] = true
				}
			}
		}
		for _, u := range plan.Updates {
			p, ok := progress[u.Slug]
			if !ok {
				return nil, nil, nil, fmt.Errorf("wiki plan missing page progress: %s", u.Slug)
			}
			if !p.Applied {
				pending[u.Slug] = append(pending[u.Slug], u)
			}
		}
		for slug, folder := range plan.PlannedFolders {
			batchCtx.PlannedFolderID[slug] = folder
		}
	}
	affected := make([]string, 0, len(affectedSet))
	for slug := range affectedSet {
		affected = append(affected, slug)
	}
	sort.Strings(affected)
	return pending, recoveredDocs, affected, nil
}

// finish 只补已提交页面的收尾，不再次调用模型或写正文；失败保留 Applied 回执。
func (b *wikiProgressBatch) finish(
	ctx context.Context,
	s *wikiIngestService,
	claim types.WikiProgressClaim,
	p types.WikiIngestPageProgress,
) error {
	if p.EffectsDone {
		return nil
	}
	if p.Changed {
		finisher, ok := s.wikiService.(interfaces.WikiPageProgressFinisher)
		if !ok {
			return errors.New("wiki page service cannot resume applied page effects")
		}
		if err := finisher.ReconcileAppliedPage(
			types.WithoutWikiPageProgressCommit(ctx), claim.TenantID, claim.KnowledgeBaseID, p,
		); err != nil {
			return err
		}
	}
	return b.repo.FinishWikiPageEffects(ctx, types.WikiPageProgressCommit{
		Claims: []types.WikiProgressClaim{claim}, Slug: p.Slug,
	})
}

// reduce 将页面贡献与主页面写入绑定。模型失败、取消和延后写入都不能转成成功回执。
func (b *wikiProgressBatch) reduce(
	ctx context.Context,
	s *wikiIngestService,
	slug string,
	updates []SlugUpdate,
	call func(context.Context, []SlugUpdate) (bool, string, bool, bool, error),
) (bool, string, bool, bool, error) {
	commit := types.WikiPageProgressCommit{Slug: slug}
	seen := make(map[int64]bool)
	for _, u := range updates {
		op, ok := b.canonical[u.KnowledgeID]
		if !ok {
			return false, "", false, false, types.ErrWikiProgressPlanMissing
		}
		if !seen[op.dbID] {
			commit.Claims = append(commit.Claims, b.claims[op.dbID])
			seen[op.dbID] = true
		}
	}
	if len(commit.Claims) == 0 {
		return false, "", false, false, types.ErrWikiProgressPlanMissing
	}
	changed, kind, additionFailed, deferred, err := call(types.WithWikiPageProgressCommit(ctx, &commit), updates)
	if err != nil || deferred || additionFailed {
		return changed, kind, additionFailed, deferred, err
	}
	if !changed {
		if err = b.repo.CompleteWikiPageNoop(ctx, commit); err != nil {
			return false, kind, false, false, err
		}
	}
	for _, claim := range commit.Claims {
		state, loadErr := b.repo.LoadWikiIngestPlan(ctx, claim)
		if loadErr != nil {
			return changed, kind, false, false, loadErr
		}
		found := false
		for _, p := range state.Pages {
			if p.Slug == slug {
				if !p.Applied {
					return changed, kind, false, false, types.ErrWikiProgressIncomplete
				}
				if err = b.finish(ctx, s, claim, p); err != nil {
					return changed, kind, false, false, err
				}
				found = true
				break
			}
		}
		if !found {
			return changed, kind, false, false, types.ErrWikiProgressPlanMissing
		}
	}
	return changed, kind, false, false, nil
}

// settle 在原批次结尾消费任务，计数释放与删除队列同事务，避免崩溃后重复扣减。
func (b *wikiProgressBatch) settle(ctx context.Context, s *wikiIngestService, ids []int64) error {
	var errs []error
	for _, id := range ids {
		claim := b.claims[id]
		op, canonical := b.canonical[claim.KnowledgeID]
		canonical = canonical && op.dbID == id
		finalize := canonical && claim.Op == WikiOpIngest &&
			!attemptSuperseded(ctx, s.tracker(), claim.KnowledgeID, claim.Attempt)
		_, err := b.repo.SettleWikiIngestClaim(ctx, claim, finalize, !canonical)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// retry 保留已落库计划，失败预算仍按原文档任务计算；失主的 worker 不再动新领取。
func (b *wikiProgressBatch) retry(
	ctx context.Context,
	s *wikiIngestService,
	payload WikiIngestPayload,
	ops []WikiPendingOp,
) error {
	var errs []error
	for _, op := range ops {
		claim := b.claims[op.dbID]
		count, err := b.repo.IncrWikiIngestFailCount(ctx, claim)
		if errors.Is(err, types.ErrWikiProgressClaimLost) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if count <= wikiMaxFailRetries {
			errs = append(errs, b.repo.ReleaseWikiIngestClaims(ctx, []types.WikiProgressClaim{claim}))
			continue
		}
		if s.deadLetterRepo != nil {
			raw, marshalErr := json.Marshal(op)
			if marshalErr != nil {
				errs = append(errs, marshalErr)
				continue
			}
			if err = s.deadLetterRepo.Insert(ctx, &types.TaskDeadLetter{
				TenantID:  payload.TenantID,
				TaskType:  wikiTaskType,
				Scope:     wikiTaskScope,
				ScopeID:   payload.KnowledgeBaseID,
				RelatedID: op.KnowledgeID,
				Payload:   raw,
				LastError: fmt.Sprintf("exceeded wikiMaxFailRetries=%d (in-batch retries)", wikiMaxFailRetries),
				FailCount: count,
			}); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		finalize := op.Op == WikiOpIngest && !attemptSuperseded(ctx, s.tracker(), op.KnowledgeID, op.Attempt)
		_, err = b.repo.SettleWikiIngestClaim(ctx, claim, finalize, true)
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// affectedIdentities 只允许本回执写入的页面参与发布，避免同 slug 删除重建后误发布新草稿。
func (b *wikiProgressBatch) affectedIdentities(
	ctx context.Context,
	s *wikiIngestService,
	slugs []string,
) ([]string, map[string]string, error) {
	expected := make(map[string]map[string]bool)
	for _, op := range b.canonical {
		state, err := b.repo.LoadWikiIngestPlan(ctx, b.claims[op.dbID])
		if err != nil {
			return nil, nil, err
		}
		for _, page := range state.Pages {
			if !page.Applied || !page.Changed {
				continue
			}
			if expected[page.Slug] == nil {
				expected[page.Slug] = make(map[string]bool)
			}
			expected[page.Slug][page.PageID] = true
		}
	}
	ids := make(map[string]string)
	var affected []string
	for _, slug := range slugs {
		if ids[slug] != "" {
			continue
		}
		page, err := s.wikiService.GetPageBySlug(ctx, b.scopeID(), slug)
		if errors.Is(err, apprepo.ErrWikiPageNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if page != nil && expected[slug][page.ID] {
			ids[slug] = page.ID
			affected = append(affected, slug)
		}
	}
	return affected, ids, nil
}

func (b *wikiProgressBatch) scopeID() string {
	for _, claim := range b.claims {
		return claim.KnowledgeBaseID
	}
	return ""
}

// seedIdentities 让本批新文档沿用冻结计划的规范 slug，不把重启后的同名实体拆成两页。
// 已冻结的不同计划保持原值，不能为去重重写已经应用的贡献。
func (b *wikiProgressBatch) seedIdentities(batchCtx *WikiBatchContext) error {
	batchCtx.frozenIdentityClaims = make(map[string]string)
	ids := make([]int64, 0, len(b.states))
	for id := range b.states {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		state := b.states[id]
		if state == nil || state.Plan == nil {
			continue
		}
		plan, err := decodeWikiDurablePlan(state)
		if err != nil {
			return err
		}
		for _, u := range plan.Updates {
			if u.Type != types.WikiPageTypeEntity && u.Type != types.WikiPageTypeConcept {
				continue
			}
			identity := normalizeWikiIdentityTitle(u.Item.Name)
			if identity == "" {
				continue
			}
			key := u.Type + "\x00" + identity
			if batchCtx.frozenIdentityClaims[key] == "" {
				batchCtx.frozenIdentityClaims[key] = u.Slug
			}
		}
	}
	return nil
}
