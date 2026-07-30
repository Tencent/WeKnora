package repository

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/application/feedbackweight"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var (
	// ErrFeedbackMessageNotFound indicates that the requested assistant message does not exist.
	ErrFeedbackMessageNotFound = errors.New("feedback message not found")
	// ErrFeedbackNotEligible indicates that feedback cannot be recorded for the message.
	ErrFeedbackNotEligible = errors.New("message is not eligible for feedback")
	// ErrFeedbackChunkNotFound indicates that the requested chunk does not exist.
	ErrFeedbackChunkNotFound = errors.New("feedback chunk not found")
	// ErrFeedbackCompletionState indicates that completed-message attribution cannot be changed.
	ErrFeedbackCompletionState = errors.New("completed message attribution is immutable")
)

const messageFeedbackReferenceJoin = "JOIN message_chunk_references AS mcr " +
	"ON mcr.message_tenant_id = mf.tenant_id AND mcr.message_id = mf.message_id"

type feedbackRepository struct {
	db             *gorm.DB
	feedbackConfig *config.FeedbackConfig
}

// NewFeedbackRepository creates a repository for feedback attribution and aggregation.
func NewFeedbackRepository(db *gorm.DB, cfg *config.Config) interfaces.FeedbackRepository {
	var feedbackConfig *config.FeedbackConfig
	if cfg != nil {
		feedbackConfig = cfg.Feedback
	}
	return &feedbackRepository{db: db, feedbackConfig: feedbackConfig}
}

type referenceKey struct {
	tenantID uint64
	chunkID  string
}

type canonicalReferenceScopeKey struct {
	tenantID        uint64
	knowledgeBaseID string
	chunkID         string
}

func canonicalScopeKey(scope types.ChunkFeedbackScope) canonicalReferenceScopeKey {
	return canonicalReferenceScopeKey{
		tenantID:        scope.TenantID,
		knowledgeBaseID: scope.KnowledgeBaseID,
		chunkID:         scope.ChunkID,
	}
}

func (r *feedbackRepository) CompleteAssistantMessageWithReferences(
	ctx context.Context,
	messageTenantID uint64,
	message *types.Message,
	references types.References,
) (bool, error) {
	if message == nil {
		return false, ErrFeedbackMessageNotFound
	}
	eligible := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var persisted types.Message
		if err := tx.Table("messages AS m").
			Select("m.*").
			Joins("JOIN sessions AS s ON s.id = m.session_id AND s.deleted_at IS NULL").
			Where("m.id = ? AND m.session_id = ? AND s.tenant_id = ? AND m.deleted_at IS NULL",
				message.ID, message.SessionID, messageTenantID).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&persisted).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrFeedbackMessageNotFound
			}
			return err
		}
		if persisted.Role != "assistant" {
			return ErrFeedbackNotEligible
		}

		completionReferences := references
		var keys []referenceKey
		var err error
		if message.CanonicalChunkReferencesSet {
			keys, completionReferences, err = resolveCanonicalReferenceKeys(
				tx, references, message.CanonicalChunkReferences,
			)
		} else {
			keys, err = resolveReferenceKeys(tx, references)
		}
		if err != nil {
			return err
		}
		eligible = len(keys) > 0

		if persisted.IsCompleted {
			var existing []types.MessageChunkReference
			if err := tx.Where("message_tenant_id = ? AND message_id = ?", messageTenantID, message.ID).
				Order("chunk_tenant_id, chunk_id").Find(&existing).Error; err != nil {
				return err
			}
			if len(existing) != len(keys) {
				return ErrFeedbackCompletionState
			}
			for i := range existing {
				if existing[i].ChunkTenantID != keys[i].tenantID || existing[i].ChunkID != keys[i].chunkID {
					return ErrFeedbackCompletionState
				}
			}
			return nil
		}

		now := time.Now()
		for _, key := range keys {
			row := &types.MessageChunkReference{
				ID:              uuid.NewString(),
				MessageTenantID: messageTenantID,
				ChunkTenantID:   key.tenantID,
				MessageID:       message.ID,
				ChunkID:         key.chunkID,
				CreatedAt:       now,
			}
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		result := tx.Model(&types.Message{}).
			Where("id = ? AND session_id = ? AND is_completed = ?", message.ID, message.SessionID, false).
			Updates(map[string]interface{}{
				"content":              message.Content,
				"knowledge_references": completionReferences,
				"agent_steps":          message.AgentSteps,
				"is_completed":         true,
				"is_fallback":          message.IsFallback,
				"updated_at":           now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrFeedbackCompletionState
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return eligible, nil
}

func resolveCanonicalReferenceKeys(
	tx *gorm.DB,
	references types.References,
	scopes []types.ChunkFeedbackScope,
) ([]referenceKey, types.References, error) {
	referenceIDs := make(map[string]map[string]struct{}, len(references))
	for _, ref := range references {
		if ref == nil || ref.ID == "" || ref.KnowledgeBaseID == "" ||
			ref.ChunkType == string(types.ChunkTypeWebSearch) ||
			ref.KnowledgeSource == "web_search" ||
			ref.MatchType == types.MatchTypeHistory {
			continue
		}
		if referenceIDs[ref.ID] == nil {
			referenceIDs[ref.ID] = make(map[string]struct{})
		}
		referenceIDs[ref.ID][ref.KnowledgeBaseID] = struct{}{}
	}

	requested := make(map[canonicalReferenceScopeKey]types.ChunkFeedbackScope, len(scopes))
	ids := make([]string, 0, len(scopes))
	seenIDs := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if scope.TenantID == 0 || scope.KnowledgeBaseID == "" || scope.ChunkID == "" {
			continue
		}
		allowedKBs := referenceIDs[scope.ChunkID]
		if _, exists := allowedKBs[scope.KnowledgeBaseID]; !exists {
			continue
		}
		key := canonicalScopeKey(scope)
		requested[key] = scope
		if _, exists := seenIDs[scope.ChunkID]; !exists {
			seenIDs[scope.ChunkID] = struct{}{}
			ids = append(ids, scope.ChunkID)
		}
	}
	if len(requested) == 0 {
		return nil, nil, nil
	}
	sort.Strings(ids)

	var chunks []types.Chunk
	if err := tx.Where("id IN ?", ids).
		Order("tenant_id, id").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Find(&chunks).Error; err != nil {
		return nil, nil, err
	}

	validScopes := make(map[canonicalReferenceScopeKey]struct{}, len(chunks))
	keys := make([]referenceKey, 0, len(chunks))
	for _, chunk := range chunks {
		scope := types.ChunkFeedbackScope{
			TenantID: chunk.TenantID, KnowledgeBaseID: chunk.KnowledgeBaseID, ChunkID: chunk.ID,
		}
		key := canonicalScopeKey(scope)
		if _, allowed := requested[key]; !allowed {
			continue
		}
		validScopes[key] = struct{}{}
		keys = append(keys, referenceKey{tenantID: chunk.TenantID, chunkID: chunk.ID})
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].tenantID != keys[j].tenantID {
			return keys[i].tenantID < keys[j].tenantID
		}
		return keys[i].chunkID < keys[j].chunkID
	})

	validReferences := make(types.References, 0, len(keys))
	seenReferences := make(map[string]struct{}, len(keys))
	for _, ref := range references {
		if ref == nil {
			continue
		}
		for scope := range validScopes {
			if ref.ID != scope.chunkID || ref.KnowledgeBaseID != scope.knowledgeBaseID {
				continue
			}
			key := fmt.Sprintf("%d\x00%s", scope.tenantID, scope.chunkID)
			if _, exists := seenReferences[key]; exists {
				break
			}
			seenReferences[key] = struct{}{}
			referenceCopy := *ref
			validReferences = append(validReferences, &referenceCopy)
			break
		}
	}
	return keys, validReferences, nil
}

func resolveReferenceKeys(tx *gorm.DB, references types.References) ([]referenceKey, error) {
	type candidate struct {
		id   string
		kbID string
	}
	candidates := make(map[string]candidate)
	for _, ref := range references {
		if ref == nil || ref.ID == "" ||
			ref.ChunkType == string(types.ChunkTypeWebSearch) ||
			ref.KnowledgeSource == "web_search" ||
			ref.MatchType == types.MatchTypeHistory {
			continue
		}
		candidates[ref.ID] = candidate{id: ref.ID, kbID: ref.KnowledgeBaseID}
		if ref.ParentChunkID != "" {
			candidates[ref.ParentChunkID] = candidate{id: ref.ParentChunkID, kbID: ref.KnowledgeBaseID}
		}
		for _, id := range ref.SubChunkID {
			if id != "" {
				candidates[id] = candidate{id: id, kbID: ref.KnowledgeBaseID}
			}
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(candidates))
	for id := range candidates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var chunks []types.Chunk
	// Every feedback lifecycle path locks chunks in tenant/id order.
	if err := tx.Where("id IN ?", ids).
		Order("tenant_id, id").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Find(&chunks).Error; err != nil {
		return nil, err
	}
	keys := make([]referenceKey, 0, len(chunks))
	for _, chunk := range chunks {
		candidate := candidates[chunk.ID]
		if candidate.kbID != "" && candidate.kbID != chunk.KnowledgeBaseID {
			continue
		}
		keys = append(keys, referenceKey{tenantID: chunk.TenantID, chunkID: chunk.ID})
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].tenantID != keys[j].tenantID {
			return keys[i].tenantID < keys[j].tenantID
		}
		return keys[i].chunkID < keys[j].chunkID
	})
	return keys, nil
}

func (r *feedbackRepository) HydrateMessages(
	ctx context.Context, tenantID uint64, userID string, messages []*types.Message,
) error {
	if len(messages) == 0 {
		return nil
	}
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		if message != nil {
			message.FeedbackEligible = false
			message.MyFeedback = nil
			ids = append(ids, message.ID)
		}
	}
	var counts []struct {
		MessageID string
		Count     int64
	}
	if err := r.db.WithContext(ctx).Table("message_chunk_references AS mcr").
		Select("mcr.message_id, COUNT(*) AS count").
		Joins(`JOIN chunks AS c
			ON c.tenant_id = mcr.chunk_tenant_id
			AND c.id = mcr.chunk_id
			AND c.deleted_at IS NULL`).
		Joins(`JOIN messages AS m
			ON m.id = mcr.message_id
			AND m.deleted_at IS NULL`).
		Joins(`JOIN sessions AS s
			ON s.id = m.session_id
			AND s.deleted_at IS NULL`).
		Where(`mcr.message_tenant_id = ? AND mcr.message_id IN ?
			AND s.tenant_id = ? AND s.user_id = ?`, tenantID, ids, tenantID, userID).
		Group("mcr.message_id").Scan(&counts).Error; err != nil {
		return err
	}
	eligible := make(map[string]bool, len(counts))
	for _, row := range counts {
		eligible[row.MessageID] = row.Count > 0
	}
	feedbackByMessage := make(map[string]*types.MessageFeedback)
	if userID != "" {
		var feedbacks []*types.MessageFeedback
		if err := r.db.WithContext(ctx).
			Where("tenant_id = ? AND user_id = ? AND message_id IN ?", tenantID, userID, ids).
			Find(&feedbacks).Error; err != nil {
			return err
		}
		for _, feedback := range feedbacks {
			feedbackByMessage[feedback.MessageID] = feedback
		}
	}
	for _, message := range messages {
		if message == nil || message.Role != "assistant" || !message.IsCompleted ||
			userID == "" || !eligible[message.ID] {
			continue
		}
		message.FeedbackEligible = true
		if feedback := feedbackByMessage[message.ID]; feedback != nil {
			message.MyFeedback = &types.MessageFeedbackState{
				Type: feedback.FeedbackType, ReasonCode: feedback.ReasonCode,
			}
		}
	}
	return nil
}

func (r *feedbackRepository) HydrateChunks(
	ctx context.Context,
	chunks []*types.Chunk,
	optimizationThreshold float64,
) error {
	if len(chunks) == 0 {
		return nil
	}
	type chunkIdentity struct {
		TenantID uint64
		ID       string
	}
	type sessionStat struct {
		ChunkTenantID uint64
		ChunkID       string
		SessionCount  int64
	}
	ids := make([]string, 0, len(chunks))
	byID := make(map[chunkIdentity]*types.Chunk, len(chunks))
	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		stored := chunk.RecallWeight
		if stored <= 0 {
			stored = 1
		}
		chunk.StoredRecallWeight = stored
		chunk.EffectiveRecallWeight = 1
		ids = append(ids, chunk.ID)
		byID[chunkIdentity{TenantID: chunk.TenantID, ID: chunk.ID}] = chunk
		total := chunk.LikeCount + chunk.DislikeCount
		if total > 0 {
			rate := float64(chunk.LikeCount) / float64(total)
			chunk.PositiveRate = &rate
			chunk.NeedsOptimization = rate < optimizationThreshold
		} else {
			chunk.PositiveRate = nil
			chunk.NeedsOptimization = false
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var stats []sessionStat
	if err := r.db.WithContext(ctx).
		Table("message_chunk_references AS r").
		Select("r.chunk_tenant_id, r.chunk_id, COUNT(DISTINCT m.session_id) AS session_count").
		Joins("JOIN messages AS m ON m.id = r.message_id AND m.deleted_at IS NULL").
		Where("r.chunk_id IN ?", ids).
		Group("r.chunk_tenant_id, r.chunk_id").
		Scan(&stats).Error; err != nil {
		return err
	}
	for _, stat := range stats {
		if chunk := byID[chunkIdentity{TenantID: stat.ChunkTenantID, ID: stat.ChunkID}]; chunk != nil {
			chunk.SessionCount = stat.SessionCount
		}
	}
	return nil
}

// ListChunkFeedbackStats performs one scoped batch query for retrieval policy
// evaluation. Tenant, knowledge-base, and chunk identity are all part of the
// predicate so shared and cross-workspace searches cannot bleed statistics.
func (r *feedbackRepository) ListChunkFeedbackStats(
	ctx context.Context,
	scopes []types.ChunkFeedbackScope,
) ([]types.ChunkFeedbackStat, error) {
	if len(scopes) == 0 {
		return nil, nil
	}
	type groupKey struct {
		tenantID        uint64
		knowledgeBaseID string
	}
	grouped := make(map[groupKey][]string)
	for _, scope := range scopes {
		if scope.TenantID == 0 || scope.KnowledgeBaseID == "" || scope.ChunkID == "" {
			return nil, fmt.Errorf("invalid chunk feedback scope")
		}
		key := groupKey{tenantID: scope.TenantID, knowledgeBaseID: scope.KnowledgeBaseID}
		grouped[key] = append(grouped[key], scope.ChunkID)
	}
	keys := make([]groupKey, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].tenantID != keys[j].tenantID {
			return keys[i].tenantID < keys[j].tenantID
		}
		return keys[i].knowledgeBaseID < keys[j].knowledgeBaseID
	})

	type row struct {
		TenantID        uint64  `gorm:"column:tenant_id"`
		KnowledgeBaseID string  `gorm:"column:knowledge_base_id"`
		ChunkID         string  `gorm:"column:chunk_id"`
		LikeCount       int64   `gorm:"column:like_count"`
		DislikeCount    int64   `gorm:"column:dislike_count"`
		RecallWeight    float64 `gorm:"column:recall_weight"`
	}
	query := r.db.WithContext(ctx).Model(&types.Chunk{}).
		Select("tenant_id, knowledge_base_id, id AS chunk_id, like_count, dislike_count, recall_weight")
	for i, key := range keys {
		ids := grouped[key]
		sort.Strings(ids)
		predicate := "tenant_id = ? AND knowledge_base_id = ? AND id IN ?"
		if i == 0 {
			query = query.Where(predicate, key.tenantID, key.knowledgeBaseID, ids)
		} else {
			query = query.Or(predicate, key.tenantID, key.knowledgeBaseID, ids)
		}
	}
	var rows []row
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]types.ChunkFeedbackStat, 0, len(rows))
	for _, item := range rows {
		result = append(result, types.ChunkFeedbackStat{
			ChunkFeedbackScope: types.ChunkFeedbackScope{
				TenantID: item.TenantID, KnowledgeBaseID: item.KnowledgeBaseID, ChunkID: item.ChunkID,
			},
			LikeCount: item.LikeCount, DislikeCount: item.DislikeCount, StoredRecallWeight: item.RecallWeight,
		})
	}
	return result, nil
}

func (r *feedbackRepository) ListChunkFeedbackGovernance(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID string,
	query *types.ChunkFeedbackListQuery,
) ([]*types.ChunkFeedbackListItem, int64, error) {
	if query == nil {
		return nil, 0, fmt.Errorf("chunk feedback query is required")
	}
	base := r.db.WithContext(ctx).Table("chunks AS chunk").
		Joins(`LEFT JOIN knowledges AS knowledge
			ON knowledge.id = chunk.knowledge_id
			AND knowledge.tenant_id = chunk.tenant_id
			AND knowledge.deleted_at IS NULL`).
		Where(
			"chunk.tenant_id = ? AND chunk.knowledge_base_id = ? AND chunk.deleted_at IS NULL",
			tenantID,
			knowledgeBaseID,
		)
	if query.Keyword != "" {
		keyword := "%" + strings.ToLower(query.Keyword) + "%"
		base = base.Where(
			"LOWER(chunk.content) LIKE ? OR LOWER(COALESCE(knowledge.title, '')) LIKE ?",
			keyword,
			keyword,
		)
	}

	policy := r.feedbackConfig
	if policy == nil {
		policy = config.DefaultFeedbackConfig()
	}
	switch query.FeedbackStatus {
	case types.ChunkFeedbackStatusRated:
		base = base.Where("chunk.like_count + chunk.dislike_count > 0")
	case types.ChunkFeedbackStatusHigh:
		base = base.Where(
			"chunk.like_count + chunk.dislike_count >= ? AND chunk.positive_rate >= ?",
			policy.MinimumSampleCount,
			policy.HighRateThreshold,
		)
	case types.ChunkFeedbackStatusNormal:
		base = base.Where(
			`chunk.like_count + chunk.dislike_count >= ?
				AND chunk.positive_rate >= ? AND chunk.positive_rate < ?`,
			policy.MinimumSampleCount,
			policy.LowRateThreshold,
			policy.HighRateThreshold,
		)
	case types.ChunkFeedbackStatusLow:
		base = base.Where(
			"chunk.like_count + chunk.dislike_count >= ? AND chunk.positive_rate < ?",
			policy.MinimumSampleCount,
			policy.LowRateThreshold,
		)
	case types.ChunkFeedbackStatusUnrated:
		base = base.Where("chunk.like_count + chunk.dislike_count = 0")
	}
	if query.NeedsOptimization != nil {
		if *query.NeedsOptimization {
			base = base.Where(
				"chunk.like_count + chunk.dislike_count > 0 AND chunk.positive_rate < ?",
				policy.EffectiveOptimizationThreshold(),
			)
		} else {
			base = base.Where(
				"chunk.like_count + chunk.dislike_count = 0 OR chunk.positive_rate >= ?",
				policy.EffectiveOptimizationThreshold(),
			)
		}
	}

	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	sortColumns := map[string]string{
		"updated_at":           "chunk.updated_at",
		"like_count":           "chunk.like_count",
		"dislike_count":        "chunk.dislike_count",
		"positive_rate":        "chunk.positive_rate",
		"stored_recall_weight": "chunk.recall_weight",
		"chunk_index":          "chunk.chunk_index",
	}
	// The effective weight is a policy tier, not the raw positive rate. Keep
	// database pagination aligned with the value the service returns.
	sortColumns["effective_recall_weight"] = fmt.Sprintf(`CASE
		WHEN chunk.like_count + chunk.dislike_count < %d THEN 1.0
		WHEN chunk.positive_rate >= %g THEN %g
		WHEN chunk.positive_rate < %g THEN %g
		ELSE %g
	END`,
		policy.MinimumSampleCount,
		policy.HighRateThreshold,
		policy.HighRecallWeight,
		policy.LowRateThreshold,
		policy.LowRecallWeight,
		policy.NormalRecallWeight,
	)
	sortColumn := sortColumns[query.SortBy]
	if sortColumn == "" {
		sortColumn = sortColumns["updated_at"]
	}
	direction := "DESC"
	if query.SortOrder == "asc" {
		direction = "ASC"
	}

	items := make([]*types.ChunkFeedbackListItem, 0)
	const selectSQL = `
		chunk.id AS chunk_id,
		chunk.knowledge_id,
		COALESCE(knowledge.title, '') AS knowledge_title,
		chunk.chunk_index,
		chunk.chunk_type,
		SUBSTR(TRIM(chunk.content), 1, 200) AS content_preview,
		chunk.like_count,
		chunk.dislike_count,
		chunk.positive_rate,
		chunk.recall_weight AS stored_recall_weight,
		chunk.feedback_reset_at,
		chunk.updated_at,
		(
			SELECT COUNT(DISTINCT message.session_id)
			FROM message_chunk_references AS reference
			JOIN messages AS message
				ON message.id = reference.message_id AND message.deleted_at IS NULL
			WHERE reference.chunk_tenant_id = chunk.tenant_id
				AND reference.chunk_id = chunk.id
		) AS session_count`
	if err := base.Session(&gorm.Session{}).
		Select(selectSQL).
		Order(fmt.Sprintf("CASE WHEN %s IS NULL THEN 1 ELSE 0 END ASC", sortColumn)).
		Order(fmt.Sprintf("%s %s", sortColumn, direction)).
		Order("chunk.id ASC").
		Offset(query.Pagination().Offset()).
		Limit(query.Pagination().Limit()).
		Scan(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *feedbackRepository) ListChunkFeedbackHistory(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID, chunkID string,
	page *types.Pagination,
) ([]*types.ChunkFeedbackAudit, int64, error) {
	if err := r.ensureChunkFeedbackTarget(ctx, tenantID, knowledgeBaseID, chunkID); err != nil {
		return nil, 0, err
	}
	base := r.db.WithContext(ctx).Model(&types.ChunkFeedbackAudit{}).
		Where("chunk_tenant_id = ? AND chunk_id = ?", tenantID, chunkID)
	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var audits []*types.ChunkFeedbackAudit
	if err := base.Session(&gorm.Session{}).
		Order("created_at DESC, id DESC").
		Offset(page.Offset()).
		Limit(page.Limit()).
		Find(&audits).Error; err != nil {
		return nil, 0, err
	}
	return audits, total, nil
}

func (r *feedbackRepository) ensureChunkFeedbackTarget(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID, chunkID string,
) error {
	var count int64
	if err := r.db.WithContext(ctx).Model(&types.Chunk{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
			tenantID,
			knowledgeBaseID,
			chunkID,
		).
		Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return ErrFeedbackChunkNotFound
	}
	return nil
}

func (r *feedbackRepository) ApplyMessageFeedback(
	ctx context.Context, input types.ApplyMessageFeedbackInput,
) (*types.MessageFeedbackState, error) {
	var state *types.MessageFeedbackState
	noAttributableChunks := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var message types.Message
		err := tx.Table("messages AS m").Select("m.*").
			Joins("JOIN sessions AS s ON s.id = m.session_id AND s.deleted_at IS NULL").
			Where(`m.id = ? AND m.session_id = ? AND m.deleted_at IS NULL AND
				s.tenant_id = ? AND s.user_id = ?`,
				input.MessageID, input.SessionID, input.MessageTenantID, input.ActorUserID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&message).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrFeedbackMessageNotFound
			}
			return err
		}
		if message.Role != "assistant" || !message.IsCompleted {
			return ErrFeedbackNotEligible
		}
		keys, chunks, err := lockReferencedChunks(tx, input.MessageTenantID, input.MessageID)
		if err != nil {
			return err
		}
		if len(keys) == 0 {
			noAttributableChunks = true
			return nil
		}

		var existing types.MessageFeedback
		find := tx.Where("tenant_id = ? AND user_id = ? AND message_id = ?",
			input.MessageTenantID, input.ActorUserID, input.MessageID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing)
		hasExisting := find.Error == nil
		if find.Error != nil && !errors.Is(find.Error, gorm.ErrRecordNotFound) {
			return find.Error
		}

		switch input.Type {
		case types.FeedbackTypeNone:
			if hasExisting {
				if err := tx.Delete(&existing).Error; err != nil {
					return err
				}
			}
			state = nil
		case types.FeedbackTypeLike, types.FeedbackTypeDislike:
			// A rating event only occurs when its direction changes. Repeating
			// the same rating is a no-op, and changing only reason metadata must
			// not move FeedbackAt across a reset baseline.
			if hasExisting && existing.FeedbackType == input.Type {
				if !reasonCodesEqual(existing.ReasonCode, input.ReasonCode) {
					existing.ReasonCode = input.ReasonCode
					existing.UpdatedAt = time.Now().UTC()
					if err := tx.Model(&types.MessageFeedback{}).
						Where("id = ?", existing.ID).
						Updates(map[string]interface{}{
							"reason_code": existing.ReasonCode,
							"updated_at":  existing.UpdatedAt,
						}).Error; err != nil {
						return err
					}
				}
				state = &types.MessageFeedbackState{Type: existing.FeedbackType, ReasonCode: existing.ReasonCode}
				return nil
			}
			now := feedbackWriteTime(chunks)
			if !hasExisting {
				existing = types.MessageFeedback{
					ID:         uuid.NewString(),
					TenantID:   input.MessageTenantID,
					UserID:     input.ActorUserID,
					SessionID:  input.SessionID,
					MessageID:  input.MessageID,
					FeedbackAt: now,
					CreatedAt:  now,
				}
			}
			existing.FeedbackType = input.Type
			existing.ReasonCode = input.ReasonCode
			existing.FeedbackAt = now
			existing.UpdatedAt = now
			if hasExisting {
				if err := tx.Save(&existing).Error; err != nil {
					return err
				}
			} else if err := tx.Create(&existing).Error; err != nil {
				return err
			}
			state = &types.MessageFeedbackState{Type: existing.FeedbackType, ReasonCode: existing.ReasonCode}
		default:
			return fmt.Errorf("invalid feedback type %q", input.Type)
		}
		return recomputeChunks(
			tx, keys, input.ActorTenantID, input.ActorUserID, feedbackTriggerSource(input.Type),
			r.feedbackConfig,
		)
	})
	if err == nil && noAttributableChunks {
		return nil, ErrFeedbackNotEligible
	}
	return state, err
}

func reasonCodesEqual(a, b *types.FeedbackReasonCode) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func feedbackWriteTime(chunks []types.Chunk) time.Time {
	now := time.Now().UTC()
	for i := range chunks {
		if resetAt := chunks[i].FeedbackResetAt; resetAt != nil && !now.After(resetAt.UTC()) {
			now = resetAt.UTC().Add(time.Microsecond)
		}
	}
	return now
}

func lockReferencedChunks(
	tx *gorm.DB, messageTenantID uint64, messageID string,
) ([]referenceKey, []types.Chunk, error) {
	var refs []types.MessageChunkReference
	if err := tx.Where("message_tenant_id = ? AND message_id = ?", messageTenantID, messageID).
		Order("chunk_tenant_id, chunk_id").Find(&refs).Error; err != nil {
		return nil, nil, err
	}
	keys := make([]referenceKey, 0, len(refs))
	chunks := make([]types.Chunk, 0, len(refs))
	staleReferenceIDs := make([]string, 0)
	for _, ref := range refs {
		var chunk types.Chunk
		if err := tx.Where("tenant_id = ? AND id = ?", ref.ChunkTenantID, ref.ChunkID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&chunk).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				staleReferenceIDs = append(staleReferenceIDs, ref.ID)
				continue
			}
			return nil, nil, err
		}
		keys = append(keys, referenceKey{tenantID: ref.ChunkTenantID, chunkID: ref.ChunkID})
		chunks = append(chunks, chunk)
	}
	if len(staleReferenceIDs) > 0 {
		sort.Strings(staleReferenceIDs)
		if err := tx.Where("id IN ?", staleReferenceIDs).
			Delete(&types.MessageChunkReference{}).Error; err != nil {
			return nil, nil, err
		}
	}
	return keys, chunks, nil
}

func feedbackTriggerSource(feedbackType types.FeedbackType) types.FeedbackTriggerSource {
	switch feedbackType {
	case types.FeedbackTypeLike:
		return types.FeedbackTriggerLike
	case types.FeedbackTypeDislike:
		return types.FeedbackTriggerDislike
	case types.FeedbackTypeNone:
		return types.FeedbackTriggerCancel
	default:
		return types.FeedbackTriggerLegacy
	}
}

func recomputeChunks(
	tx *gorm.DB,
	keys []referenceKey,
	actorTenantID uint64,
	actorUserID string,
	triggerSource types.FeedbackTriggerSource,
	feedbackConfig *config.FeedbackConfig,
) error {
	for _, key := range keys {
		var chunk types.Chunk
		if err := tx.Where("tenant_id = ? AND id = ?", key.tenantID, key.chunkID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&chunk).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		var rows []struct {
			FeedbackType types.FeedbackType
			Count        int64
		}
		query := tx.Table("message_feedbacks AS mf").
			Select("mf.feedback_type, COUNT(*) AS count").
			Joins(messageFeedbackReferenceJoin).
			Joins("JOIN messages AS m ON m.id = mf.message_id AND m.deleted_at IS NULL").
			Where("mcr.chunk_tenant_id = ? AND mcr.chunk_id = ?", key.tenantID, key.chunkID)
		if chunk.FeedbackResetAt != nil {
			query = query.Where("mf.feedback_at > ?", chunk.FeedbackResetAt.UTC())
		}
		if err := query.Group("mf.feedback_type").Scan(&rows).Error; err != nil {
			return err
		}
		var likes, dislikes int64
		for _, row := range rows {
			switch row.FeedbackType {
			case types.FeedbackTypeLike:
				likes = row.Count
			case types.FeedbackTypeDislike:
				dislikes = row.Count
			}
		}
		var positiveRate *float64
		if total := likes + dislikes; total > 0 {
			rate := float64(likes) / float64(total)
			positiveRate = &rate
		}
		policy := feedbackConfig
		if policy == nil {
			policy = config.DefaultFeedbackConfig()
		}
		weight, _, err := feedbackweight.EffectiveWeight(policy, likes, dislikes)
		if err != nil {
			return err
		}
		oldWeight := chunk.RecallWeight
		if oldWeight <= 0 {
			oldWeight = 1
		}
		if err := tx.Model(&types.Chunk{}).
			Where("tenant_id = ? AND id = ?", key.tenantID, key.chunkID).
			Updates(map[string]interface{}{
				"like_count":    likes,
				"dislike_count": dislikes,
				"positive_rate": positiveRate,
				"recall_weight": weight,
			}).Error; err != nil {
			return err
		}
		if math.Abs(oldWeight-weight) > 1e-9 {
			if err := tx.Create(&types.ChunkFeedbackAudit{
				ChunkTenantID: key.tenantID,
				ChunkID:       key.chunkID,
				ActorTenantID: actorTenantID,
				ActorUserID:   actorUserID,
				Action:        types.ChunkFeedbackAuditActionWeightChanged,
				TriggerSource: triggerSource,
				OldWeight:     oldWeight,
				NewWeight:     weight,
				CreatedAt:     time.Now(),
			}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *feedbackRepository) ResetChunkFeedback(
	ctx context.Context, input types.ResetChunkFeedbackInput,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var chunk types.Chunk
		if err := tx.Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?",
			input.ChunkTenantID, input.KnowledgeBaseID, input.ChunkID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&chunk).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrFeedbackChunkNotFound
			}
			return err
		}
		var latest struct {
			FeedbackAt time.Time
		}
		if err := tx.Table("message_feedbacks AS mf").
			Joins(messageFeedbackReferenceJoin).
			Where("mcr.chunk_tenant_id = ? AND mcr.chunk_id = ?", input.ChunkTenantID, input.ChunkID).
			Select("mf.feedback_at").
			Order("mf.feedback_at DESC").
			Limit(1).
			Scan(&latest).Error; err != nil {
			return err
		}
		resetAt := time.Now().UTC()
		if !latest.FeedbackAt.IsZero() && !resetAt.After(latest.FeedbackAt.UTC()) {
			resetAt = latest.FeedbackAt.UTC().Add(time.Microsecond)
		}
		if chunk.FeedbackResetAt != nil && !resetAt.After(chunk.FeedbackResetAt.UTC()) {
			resetAt = chunk.FeedbackResetAt.UTC().Add(time.Microsecond)
		}
		oldWeight := chunk.RecallWeight
		if oldWeight <= 0 {
			oldWeight = 1
		}
		if err := tx.Model(&types.Chunk{}).
			Where("tenant_id = ? AND id = ?", input.ChunkTenantID, input.ChunkID).
			Updates(map[string]interface{}{
				"like_count":        0,
				"dislike_count":     0,
				"positive_rate":     nil,
				"recall_weight":     1.0,
				"feedback_reset_at": resetAt,
			}).Error; err != nil {
			return err
		}
		return tx.Create(&types.ChunkFeedbackAudit{
			ChunkTenantID: input.ChunkTenantID,
			ChunkID:       input.ChunkID,
			ActorTenantID: input.ActorTenantID,
			ActorUserID:   input.ActorUserID,
			Action:        types.ChunkFeedbackAuditActionReset,
			TriggerSource: types.FeedbackTriggerAdminReset,
			OldWeight:     oldWeight,
			NewWeight:     1,
			CreatedAt:     time.Now(),
		}).Error
	})
}

func (r *feedbackRepository) GetChunkFeedbackDetails(
	ctx context.Context, tenantID uint64, chunkID string,
) (*types.ChunkFeedbackDetails, error) {
	var chunk types.Chunk
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, chunkID).First(&chunk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrFeedbackChunkNotFound
		}
		return nil, err
	}
	var knowledgeTitle string
	if err := r.db.WithContext(ctx).Table("knowledges").
		Select("title").
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id = ? AND deleted_at IS NULL",
			tenantID,
			chunk.KnowledgeBaseID,
			chunk.KnowledgeID,
		).
		Scan(&knowledgeTitle).Error; err != nil {
		return nil, err
	}
	var sessionCount int64
	if err := r.db.WithContext(ctx).Table("message_chunk_references AS reference").
		Joins(
			"JOIN messages AS message ON message.id = reference.message_id AND message.deleted_at IS NULL",
		).
		Where("reference.chunk_tenant_id = ? AND reference.chunk_id = ?", tenantID, chunkID).
		Distinct("message.session_id").
		Count(&sessionCount).Error; err != nil {
		return nil, err
	}
	var reasons []struct {
		ReasonCode types.FeedbackReasonCode
		Count      int64
	}
	query := r.db.WithContext(ctx).Table("message_feedbacks AS mf").
		Select("mf.reason_code, COUNT(*) AS count").
		Joins(messageFeedbackReferenceJoin).
		Joins("JOIN messages AS m ON m.id = mf.message_id AND m.deleted_at IS NULL").
		Where("mcr.chunk_tenant_id = ? AND mcr.chunk_id = ? AND mf.feedback_type = ?",
			tenantID, chunkID, types.FeedbackTypeDislike).
		Where("mf.reason_code IS NOT NULL")
	if chunk.FeedbackResetAt != nil {
		query = query.Where("mf.feedback_at > ?", chunk.FeedbackResetAt.UTC())
	}
	if err := query.Group("mf.reason_code").Scan(&reasons).Error; err != nil {
		return nil, err
	}
	storedWeight := chunk.RecallWeight
	if storedWeight <= 0 {
		storedWeight = 1
	}
	result := &types.ChunkFeedbackDetails{
		ChunkID:            chunk.ID,
		KnowledgeID:        chunk.KnowledgeID,
		KnowledgeBaseID:    chunk.KnowledgeBaseID,
		KnowledgeTitle:     knowledgeTitle,
		ChunkIndex:         chunk.ChunkIndex,
		ChunkType:          chunk.ChunkType,
		Content:            chunk.Content,
		ContentPreview:     feedbackContentPreview(chunk.Content),
		ReasonCounts:       make(map[types.FeedbackReasonCode]int64),
		LikeCount:          chunk.LikeCount,
		DislikeCount:       chunk.DislikeCount,
		SessionCount:       sessionCount,
		PositiveRate:       chunk.PositiveRate,
		StoredRecallWeight: storedWeight,
		FeedbackResetAt:    chunk.FeedbackResetAt,
	}
	for _, row := range reasons {
		result.ReasonCounts[row.ReasonCode] = row.Count
	}
	if err := r.db.WithContext(ctx).
		Where("chunk_tenant_id = ? AND chunk_id = ?", tenantID, chunkID).
		Order("id DESC").Limit(50).Find(&result.Audits).Error; err != nil {
		return nil, err
	}
	return result, nil
}

func feedbackContentPreview(content string) string {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) > 200 {
		runes = runes[:200]
	}
	return string(runes)
}

func (r *feedbackRepository) DeleteMessageWithFeedback(
	ctx context.Context, tenantID uint64, sessionID, messageID, actorUserID string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var message types.Message
		if err := tx.Table("messages AS m").Select("m.*").
			Joins("JOIN sessions AS s ON s.id = m.session_id AND s.deleted_at IS NULL").
			Where("m.session_id = ? AND m.id = ? AND m.deleted_at IS NULL AND s.tenant_id = ?", sessionID, messageID, tenantID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&message).Error; err != nil {
			return err
		}
		return deleteMessagesAndRecompute(
			tx, tenantID, []string{messageID}, actorUserID, r.feedbackConfig,
		)
	})
}

func (r *feedbackRepository) DeleteSessionMessagesWithFeedback(
	ctx context.Context, tenantID uint64, sessionIDs []string, actorUserID string, deleteSessions bool,
) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var messages []types.Message
		if err := tx.Table("messages AS m").Select("m.*").
			Joins("JOIN sessions AS s ON s.id = m.session_id AND s.deleted_at IS NULL").
			Where("m.session_id IN ? AND m.deleted_at IS NULL AND s.tenant_id = ?", sessionIDs, tenantID).
			Clauses(clause.Locking{Strength: "UPDATE"}).Find(&messages).Error; err != nil {
			return err
		}
		ids := make([]string, 0, len(messages))
		for _, message := range messages {
			ids = append(ids, message.ID)
		}
		if err := deleteMessagesAndRecompute(
			tx, tenantID, ids, actorUserID, r.feedbackConfig,
		); err != nil {
			return err
		}
		if deleteSessions {
			return tx.Where("tenant_id = ? AND id IN ?", tenantID, sessionIDs).Delete(&types.Session{}).Error
		}
		return nil
	})
}

func deleteMessagesAndRecompute(
	tx *gorm.DB, actorTenantID uint64, messageIDs []string, actorUserID string,
	feedbackConfig *config.FeedbackConfig,
) error {
	if len(messageIDs) == 0 {
		return nil
	}
	var refs []types.MessageChunkReference
	if err := tx.Where("message_id IN ?", messageIDs).Order("chunk_tenant_id, chunk_id").Find(&refs).Error; err != nil {
		return err
	}
	seen := make(map[referenceKey]struct{})
	keys := make([]referenceKey, 0, len(refs))
	for _, ref := range refs {
		key := referenceKey{tenantID: ref.ChunkTenantID, chunkID: ref.ChunkID}
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		if err := tx.Where("tenant_id = ? AND id = ?", key.tenantID, key.chunkID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&types.Chunk{}).Error; err != nil &&
			!errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if err := tx.Where("message_id IN ?", messageIDs).Delete(&types.MessageFeedback{}).Error; err != nil {
		return err
	}
	if err := tx.Where("message_id IN ?", messageIDs).Delete(&types.MessageChunkReference{}).Error; err != nil {
		return err
	}
	if err := tx.Where("id IN ?", messageIDs).Delete(&types.Message{}).Error; err != nil {
		return err
	}
	return recomputeChunks(
		tx, keys, actorTenantID, actorUserID, types.FeedbackTriggerContentDelete,
		feedbackConfig,
	)
}
