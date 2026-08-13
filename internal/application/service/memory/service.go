package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// ErrMemoryDisabled is returned by write operations when memory is off at the
// workspace or user level.
var ErrMemoryDisabled = errors.New("memory: disabled for this scope")

// ErrItemNotFound is returned when an item id does not exist in the caller's
// own memory space. Scope mismatch and genuine absence deliberately produce
// the same error so an id cannot be probed for existence across users.
var ErrItemNotFound = errors.New("memory: item not found")

// ErrPreviouslyForgotten means the statement matches one the user deleted.
// Callers on the write path treat it as "nothing to do", not as a failure.
var ErrPreviouslyForgotten = errors.New("memory: previously forgotten by the user")

// ErrSensitiveContent means the statement was almost entirely credentials or
// identity numbers, so redacting it left nothing worth remembering.
var ErrSensitiveContent = errors.New("memory: statement was sensitive material")

// rejectedMessageWindow is how long a rejected message keeps blocking
// re-derivation. The case this closes is the debounced run that reads the same
// message minutes after the user deleted what it produced; past that, whatever
// the user said is treated fresh again.
const rejectedMessageWindow = time.Hour

// Service implements interfaces.MemoryService.
type Service struct {
	repo         interfaces.MemoryRepository
	tenantRepo   interfaces.TenantRepository
	messageRepo  interfaces.MessageRepository
	modelService interfaces.ModelService
	enqueuer     interfaces.TaskEnqueuer
	config       *config.Config
}

// NewMemoryService builds the long-term memory service.
func NewMemoryService(
	repo interfaces.MemoryRepository,
	tenantRepo interfaces.TenantRepository,
	messageRepo interfaces.MessageRepository,
	modelService interfaces.ModelService,
	enqueuer interfaces.TaskEnqueuer,
	cfg *config.Config,
) interfaces.MemoryService {
	return &Service{
		repo:         repo,
		tenantRepo:   tenantRepo,
		messageRepo:  messageRepo,
		modelService: modelService,
		enqueuer:     enqueuer,
		config:       cfg,
	}
}

// workspaceConfig loads the workspace memory switch. A missing tenant or an
// unset column yields a zero-value config, which is disabled.
func (s *Service) workspaceConfig(ctx context.Context, tenantID uint64) *types.MemoryConfig {
	tenant, err := s.tenantRepo.GetTenantByID(ctx, tenantID)
	if err != nil || tenant == nil || tenant.MemoryConfig == nil {
		return &types.MemoryConfig{}
	}
	cfg := *tenant.MemoryConfig
	cfg.Normalize()
	return &cfg
}

// enabledScope resolves the scope and checks every level of the switch. The
// second return value is false whenever memory must not be used, and callers
// on the read path treat that as "no memory" rather than as a failure.
func (s *Service) enabledScope(ctx context.Context) (interfaces.MemoryScope, *types.MemoryConfig, bool) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return scope, nil, false
	}
	cfg := s.workspaceConfig(ctx, scope.TenantID)
	if !cfg.MemoryEnabled() {
		return scope, cfg, false
	}
	if !types.MemoryAllowedForAgent(ctx) {
		return scope, cfg, false
	}
	subject, err := s.repo.GetSubject(ctx, scope)
	if err != nil {
		logger.Warnf(ctx, "memory: load subject failed: %v", err)
		return scope, cfg, false
	}
	// A subject row is created on first write. Its absence means the user has
	// nothing stored yet, which is still "enabled" for the write path.
	if subject != nil && !subject.Enabled {
		return scope, cfg, false
	}
	return scope, cfg, true
}

// Recall assembles the memory to inject for one turn. It never calls a model
// and never returns an error: memory is an enhancement, so any failure has to
// degrade into an ordinary answer rather than into a failed request.
func (s *Service) Recall(ctx context.Context, query string) interfaces.MemoryRecall {
	recallCtx, recallSpan := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name: "memory.recall",
		Input: map[string]interface{}{
			"query": langfuse.TruncateRunes(query, recallQueryPreviewRunes),
		},
	})

	scope, cfg, ok := s.enabledScope(recallCtx)
	if !ok {
		reason := s.scopeDisableReason(recallCtx)
		logger.Infof(recallCtx, "memory: recall skipped (%s)", reason)
		recallSpan.Finish(langfuse.SummarizeMemoryRecallOutput(map[string]interface{}{
			"outcome": "disabled",
			"reason":  reason,
		}, nil), nil, nil)
		return interfaces.MemoryRecall{}
	}

	subject, err := s.repo.GetSubject(recallCtx, scope)
	if err != nil || subject == nil {
		reason := "no_subject"
		if err != nil {
			reason = "subject_load_failed"
			logger.Warnf(recallCtx, "memory: load subject for recall failed: %v", err)
		}
		logger.Infof(recallCtx, "memory: recall skipped (%s)", reason)
		recallSpan.Finish(langfuse.SummarizeMemoryRecallOutput(map[string]interface{}{
			"outcome":    "empty",
			"reason":     reason,
			"subject_id": scope.SubjectID,
		}, nil), map[string]interface{}{
			"tenant_id": scope.TenantID,
		}, nil)
		return interfaces.MemoryRecall{}
	}

	residentItems, err := s.repo.ListActiveResident(recallCtx, scope, 60)
	if err != nil {
		logger.Warnf(recallCtx, "memory: load resident items failed: %v", err)
		residentItems = nil
	}
	standing, interests := splitResidentInterests(residentItems)
	selectedInterests, relevantInterests := selectResidentInterests(
		query, interests, types.MemoryResidentInterestMaxItems)
	blockItems := append(append([]*types.MemoryItem(nil), standing...), selectedInterests...)

	// Render from the items rather than from subject.BlockText. The cached
	// block saves nothing here — the items were just loaded either way — and
	// trusting it means any change that alters what belongs in the block
	// (a write that failed, a new resident kind) stays invisible until the
	// user's next write. The cache is only a fallback for a failed load.
	block := types.RenderMemoryBlock(blockItems)
	if block == "" {
		block = subject.BlockText
	}

	situational, err := s.repo.ListActiveByKinds(recallCtx, scope,
		[]string{types.MemoryKindFact, types.MemoryKindTask}, 400)
	if err != nil {
		logger.Warnf(recallCtx, "memory: load situational items failed: %v", err)
		situational = nil
	}
	// Resident items are already in the block; matching them again would print
	// them twice.
	resident := make(map[string]struct{}, len(residentItems))
	for _, item := range residentItems {
		resident[item.ID] = struct{}{}
	}
	candidates := situational[:0:0]
	for _, item := range situational {
		if _, ok := resident[item.ID]; !ok {
			candidates = append(candidates, item)
		}
	}

	logger.Infof(recallCtx,
		"memory: recall start subject=%s resident=%d candidates=%d block_runes=%d",
		scope.SubjectID, len(residentItems), len(candidates), len([]rune(block)))

	matched, rankTrace := s.selectRecallWithTrace(recallCtx, scope, cfg, query, candidates)

	prompt := types.WrapMemoryForPrompt(block, types.RenderMemoryRecall(matched))
	if prompt == "" {
		emptyMeta := s.recallEmptyMeta(scope, len(residentItems), len(candidates), rankTrace)
		emptyMeta["block_runes"] = len([]rune(block))
		logger.Infof(recallCtx,
			"memory: recall empty subject=%s resident=%d candidates=%d mode=%s",
			scope.SubjectID, len(residentItems), len(candidates), rankTrace.Mode)
		recallSpan.Finish(langfuse.SummarizeMemoryRecallOutput(emptyMeta, nil), map[string]interface{}{
			"tenant_id": scope.TenantID,
		}, nil)
		return interfaces.MemoryRecall{}
	}

	// What was injected and what is reported are deliberately not the same set.
	// An interest that rode along because the cap left room is standing
	// background, not something this question pulled in, and reporting it would
	// put a memory unrelated to the answer on the chat timeline every turn.
	//
	// The block is also rendered from a truncated list, so report the items
	// that actually fit rather than everything that was loaded.
	used := residentItemsWithinBlock(standing, block)
	used = append(used, residentItemsWithinBlock(relevantInterests, block)...)
	used = append(used, matched...)
	s.touchAsync(recallCtx, scope, used)

	logger.Infof(recallCtx,
		"memory: recall done subject=%s used=%d matched=%d interest_injected=%d interest_relevant=%d mode=%s prompt_runes=%d",
		scope.SubjectID, len(used), len(matched), len(selectedInterests), len(relevantInterests),
		rankTrace.Mode, len([]rune(prompt)))
	recallSpan.Finish(langfuse.SummarizeMemoryRecallOutput(map[string]interface{}{
		"outcome":           "ok",
		"subject_id":        scope.SubjectID,
		"resident_count":    len(residentItems),
		"block_runes":       len([]rune(block)),
		"candidate_count":   len(candidates),
		"lexical_hits":      rankTrace.LexicalHits,
		"vector_hits":       rankTrace.VectorHits,
		"vector_skip":       rankTrace.VectorSkipReason,
		"ranking_mode":      rankTrace.Mode,
		"fused_candidates":  rankTrace.FusedCandidates,
		"matched_count":     len(matched),
		"interest_total":    len(interests),
		"interest_injected": len(selectedInterests),
		"interest_relevant": len(relevantInterests),
		"used_count":        len(used),
		"prompt_runes":      len([]rune(prompt)),
	}, used), map[string]interface{}{
		"tenant_id": scope.TenantID,
	}, nil)

	return interfaces.MemoryRecall{Prompt: prompt, Items: used}
}

// residentItemsWithinBlock filters to the items whose content survived the
// block's rune budget.
func residentItemsWithinBlock(items []*types.MemoryItem, block string) []*types.MemoryItem {
	if block == "" {
		return nil
	}
	within := make([]*types.MemoryItem, 0, len(items))
	for _, item := range items {
		if item != nil && strings.Contains(block, types.SanitizeMemoryContent(item.Content)) {
			within = append(within, item)
		}
	}
	return within
}

// touchAsync records usage without adding a write to the request's critical
// path. WithoutCancel keeps it alive after the HTTP handler returns.
func (s *Service) touchAsync(ctx context.Context, scope interfaces.MemoryScope, items []*types.MemoryItem) {
	if len(items) == 0 {
		return
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	bgCtx := context.WithoutCancel(ctx)
	go func() {
		if err := s.repo.TouchUsed(bgCtx, scope, ids); err != nil {
			logger.Warnf(bgCtx, "memory: touch used failed: %v", err)
		}
	}()
}

// Remember stores one statement, resolving any contradiction with what is
// already known about the same topic.
func (s *Service) Remember(ctx context.Context, item types.MemoryItem) (*types.MemoryItem, error) {
	scope, cfg, ok := s.enabledScope(ctx)
	if !ok {
		return nil, ErrMemoryDisabled
	}
	return s.write(ctx, scope, cfg, item)
}

// write is the single insertion path. Both the explicit "remember this" route
// and the background extraction task go through it, so sanitization, conflict
// resolution, block rebuild and capacity enforcement cannot be bypassed by
// adding a new caller.
func (s *Service) write(
	ctx context.Context,
	scope interfaces.MemoryScope,
	cfg *types.MemoryConfig,
	item types.MemoryItem,
) (*types.MemoryItem, error) {
	content := types.SanitizeMemoryContent(item.Content)
	if content == "" {
		return nil, errors.New("memory: empty content")
	}
	// Redact before anything else looks at the statement. A memory is injected
	// into the system prompt of every later turn, so a credential that reaches
	// storage is not merely retained, it is re-sent to a model repeatedly.
	if redacted, changed := types.RedactSensitive(content); changed {
		if types.IsMostlyRedacted(redacted) {
			logger.Infof(ctx, "memory: dropped a statement that was mostly sensitive material")
			return nil, ErrSensitiveContent
		}
		logger.Infof(ctx, "memory: redacted sensitive material before storing")
		content = types.SanitizeMemoryContent(redacted)
	}
	if !types.IsValidMemoryKind(item.Kind) {
		item.Kind = types.MemoryKindFact
	}

	// Something the user deliberately forgot must not come back the next time
	// distillation reads the message it came from. Two checks, because the
	// re-derived statement is usually worded slightly differently and so does
	// not hash the same: the exact fingerprint, and whether the message it came
	// from already produced a memory the user rejected.
	forgotten, err := s.repo.HasTombstone(ctx, scope, types.MemoryFingerprint(content))
	if err != nil {
		return nil, fmt.Errorf("check forgotten memory: %w", err)
	}
	if !forgotten && item.SourceMessageID != "" && item.Origin == types.MemoryOriginExtracted {
		// Only the background path is gated this way. An explicit "remember
		// this" is the user asking again, and must always win.
		forgotten, err = s.repo.HasTombstoneForMessage(
			ctx, scope, item.SourceMessageID, rejectedMessageWindow,
		)
		if err != nil {
			return nil, fmt.Errorf("check forgotten source: %w", err)
		}
	}
	if forgotten {
		logger.Infof(ctx, "memory: skipped a statement the user previously deleted")
		return nil, ErrPreviouslyForgotten
	}
	if _, err := s.repo.EnsureSubject(ctx, scope); err != nil {
		return nil, fmt.Errorf("ensure memory subject: %w", err)
	}

	topic := types.SanitizeMemoryTopic(item.Topic)
	normalizedKey := types.MemoryItemKey(topic, content)
	existing, err := s.repo.FindActiveByKey(ctx, scope, normalizedKey)
	if err != nil {
		return nil, fmt.Errorf("find conflicting memory: %w", err)
	}
	if existing != nil && types.SanitizeMemoryContent(existing.Content) == content {
		// Same statement about the same topic: nothing changed, so keep the
		// original timestamps instead of churning the row on every turn.
		return existing, nil
	}
	if existing == nil {
		// The same fact often arrives twice: once because the user said
		// "remember ..." and again from the background distillation, phrased
		// slightly differently ("我们的生产库是 X" vs "生产库是 X"). They get
		// different topic keys, so key matching alone lets both through and
		// the user sees their memory duplicated.
		duplicate, longer, err := s.findContainedDuplicate(ctx, scope, item.Kind, content)
		if err != nil {
			return nil, err
		}
		if duplicate != nil && !longer {
			return duplicate, nil
		}
		// The new statement subsumes the old one, so let it supersede.
		existing = duplicate
	}

	stored := &types.MemoryItem{
		ID:              uuid.New().String(),
		TenantID:        scope.TenantID,
		SubjectID:       scope.SubjectID,
		Kind:            item.Kind,
		Content:         content,
		Topic:           topic,
		NormalizedKey:   normalizedKey,
		Importance:      types.ClampMemoryImportance(item.Importance),
		Origin:          item.Origin,
		Status:          statusForWrite(item),
		SourceSessionID: item.SourceSessionID,
		SourceMessageID: item.SourceMessageID,
		ValidFrom:       time.Now(),
		ExpiresAt:       item.ExpiresAt,
	}
	if stored.Origin == "" {
		stored.Origin = types.MemoryOriginExtracted
	}
	if err := s.repo.CreateItem(ctx, stored); err != nil {
		return nil, fmt.Errorf("create memory item: %w", err)
	}
	if existing != nil {
		// Supersede rather than delete: the old statement keeps its content
		// and gains invalid_at, so the memory manager can show what changed.
		if err := s.repo.SupersedeItem(ctx, scope, existing.ID, stored.ID); err != nil {
			logger.Warnf(ctx, "memory: supersede %s failed: %v", existing.ID, err)
		}
	}

	s.enforceCapacity(ctx, scope, cfg)
	s.rebuildBlock(ctx, scope)
	// A memory with no vector is invisible to semantic recall, so this runs on
	// every write. It is best effort: failing to embed must not fail the write,
	// and the backfill pass picks up whatever this missed.
	s.storeItemEmbedding(ctx, scope, cfg, stored)
	return stored, nil
}

// findContainedDuplicate looks for a live memory of the same kind whose
// statement contains, or is contained by, the incoming one.
//
// Containment is deliberately the whole rule. It is cheap, explainable to a
// user reading their own memory list, and it cannot merge two statements that
// merely share a topic — only ones where the shorter adds nothing the longer
// does not already say. The returned bool reports whether the new statement is
// the longer of the two.
func (s *Service) findContainedDuplicate(
	ctx context.Context, scope interfaces.MemoryScope, kind, content string,
) (*types.MemoryItem, bool, error) {
	candidates, err := s.repo.ListLive(ctx, scope, kind, 200)
	if err != nil {
		return nil, false, fmt.Errorf("scan for duplicate memory: %w", err)
	}
	normalized := types.NormalizeMemoryForMatch(content)
	if normalized == "" {
		return nil, false, nil
	}
	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		existing := types.NormalizeMemoryForMatch(candidate.Content)
		if existing == "" {
			continue
		}
		if strings.Contains(existing, normalized) {
			return candidate, false, nil
		}
		if strings.Contains(normalized, existing) {
			return candidate, true, nil
		}
	}
	return nil, false, nil
}

// statusForWrite decides whether a memory takes effect immediately or waits
// for the user.
//
// Something the user said takes effect at once. Something the system guessed
// about them — their role, their domain, inferred from the questions they ask —
// is proposed instead. Inference is where the value is and also where the harm
// is: a wrong guess asserted silently is how a memory feature loses trust for
// good, and unlike ChatGPT's background layer this one stays auditable.
func statusForWrite(item types.MemoryItem) string {
	if item.Inferred && item.Origin != types.MemoryOriginExplicit && item.Origin != types.MemoryOriginManual {
		return types.MemoryStatusPending
	}
	return types.MemoryStatusActive
}

// enforceCapacity archives the lowest ranked items once the subject exceeds
// its cap. This is the only automatic forgetting in the system.
func (s *Service) enforceCapacity(ctx context.Context, scope interfaces.MemoryScope, cfg *types.MemoryConfig) {
	maxItems := cfg.EffectiveMaxItems()
	count, err := s.repo.CountActive(ctx, scope)
	if err != nil {
		logger.Warnf(ctx, "memory: count active failed: %v", err)
		return
	}
	if count <= int64(maxItems) {
		return
	}
	archived, err := s.repo.ArchiveLowestRanked(ctx, scope, maxItems)
	if err != nil {
		logger.Warnf(ctx, "memory: archive overflow failed: %v", err)
		return
	}
	logger.Infof(ctx, "memory: archived %d items over the %d cap", archived, maxItems)
}

// rebuildBlock re-renders the resident block so the read path stays a single
// primary-key lookup. Called after every mutation.
func (s *Service) rebuildBlock(ctx context.Context, scope interfaces.MemoryScope) {
	items, err := s.repo.ListActiveResident(ctx, scope, 60)
	if err != nil {
		logger.Warnf(ctx, "memory: rebuild block load failed: %v", err)
		return
	}
	count, err := s.repo.CountActive(ctx, scope)
	if err != nil {
		logger.Warnf(ctx, "memory: rebuild block count failed: %v", err)
		return
	}
	block := types.RenderMemoryBlock(items)
	if err := s.repo.UpdateSubjectBlock(ctx, scope, block, int(count)); err != nil {
		logger.Warnf(ctx, "memory: rebuild block store failed: %v", err)
	}
}

// ListItems backs the memory manager list.
func (s *Service) ListItems(
	ctx context.Context, status string, limit, offset int,
) ([]*types.MemoryItem, int64, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, 0, err
	}
	return s.repo.ListItems(ctx, scope, status, limit, offset)
}

// ListTopics returns subjects that have been counted but not yet promoted.
func (s *Service) ListTopics(
	ctx context.Context, limit, offset int,
) ([]*types.MemoryTopicView, int64, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, 0, err
	}
	stats, total, err := s.repo.ListUnpromotedTopics(ctx, scope, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	threshold := s.workspaceConfig(ctx, scope.TenantID).EffectiveInterestThreshold()
	views := make([]*types.MemoryTopicView, 0, len(stats))
	for _, stat := range stats {
		if view := types.MemoryTopicViewFromStat(stat, threshold); view != nil {
			views = append(views, view)
		}
	}
	return views, total, nil
}

func (s *Service) unpromotedTopic(
	ctx context.Context, scope interfaces.MemoryScope, id string,
) (*types.MemoryTopicStat, error) {
	stat, err := s.repo.TopicByID(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	if stat == nil || stat.PromotedAt != nil {
		return nil, ErrItemNotFound
	}
	return stat, nil
}

// PromoteTopic turns a counted subject into an interest without waiting.
func (s *Service) PromoteTopic(ctx context.Context, id string) (*types.MemoryItem, error) {
	scope, cfg, ok := s.enabledScope(ctx)
	if !ok {
		return nil, ErrMemoryDisabled
	}
	stat, err := s.unpromotedTopic(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	item, err := s.write(ctx, scope, cfg, types.MemoryItem{
		Kind:       types.MemoryKindInterest,
		Topic:      stat.Topic,
		Content:    stat.Topic,
		Importance: 3,
		Origin:     types.MemoryOriginManual,
	})
	if err != nil {
		return nil, err
	}
	if err := s.repo.MarkTopicPromoted(ctx, scope, stat.NormalizedKey); err != nil {
		logger.Warnf(ctx, "memory: mark topic promoted failed: %v", err)
	}
	return item, nil
}

// DeleteTopic stops tracking a subject and remembers the refusal so automatic
// promotion cannot bring the same label back.
func (s *Service) DeleteTopic(ctx context.Context, id string) error {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return err
	}
	stat, err := s.unpromotedTopic(ctx, scope, id)
	if err != nil {
		return err
	}
	s.tombstoneTopic(ctx, scope, stat)
	return s.repo.DeleteTopic(ctx, scope, id)
}

// ListDocuments returns documents cited often enough to count as a habit.
func (s *Service) ListDocuments(
	ctx context.Context, limit, offset int,
) ([]*types.MemoryDocView, int64, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, total, err := s.repo.ListFamiliarDocs(
		ctx, scope, types.MemoryDocAffinityMinHits, limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	views := make([]*types.MemoryDocView, 0, len(rows))
	for _, row := range rows {
		if view := types.MemoryDocViewFromAffinity(row); view != nil {
			views = append(views, view)
		}
	}
	return views, total, nil
}

// DeleteDocument stops using one document as a personal retrieval signal.
func (s *Service) DeleteDocument(ctx context.Context, id string) error {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return err
	}
	row, err := s.repo.DocAffinityByID(ctx, scope, id)
	if err != nil {
		return err
	}
	if row == nil {
		return ErrItemNotFound
	}
	return s.repo.DeleteDocAffinity(ctx, scope, id)
}

// FamiliarKnowledgeIDs returns document ids this person keeps citing.
func (s *Service) FamiliarKnowledgeIDs(ctx context.Context) []string {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil
	}
	rows, err := s.repo.TopDocAffinity(ctx, scope, 200)
	if err != nil {
		logger.Warnf(ctx, "memory: load familiar documents failed: %v", err)
		return nil
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row == nil || row.KnowledgeID == "" || row.Hits < types.MemoryDocAffinityMinHits {
			continue
		}
		ids = append(ids, row.KnowledgeID)
	}
	return ids
}

func (s *Service) topicWasForgotten(
	ctx context.Context, scope interfaces.MemoryScope, labels ...string,
) bool {
	seen := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		fingerprint := types.MemoryFingerprint(types.SanitizeMemoryContent(label))
		if fingerprint == "" {
			continue
		}
		if _, ok := seen[fingerprint]; ok {
			continue
		}
		seen[fingerprint] = struct{}{}
		forgotten, err := s.repo.HasTombstone(ctx, scope, fingerprint)
		if err != nil {
			logger.Warnf(ctx, "memory: check forgotten topic failed: %v", err)
			continue
		}
		if forgotten {
			return true
		}
	}
	return false
}

func (s *Service) tombstoneTopic(
	ctx context.Context, scope interfaces.MemoryScope, stat *types.MemoryTopicStat,
) {
	if stat == nil {
		return
	}
	labels := make([]string, 0, 1+len(stat.Aliases))
	if stat.Topic != "" {
		labels = append(labels, stat.Topic)
	}
	labels = append(labels, stat.Aliases...)
	seen := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		content := types.SanitizeMemoryContent(label)
		fingerprint := types.MemoryFingerprint(content)
		if fingerprint == "" {
			continue
		}
		if _, ok := seen[fingerprint]; ok {
			continue
		}
		seen[fingerprint] = struct{}{}
		if err := s.repo.AddTombstone(ctx, scope, stat.Topic, fingerprint, ""); err != nil {
			logger.Warnf(ctx, "memory: record topic tombstone failed: %v", err)
		}
	}
}

// CreateItem adds a memory the user typed themselves. It goes through the same
// write path as everything else, so a hand-written memory can supersede an
// extracted one about the same topic rather than sitting next to it.
func (s *Service) CreateItem(
	ctx context.Context, kind, content string, importance int,
) (*types.MemoryItem, error) {
	scope, cfg, ok := s.enabledScope(ctx)
	if !ok {
		return nil, ErrMemoryDisabled
	}
	if !types.IsValidMemoryKind(kind) {
		kind = types.MemoryKindFact
	}
	if importance <= 0 {
		importance = 3
	}
	return s.write(ctx, scope, cfg, types.MemoryItem{
		Kind:       kind,
		Content:    content,
		Importance: importance,
		Origin:     types.MemoryOriginManual,
	})
}

// UpdateItem edits one item from the memory manager. Edited items become
// manual so a later extraction does not quietly undo a user's correction.
func (s *Service) UpdateItem(
	ctx context.Context, id, content string, importance int,
) (*types.MemoryItem, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	existing, err := s.repo.GetItem(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrItemNotFound
	}
	sanitized := types.SanitizeMemoryContent(content)
	if sanitized == "" {
		return nil, errors.New("memory: empty content")
	}
	// Keep the original topic: the user is correcting the statement, not
	// re-filing it under a different subject, and reusing the topic is what
	// keeps the correction able to supersede a future extraction.
	normalizedKey := types.MemoryItemKey(existing.Topic, sanitized)
	importance = types.ClampMemoryImportance(importance)
	if err := s.repo.UpdateItemContent(ctx, scope, id, sanitized, normalizedKey, importance); err != nil {
		return nil, err
	}
	s.rebuildBlock(ctx, scope)
	return s.repo.GetItem(ctx, scope, id)
}

// DeleteItem forgets one memory permanently.
func (s *Service) DeleteItem(ctx context.Context, id string) error {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return err
	}
	existing, err := s.repo.GetItem(ctx, scope, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrItemNotFound
	}
	// Record the rejection before removing the row. Deleting a memory that
	// distillation is about to re-derive from the same message is how a user
	// ends up deleting the same thing twice and stops trusting the feature.
	if err := s.repo.AddTombstone(
		ctx, scope, existing.Topic, types.MemoryFingerprint(existing.Content), existing.SourceMessageID,
	); err != nil {
		logger.Warnf(ctx, "memory: record tombstone failed: %v", err)
	}
	if err := s.repo.DeleteItem(ctx, scope, id); err != nil {
		return err
	}
	s.rebuildBlock(ctx, scope)
	return nil
}

// Clear forgets everything in the caller's memory space.
func (s *Service) Clear(ctx context.Context) (int64, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return 0, err
	}
	// Clearing is a rejection of everything currently stored, so it leaves the
	// same tombstones an individual delete would.
	items, _, err := s.repo.ListItems(ctx, scope, "", types.MaxMemoryTombstones, 0)
	if err != nil {
		return 0, err
	}
	for _, item := range items {
		if item == nil {
			continue
		}
		if err := s.repo.AddTombstone(
			ctx, scope, item.Topic, types.MemoryFingerprint(item.Content), item.SourceMessageID,
		); err != nil {
			logger.Warnf(ctx, "memory: record tombstone during clear failed: %v", err)
		}
	}
	removed, err := s.repo.DeleteAll(ctx, scope)
	if err != nil {
		return 0, err
	}
	if err := s.repo.DeleteAllTopics(ctx, scope); err != nil {
		return 0, err
	}
	if err := s.repo.DeleteAllDocAffinity(ctx, scope); err != nil {
		return 0, err
	}
	s.rebuildBlock(ctx, scope)
	return removed, nil
}

// GetSettings returns the merged view the settings UI renders.
func (s *Service) GetSettings(ctx context.Context) (*types.MemorySettings, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	cfg := s.workspaceConfig(ctx, scope.TenantID)
	settings := &types.MemorySettings{
		WorkspaceEnabled: cfg.MemoryEnabled(),
		UserEnabled:      true,
		WriteMode:        cfg.WriteMode,
		MaxItems:         cfg.EffectiveMaxItems(),
	}
	if settings.WriteMode == "" {
		settings.WriteMode = types.MemoryWriteExplicitOnly
	}
	subject, err := s.repo.GetSubject(ctx, scope)
	if err != nil {
		return nil, err
	}
	if subject != nil {
		settings.UserEnabled = subject.Enabled
		settings.ItemCount = subject.ItemCount
	}
	count, err := s.repo.CountActive(ctx, scope)
	if err == nil {
		settings.ItemCount = int(count)
	}
	settings.Effective = settings.WorkspaceEnabled && settings.UserEnabled
	return settings, nil
}

// SetEnabled flips the caller's own opt out.
func (s *Service) SetEnabled(ctx context.Context, enabled bool) error {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return err
	}
	return s.repo.UpdateSubjectEnabled(ctx, scope, enabled)
}

// ---------------------------------------------------------------------------
// Retrieval conditioning
// ---------------------------------------------------------------------------

// retrievalBackgroundRuneBudget bounds what reaches the rewriter. The rewrite
// prompt is small and latency-sensitive; a paragraph of background would both
// slow it down and drown the actual question.
const retrievalBackgroundRuneBudget = 240

// RetrievalContextFor returns what memory contributes to retrieval.
//
// Like Recall this makes no model call: it is two indexed reads plus string
// assembly, because it runs before the first token of every retrieval turn.
func (s *Service) RetrievalContextFor(ctx context.Context) interfaces.RetrievalContext {
	condCtx, condSpan := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name: "memory.retrieval_context",
	})
	scope, cfg, ok := s.enabledScope(condCtx)
	if !ok || !cfg.RetrievalConditioningEnabled() {
		reason := "disabled"
		if !ok {
			reason = s.scopeDisableReason(condCtx)
		} else if !cfg.RetrievalConditioningEnabled() {
			reason = "retrieval_conditioning_disabled"
		}
		condSpan.Finish(map[string]interface{}{
			"outcome": "skipped",
			"reason":  reason,
		}, nil, nil)
		return interfaces.RetrievalContext{}
	}

	items, err := s.repo.ListActiveByKinds(condCtx, scope,
		[]string{types.MemoryKindProfile, types.MemoryKindInterest}, 30)
	if err != nil {
		logger.Warnf(condCtx, "memory: load retrieval context failed: %v", err)
		condSpan.Finish(map[string]interface{}{
			"outcome": "error",
			"error":   err.Error(),
		}, nil, err)
		return interfaces.RetrievalContext{}
	}

	var (
		background []string
		interests  []string
		used       []*types.MemoryItem
		budget     int
	)
	for _, item := range items {
		if item == nil {
			continue
		}
		line := types.SanitizeMemoryContent(item.Content)
		if line == "" {
			continue
		}
		cost := len([]rune(line)) + 2
		if budget+cost > retrievalBackgroundRuneBudget {
			break
		}
		budget += cost
		used = append(used, item)
		if item.Kind == types.MemoryKindInterest {
			interests = append(interests, line)
			continue
		}
		background = append(background, line)
	}

	documents := s.topDocumentTitles(condCtx, scope)

	retrievalCtx := interfaces.RetrievalContext{
		Background: strings.Join(background, "；"),
		Interests:  interests,
		Documents:  documents,
		Items:      used,
	}
	logger.Infof(condCtx,
		"memory: retrieval context subject=%s interests=%d documents=%d items=%d",
		scope.SubjectID, len(interests), len(documents), len(used))
	condSpan.Finish(langfuse.SummarizeRetrievalContextOutput(
		retrievalCtx.Background, retrievalCtx.Interests, retrievalCtx.Documents, retrievalCtx.Items,
	), map[string]interface{}{
		"tenant_id": scope.TenantID,
	}, nil)
	return retrievalCtx
}

// topDocumentTitles gives the rewriter the vocabulary this person's answers
// usually come from. Titles are used rather than ids because the rewriter's job
// is to produce better search text, not to address documents.
func (s *Service) topDocumentTitles(ctx context.Context, scope interfaces.MemoryScope) []string {
	rows, err := s.repo.TopDocAffinity(ctx, scope, 5)
	if err != nil {
		logger.Warnf(ctx, "memory: load document affinity failed: %v", err)
		return nil
	}
	titles := make([]string, 0, len(rows))
	for _, row := range rows {
		if row == nil || strings.TrimSpace(row.Title) == "" {
			continue
		}
		// One sighting is not a habit.
		if row.Hits < types.MemoryDocAffinityMinHits {
			continue
		}
		titles = append(titles, row.Title)
	}
	return titles
}

// DocumentAffinity scores documents by how much this person has relied on them.
func (s *Service) DocumentAffinity(ctx context.Context, knowledgeIDs []string) map[string]int {
	scope, cfg, ok := s.enabledScope(ctx)
	if !ok || !cfg.RetrievalConditioningEnabled() || len(knowledgeIDs) == 0 {
		return nil
	}
	affinity, err := s.repo.DocAffinity(ctx, scope, knowledgeIDs)
	if err != nil {
		logger.Warnf(ctx, "memory: read document affinity failed: %v", err)
		return nil
	}
	return affinity
}

// RecordAnswerSources notes which documents an answer drew on.
//
// The references attached to an answer are a weaker signal than an explicit
// thumbs-up, but they are the only one available without asking the user
// anything, and they are what makes the reranker able to prefer the material
// this person keeps coming back to.
func (s *Service) RecordAnswerSources(ctx context.Context, refs []types.MemoryDocAffinity) {
	if len(refs) == 0 {
		return
	}
	scope, cfg, ok := s.enabledScope(ctx)
	if !ok || !cfg.RetrievalConditioningEnabled() {
		return
	}
	if _, err := s.repo.EnsureSubject(ctx, scope); err != nil {
		logger.Warnf(ctx, "memory: ensure subject for affinity failed: %v", err)
		return
	}
	if err := s.repo.BumpDocAffinity(ctx, scope, refs); err != nil {
		logger.Warnf(ctx, "memory: record answer sources failed: %v", err)
	}
}

// ObserveQuestionTopics counts what a person asked about and promotes a subject
// into memory once it recurs.
//
// This is the answer to "a knowledge-base question is not about the user, so it
// produces nothing". A single question really is noise — recording it would
// fill the profile with every passing curiosity. But the same subject across
// several conversations says something durable about the person, and counting
// first is how MemoryOS separates the two without a rule that throws away every
// question. Returns the interests promoted by this call.
func (s *Service) ObserveQuestionTopics(ctx context.Context, topics []string) []string {
	if len(topics) == 0 {
		return nil
	}
	scope, cfg, ok := s.enabledScope(ctx)
	if !ok {
		return nil
	}
	return s.observeTopics(ctx, scope, cfg, cfg.ExtractModelID, topics)
}

// observeTopics is the scope-explicit form.
//
// Distillation runs on a background worker whose context carries no principal —
// its scope comes from the task payload — so anything the distiller calls has
// to be handed the scope rather than re-deriving it from the request.
func (s *Service) observeTopics(
	ctx context.Context,
	scope interfaces.MemoryScope,
	cfg *types.MemoryConfig,
	modelID string,
	topics []string,
) []string {
	if len(topics) == 0 || cfg == nil || !cfg.AutoExtractEnabled() {
		return nil
	}
	if _, err := s.repo.EnsureSubject(ctx, scope); err != nil {
		logger.Warnf(ctx, "memory: ensure subject for topics failed: %v", err)
		return nil
	}

	// Clean the labels first, then resolve them against the subjects this
	// person already has. Counting the raw string is what made this feature
	// silently useless: a model names the same subject differently every run,
	// so each sighting landed under its own key and no topic ever recurred.
	surfaces := make([]string, 0, len(topics))
	for _, topic := range topics {
		if topic = types.SanitizeMemoryTopic(topic); topic != "" {
			surfaces = append(surfaces, topic)
		}
	}
	if len(surfaces) == 0 {
		return nil
	}
	resolutions := s.resolveTopics(ctx, scope, modelID, surfaces)

	threshold := cfg.EffectiveInterestThreshold()
	var promoted []string
	for _, resolution := range resolutions {
		// The stored label stays the one this subject was first recorded under,
		// so a person's topic list does not churn its wording every time the
		// model rephrases. The new wording is kept as an alias.
		canonicalTopic := resolution.Surface
		if resolution.Canonical != nil {
			canonicalTopic = resolution.Canonical.Topic
		}
		key := types.NormalizeTopicKey(canonicalTopic)
		if key == "" {
			continue
		}
		if s.topicWasForgotten(ctx, scope, canonicalTopic, resolution.Surface) {
			continue
		}
		aliasesBefore := s.topicAliasCount(ctx, scope, key)
		stat, err := s.repo.BumpTopic(ctx, scope, canonicalTopic, key, resolution.Surface)
		if err != nil {
			logger.Warnf(ctx, "memory: count topic %q failed: %v", canonicalTopic, err)
			continue
		}
		if stat == nil {
			logger.Warnf(ctx, "memory: topic %q produced no row", canonicalTopic)
			continue
		}
		// Without this line there is no way to tell, from the outside, whether
		// a topic was counted, which subject it was folded into, or which tier
		// decided — and "hits is always 1" looks identical to "nothing ran".
		logger.Infof(ctx,
			"memory: topic %q -> %q (tier=%s, hits=%d, threshold=%d)",
			resolution.Surface, canonicalTopic, resolutionTier(resolution), stat.Hits, threshold)
		if types.TopicLooksLikeOneQuestion(canonicalTopic) {
			logger.Warnf(ctx,
				"memory: topic %q names one question rather than a subject, so it will never "+
					"recur and can never reach the threshold", canonicalTopic)
		}
		// A new wording changes what this subject's interest should embed to,
		// and the vector was written once at promotion time. Drop it and let
		// the maintenance backfill rebuild it with the wording included.
		if len(stat.Aliases) > aliasesBefore {
			s.invalidateInterestEmbedding(ctx, scope, canonicalTopic)
		}
		if resolution.MergedLabel != "" {
			canonicalTopic, key = s.renameTopic(ctx, scope, stat, resolution.MergedLabel, key)
		}

		if stat.PromotedAt != nil || stat.Hits < threshold {
			continue
		}
		if _, err := s.write(ctx, scope, cfg, types.MemoryItem{
			Kind:       types.MemoryKindInterest,
			Topic:      canonicalTopic,
			Content:    canonicalTopic,
			Importance: 3,
			Origin:     types.MemoryOriginExtracted,
		}); err != nil {
			if !errors.Is(err, ErrPreviouslyForgotten) && !errors.Is(err, ErrSensitiveContent) {
				logger.Warnf(ctx, "memory: promote interest failed: %v", err)
			}
			// Mark it promoted anyway: a topic the user has forgotten once
			// should not re-propose itself on every subsequent question.
		}
		if err := s.repo.MarkTopicPromoted(ctx, scope, key); err != nil {
			logger.Warnf(ctx, "memory: mark topic promoted failed: %v", err)
		}
		promoted = append(promoted, canonicalTopic)
	}
	if len(promoted) > 0 {
		logger.Infof(ctx, "memory: promoted %d recurring topics into interests", len(promoted))
	}
	return promoted
}

// renameTopic adopts a better label for a subject and keeps everything that
// refers to it in step. Returns the label and key to carry on with.
//
// The label a merge leaves behind is otherwise just whichever wording arrived
// first, and that label is not cosmetic: it is fed to the query rewriter as
// this person's vocabulary and shown to them as what we think they care about.
func (s *Service) renameTopic(
	ctx context.Context,
	scope interfaces.MemoryScope,
	stat *types.MemoryTopicStat,
	newLabel, currentKey string,
) (string, string) {
	newKey := types.NormalizeTopicKey(newLabel)
	renamed, err := s.repo.RenameTopic(ctx, scope, currentKey, newKey, newLabel)
	if err != nil {
		logger.Warnf(ctx, "memory: rename topic %q failed: %v", stat.Topic, err)
		return stat.Topic, currentKey
	}
	if !renamed {
		return stat.Topic, currentKey
	}
	logger.Infof(ctx, "memory: renamed topic %q to %q", stat.Topic, newLabel)
	s.renameInterestItem(ctx, scope, stat.Topic, newLabel)
	return newLabel, newKey
}

// renameInterestItem keeps a promoted interest in step with its subject.
//
// It only touches an item that still reads exactly as the old label. Anything
// else has been edited by the user, and quietly overwriting someone's own
// wording is worse than leaving the two slightly out of step.
func (s *Service) renameInterestItem(
	ctx context.Context, scope interfaces.MemoryScope, oldLabel, newLabel string,
) {
	items, err := s.repo.ListActiveByKinds(ctx, scope, []string{types.MemoryKindInterest}, 100)
	if err != nil {
		logger.Warnf(ctx, "memory: load interests for rename failed: %v", err)
		return
	}
	for _, item := range items {
		if item == nil || item.Content != oldLabel {
			continue
		}
		err := s.repo.UpdateItemContent(
			ctx, scope, item.ID, newLabel, types.MemoryItemKey(newLabel, newLabel), item.Importance)
		if err != nil {
			logger.Warnf(ctx, "memory: rename interest item failed: %v", err)
			continue
		}
		// The vector still spells the old label, so semantic recall would keep
		// matching a name this subject no longer goes by.
		if err := s.repo.DeleteItemEmbedding(ctx, scope, item.ID); err != nil {
			logger.Warnf(ctx, "memory: drop renamed interest embedding failed: %v", err)
		}
		s.rebuildBlock(ctx, scope)
		return
	}
}

// topicAliasCount reports how many wordings a subject is already known by, so
// the caller can tell whether a sighting added one.
func (s *Service) topicAliasCount(
	ctx context.Context, scope interfaces.MemoryScope, key string,
) int {
	stat, err := s.repo.TopicByKey(ctx, scope, key)
	if err != nil || stat == nil {
		return 0
	}
	return len(stat.Aliases)
}

// invalidateInterestEmbedding drops the vector of the interest promoted from
// this subject, if there is one. Best effort: losing the vector for one
// maintenance cycle costs semantic recall on one memory, and the item stays
// reachable by wording the whole time.
func (s *Service) invalidateInterestEmbedding(
	ctx context.Context, scope interfaces.MemoryScope, topic string,
) {
	items, err := s.repo.ListActiveByKinds(ctx, scope, []string{types.MemoryKindInterest}, 100)
	if err != nil {
		logger.Warnf(ctx, "memory: load interests for re-embedding failed: %v", err)
		return
	}
	for _, item := range items {
		if item == nil || item.Content != topic {
			continue
		}
		if err := s.repo.DeleteItemEmbedding(ctx, scope, item.ID); err != nil {
			logger.Warnf(ctx, "memory: drop interest embedding failed: %v", err)
		}
		return
	}
}

// resolutionTier names which rule matched, for logs.
func resolutionTier(resolution topicResolution) string {
	if resolution.Tier == "" {
		return "new"
	}
	return resolution.Tier
}

// ConfirmItem accepts something the system inferred, moving it out of the
// pending inbox and into use.
func (s *Service) ConfirmItem(ctx context.Context, id string) (*types.MemoryItem, error) {
	scope, err := ResolveScope(ctx)
	if err != nil {
		return nil, err
	}
	existing, err := s.repo.GetItem(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrItemNotFound
	}
	if err := s.repo.SetItemStatus(ctx, scope, id, types.MemoryStatusActive); err != nil {
		return nil, err
	}
	s.rebuildBlock(ctx, scope)
	return s.repo.GetItem(ctx, scope, id)
}

// RejectItem declines an inference. It deletes rather than archives, so the
// tombstone stops the same guess from being proposed again next week.
func (s *Service) RejectItem(ctx context.Context, id string) error {
	return s.DeleteItem(ctx, id)
}
