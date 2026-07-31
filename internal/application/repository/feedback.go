package repository

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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
	db           *gorm.DB
	weightPolicy feedbackWeightPolicy
}

type feedbackWeightPolicy struct {
	minimumSampleCount int64
	lowThreshold       float64
	highThreshold      float64
	lowWeight          float64
	normalWeight       float64
	highWeight         float64
}

func defaultFeedbackWeightPolicy() feedbackWeightPolicy {
	return feedbackWeightPolicy{
		minimumSampleCount: 5,
		lowThreshold:       0.5,
		highThreshold:      0.8,
		lowWeight:          0.8,
		normalWeight:       1,
		highWeight:         1.2,
	}
}

func feedbackWeightPolicyFromConfig(cfg *config.FeedbackConfig) feedbackWeightPolicy {
	if cfg == nil {
		return defaultFeedbackWeightPolicy()
	}
	return feedbackWeightPolicy{
		minimumSampleCount: cfg.MinimumSampleCount,
		lowThreshold:       cfg.LowThreshold,
		highThreshold:      cfg.HighThreshold,
		lowWeight:          cfg.LowWeight,
		normalWeight:       cfg.NormalWeight,
		highWeight:         cfg.HighWeight,
	}
}

func (r *feedbackRepository) effectiveWeightPolicy() feedbackWeightPolicy {
	policy := r.weightPolicy
	if policy.minimumSampleCount < 1 ||
		math.IsNaN(policy.lowThreshold) || math.IsInf(policy.lowThreshold, 0) ||
		math.IsNaN(policy.highThreshold) || math.IsInf(policy.highThreshold, 0) ||
		math.IsNaN(policy.lowWeight) || math.IsInf(policy.lowWeight, 0) ||
		math.IsNaN(policy.normalWeight) || math.IsInf(policy.normalWeight, 0) ||
		math.IsNaN(policy.highWeight) || math.IsInf(policy.highWeight, 0) ||
		policy.lowThreshold < 0 ||
		policy.highThreshold < policy.lowThreshold ||
		policy.highThreshold > 1 ||
		policy.lowWeight <= 0 ||
		policy.normalWeight < policy.lowWeight ||
		policy.highWeight < policy.normalWeight {
		return defaultFeedbackWeightPolicy()
	}
	return policy
}

// NewFeedbackRepository creates a repository for feedback attribution and aggregation.
func NewFeedbackRepository(db *gorm.DB, cfg *config.Config) interfaces.FeedbackRepository {
	var feedbackConfig *config.FeedbackConfig
	if cfg != nil {
		feedbackConfig = cfg.Feedback
	}
	return &feedbackRepository{
		db:           db,
		weightPolicy: feedbackWeightPolicyFromConfig(feedbackConfig),
	}
}

type referenceKey struct {
	tenantID uint64
	kbID     string
	chunkID  string
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

		keys, err := resolveReferenceKeys(tx, references)
		if err != nil {
			return err
		}
		eligible = len(keys) > 0

		if persisted.IsCompleted {
			var existing []types.MessageChunkReference
			if err := tx.Where("message_tenant_id = ? AND message_id = ?", messageTenantID, message.ID).
				Order("chunk_tenant_id, chunk_knowledge_base_id, chunk_id").Find(&existing).Error; err != nil {
				return err
			}
			if len(existing) != len(keys) {
				return ErrFeedbackCompletionState
			}
			for i := range existing {
				if existing[i].ChunkTenantID != keys[i].tenantID ||
					existing[i].ChunkKnowledgeBaseID != keys[i].kbID ||
					existing[i].ChunkID != keys[i].chunkID {
					return ErrFeedbackCompletionState
				}
			}
			return nil
		}

		now := time.Now()
		for _, key := range keys {
			row := &types.MessageChunkReference{
				ID:                   uuid.NewString(),
				MessageTenantID:      messageTenantID,
				ChunkTenantID:        key.tenantID,
				ChunkKnowledgeBaseID: key.kbID,
				MessageID:            message.ID,
				ChunkID:              key.chunkID,
				CreatedAt:            now,
			}
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		result := tx.Model(&types.Message{}).
			Where("id = ? AND session_id = ? AND is_completed = ?", message.ID, message.SessionID, false).
			Updates(map[string]interface{}{
				"content":              message.Content,
				"knowledge_references": references,
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
	return eligible, err
}

func resolveReferenceKeys(tx *gorm.DB, references types.References) ([]referenceKey, error) {
	candidates := make(map[referenceKey]struct{})
	for _, ref := range references {
		if ref == nil || ref.ID == "" || ref.TenantID == 0 || ref.KnowledgeBaseID == "" ||
			ref.ChunkType == string(types.ChunkTypeWebSearch) ||
			ref.KnowledgeSource == "web_search" ||
			ref.MatchType == types.MatchTypeHistory {
			continue
		}
		candidates[referenceKey{
			tenantID: ref.TenantID, kbID: ref.KnowledgeBaseID, chunkID: ref.ID,
		}] = struct{}{}
		if ref.ParentChunkID != "" {
			candidates[referenceKey{
				tenantID: ref.TenantID, kbID: ref.KnowledgeBaseID, chunkID: ref.ParentChunkID,
			}] = struct{}{}
		}
		for _, id := range ref.SubChunkID {
			if id != "" {
				candidates[referenceKey{
					tenantID: ref.TenantID, kbID: ref.KnowledgeBaseID, chunkID: id,
				}] = struct{}{}
			}
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	scopes := make([]referenceKey, 0, len(candidates))
	for scope := range candidates {
		scopes = append(scopes, scope)
	}
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].tenantID != scopes[j].tenantID {
			return scopes[i].tenantID < scopes[j].tenantID
		}
		if scopes[i].kbID != scopes[j].kbID {
			return scopes[i].kbID < scopes[j].kbID
		}
		return scopes[i].chunkID < scopes[j].chunkID
	})

	query := tx.Model(&types.Chunk{})
	for index, scope := range scopes {
		condition := "tenant_id = ? AND knowledge_base_id = ? AND id = ?"
		if index == 0 {
			query = query.Where(condition, scope.tenantID, scope.kbID, scope.chunkID)
		} else {
			query = query.Or(condition, scope.tenantID, scope.kbID, scope.chunkID)
		}
	}
	var chunks []types.Chunk
	// Every feedback lifecycle path locks chunks in canonical scope order.
	if err := query.
		Order("tenant_id, knowledge_base_id, id").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Find(&chunks).Error; err != nil {
		return nil, err
	}
	keys := make([]referenceKey, 0, len(chunks))
	for _, chunk := range chunks {
		keys = append(keys, referenceKey{
			tenantID: chunk.TenantID, kbID: chunk.KnowledgeBaseID, chunkID: chunk.ID,
		})
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].tenantID != keys[j].tenantID {
			return keys[i].tenantID < keys[j].tenantID
		}
		if keys[i].kbID != keys[j].kbID {
			return keys[i].kbID < keys[j].kbID
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
			AND c.knowledge_base_id = mcr.chunk_knowledge_base_id
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
		KBID     string
		ID       string
	}
	type sessionStat struct {
		ChunkTenantID        uint64
		ChunkKnowledgeBaseID string
		ChunkID              string
		SessionCount         int64
	}
	byID := make(map[chunkIdentity]*types.Chunk, len(chunks))
	scopes := make([]chunkIdentity, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		scope := chunkIdentity{TenantID: chunk.TenantID, KBID: chunk.KnowledgeBaseID, ID: chunk.ID}
		scopes = append(scopes, scope)
		byID[scope] = chunk
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
	if len(scopes) == 0 {
		return nil
	}
	var stats []sessionStat
	query := r.db.WithContext(ctx).
		Table("message_chunk_references AS r").
		Select(`r.chunk_tenant_id, r.chunk_knowledge_base_id, r.chunk_id,
			COUNT(DISTINCT m.session_id) AS session_count`).
		Joins("JOIN messages AS m ON m.id = r.message_id AND m.deleted_at IS NULL")
	for index, scope := range scopes {
		condition := "r.chunk_tenant_id = ? AND r.chunk_knowledge_base_id = ? AND r.chunk_id = ?"
		if index == 0 {
			query = query.Where(condition, scope.TenantID, scope.KBID, scope.ID)
		} else {
			query = query.Or(condition, scope.TenantID, scope.KBID, scope.ID)
		}
	}
	if err := query.
		Group("r.chunk_tenant_id, r.chunk_knowledge_base_id, r.chunk_id").
		Scan(&stats).Error; err != nil {
		return err
	}
	for _, stat := range stats {
		scope := chunkIdentity{
			TenantID: stat.ChunkTenantID, KBID: stat.ChunkKnowledgeBaseID, ID: stat.ChunkID,
		}
		if chunk := byID[scope]; chunk != nil {
			chunk.SessionCount = stat.SessionCount
		}
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

		same := hasExisting && existing.FeedbackType == input.Type &&
			reasonCodesEqual(existing.ReasonCode, input.ReasonCode)
		activeEverywhere := hasExisting
		for _, chunk := range chunks {
			activeEverywhere = activeEverywhere &&
				feedbackIsAfterReset(existing.UpdatedAt, chunk.FeedbackResetAt)
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
			if same && activeEverywhere {
				state = &types.MessageFeedbackState{Type: existing.FeedbackType, ReasonCode: existing.ReasonCode}
				return nil
			}
			if hasExisting && !activeEverywhere && existing.FeedbackType == input.Type && !same {
				if err := tx.Model(&types.MessageFeedback{}).
					Where("id = ?", existing.ID).
					UpdateColumn("reason_code", input.ReasonCode).Error; err != nil {
					return err
				}
				existing.ReasonCode = input.ReasonCode
				state = &types.MessageFeedbackState{Type: existing.FeedbackType, ReasonCode: existing.ReasonCode}
				break
			}
			now := feedbackWriteTime(chunks)
			if !hasExisting {
				existing = types.MessageFeedback{
					ID:        uuid.NewString(),
					TenantID:  input.MessageTenantID,
					UserID:    input.ActorUserID,
					SessionID: input.SessionID,
					MessageID: input.MessageID,
					CreatedAt: now,
				}
			}
			existing.FeedbackType = input.Type
			existing.ReasonCode = input.ReasonCode
			existing.UpdatedAt = now
			if hasExisting {
				if err := tx.Model(&types.MessageFeedback{}).
					Where("id = ?", existing.ID).
					UpdateColumns(map[string]interface{}{
						"feedback_type": existing.FeedbackType,
						"reason_code":   existing.ReasonCode,
						"updated_at":    existing.UpdatedAt,
					}).Error; err != nil {
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
			tx, keys, r.effectiveWeightPolicy(),
			input.ActorTenantID, input.ActorUserID, feedbackTriggerSource(input.Type),
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

func feedbackIsAfterReset(updatedAt time.Time, resetAt *time.Time) bool {
	return resetAt == nil || updatedAt.After(resetAt.UTC())
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
		Order("chunk_tenant_id, chunk_knowledge_base_id, chunk_id").Find(&refs).Error; err != nil {
		return nil, nil, err
	}
	keys := make([]referenceKey, 0, len(refs))
	chunks := make([]types.Chunk, 0, len(refs))
	staleReferenceIDs := make([]string, 0)
	for _, ref := range refs {
		var chunk types.Chunk
		if err := tx.Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
			ref.ChunkTenantID, ref.ChunkKnowledgeBaseID, ref.ChunkID,
		).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&chunk).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				staleReferenceIDs = append(staleReferenceIDs, ref.ID)
				continue
			}
			return nil, nil, err
		}
		keys = append(keys, referenceKey{
			tenantID: ref.ChunkTenantID, kbID: ref.ChunkKnowledgeBaseID, chunkID: ref.ChunkID,
		})
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
	policy feedbackWeightPolicy,
	actorTenantID uint64,
	actorUserID string,
	triggerSource types.FeedbackTriggerSource,
) error {
	for _, key := range keys {
		var chunk types.Chunk
		if err := tx.Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id = ?", key.tenantID, key.kbID, key.chunkID,
		).
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
			Where(
				`mcr.chunk_tenant_id = ? AND mcr.chunk_knowledge_base_id = ? AND mcr.chunk_id = ?`,
				key.tenantID, key.kbID, key.chunkID,
			)
		if chunk.FeedbackResetAt != nil {
			query = query.Where("mf.updated_at > ?", chunk.FeedbackResetAt.UTC())
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
		weight := policy.normalWeight
		if total := likes + dislikes; total > 0 {
			rate := float64(likes) / float64(total)
			positiveRate = &rate
			if total >= policy.minimumSampleCount {
				switch {
				case rate >= policy.highThreshold:
					weight = policy.highWeight
				case rate < policy.lowThreshold:
					weight = policy.lowWeight
				}
			}
		}
		oldWeight := chunk.RecallWeight
		if oldWeight <= 0 {
			oldWeight = 1
		}
		if err := tx.Model(&types.Chunk{}).
			Where(
				"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
				key.tenantID, key.kbID, key.chunkID,
			).
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
			UpdatedAt time.Time
		}
		if err := tx.Table("message_feedbacks AS mf").
			Joins(messageFeedbackReferenceJoin).
			Where(
				`mcr.chunk_tenant_id = ? AND mcr.chunk_knowledge_base_id = ? AND mcr.chunk_id = ?`,
				input.ChunkTenantID, input.KnowledgeBaseID, input.ChunkID,
			).
			Select("mf.updated_at").
			Order("mf.updated_at DESC").
			Limit(1).
			Scan(&latest).Error; err != nil {
			return err
		}
		resetAt := time.Now().UTC()
		if !latest.UpdatedAt.IsZero() && !resetAt.After(latest.UpdatedAt.UTC()) {
			resetAt = latest.UpdatedAt.UTC().Add(time.Microsecond)
		}
		if chunk.FeedbackResetAt != nil && !resetAt.After(chunk.FeedbackResetAt.UTC()) {
			resetAt = chunk.FeedbackResetAt.UTC().Add(time.Microsecond)
		}
		oldWeight := chunk.RecallWeight
		if oldWeight <= 0 {
			oldWeight = 1
		}
		if err := tx.Model(&types.Chunk{}).
			Where(
				"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
				input.ChunkTenantID, input.KnowledgeBaseID, input.ChunkID,
			).
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
		Where(
			`mcr.chunk_tenant_id = ? AND mcr.chunk_knowledge_base_id = ?
				AND mcr.chunk_id = ? AND mf.feedback_type = ?`,
			tenantID, chunk.KnowledgeBaseID, chunkID, types.FeedbackTypeDislike,
		).
		Where("mf.reason_code IS NOT NULL")
	if chunk.FeedbackResetAt != nil {
		query = query.Where("mf.updated_at > ?", chunk.FeedbackResetAt.UTC())
	}
	if err := query.Group("mf.reason_code").Scan(&reasons).Error; err != nil {
		return nil, err
	}
	result := &types.ChunkFeedbackDetails{ReasonCounts: make(map[types.FeedbackReasonCode]int64)}
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
			tx, tenantID, []string{messageID}, actorUserID, r.effectiveWeightPolicy(),
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
			tx, tenantID, ids, actorUserID, r.effectiveWeightPolicy(),
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
	policy feedbackWeightPolicy,
) error {
	if len(messageIDs) == 0 {
		return nil
	}
	var refs []types.MessageChunkReference
	if err := tx.Where("message_tenant_id = ? AND message_id IN ?", actorTenantID, messageIDs).
		Order("chunk_tenant_id, chunk_knowledge_base_id, chunk_id").Find(&refs).Error; err != nil {
		return err
	}
	seen := make(map[referenceKey]struct{})
	keys := make([]referenceKey, 0, len(refs))
	for _, ref := range refs {
		key := referenceKey{
			tenantID: ref.ChunkTenantID, kbID: ref.ChunkKnowledgeBaseID, chunkID: ref.ChunkID,
		}
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		if err := tx.Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
			key.tenantID, key.kbID, key.chunkID,
		).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&types.Chunk{}).Error; err != nil &&
			!errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if err := tx.Where(
		"tenant_id = ? AND message_id IN ?", actorTenantID, messageIDs,
	).Delete(&types.MessageFeedback{}).Error; err != nil {
		return err
	}
	if err := tx.Where(
		"message_tenant_id = ? AND message_id IN ?", actorTenantID, messageIDs,
	).Delete(&types.MessageChunkReference{}).Error; err != nil {
		return err
	}
	if err := tx.Where("id IN ?", messageIDs).Delete(&types.Message{}).Error; err != nil {
		return err
	}
	return recomputeChunks(
		tx, keys, policy,
		actorTenantID, actorUserID, types.FeedbackTriggerContentDelete,
	)
}
