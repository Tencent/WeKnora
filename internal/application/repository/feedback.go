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
	db *gorm.DB
}

// NewFeedbackRepository creates a repository for feedback attribution and aggregation.
func NewFeedbackRepository(db *gorm.DB) interfaces.FeedbackRepository {
	return &feedbackRepository{db: db}
}

type referenceKey struct {
	tenantID uint64
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
	if err := tx.Where("id IN ?", ids).
		Order("id").
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
		Where("mcr.message_tenant_id = ? AND mcr.message_id IN ?", tenantID, ids).
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
		if message == nil || message.Role != "assistant" || !message.IsCompleted || userID == "" {
			continue
		}
		message.FeedbackEligible = eligible[message.ID]
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
				s.tenant_id = ? AND (s.user_id = ? OR s.user_id IS NULL OR s.user_id = '')`,
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
		weight := 1.0
		if total := likes + dislikes; total > 0 {
			rate := float64(likes) / float64(total)
			positiveRate = &rate
			switch {
			case rate >= 0.8:
				weight = 1.2
			case rate < 0.5:
				weight = 0.8
			}
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
			UpdatedAt time.Time
		}
		if err := tx.Table("message_feedbacks AS mf").
			Joins(messageFeedbackReferenceJoin).
			Where("mcr.chunk_tenant_id = ? AND mcr.chunk_id = ?", input.ChunkTenantID, input.ChunkID).
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
		return deleteMessagesAndRecompute(tx, tenantID, []string{messageID}, actorUserID)
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
		if err := deleteMessagesAndRecompute(tx, tenantID, ids, actorUserID); err != nil {
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
	)
}
