package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
)

func setupFeedbackTestRepository(
	t *testing.T,
) (*feedbackRepository, *gorm.DB, *types.Session, *types.Message, *types.Chunk) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.Exec(`
		CREATE TABLE sessions (
			id text PRIMARY KEY,
			tenant_id integer NOT NULL,
			user_id text,
			deleted_at datetime
		);
		CREATE TABLE messages (
			id text PRIMARY KEY,
			session_id text NOT NULL,
			content text,
			role text,
			knowledge_references json,
			agent_steps json,
			is_completed numeric NOT NULL DEFAULT 0,
			is_fallback numeric NOT NULL DEFAULT 0,
			created_at datetime,
			updated_at datetime,
			deleted_at datetime
		);
		CREATE TABLE knowledges (
			id text PRIMARY KEY,
			tenant_id integer NOT NULL,
			knowledge_base_id text NOT NULL,
			title text,
			deleted_at datetime
		);
	`).Error)
	require.NoError(t, db.AutoMigrate(
		&types.Chunk{},
		&types.MessageChunkReference{},
		&types.MessageFeedback{},
		&types.ChunkFeedbackAudit{},
	))
	session := &types.Session{ID: "session-a", TenantID: 101, UserID: "user-a"}
	require.NoError(t, db.Exec(
		"INSERT INTO sessions (id, tenant_id, user_id) VALUES (?, ?, ?)",
		session.ID, session.TenantID, session.UserID,
	).Error)
	message := &types.Message{
		ID: "message-a", SessionID: session.ID, Role: "assistant", Content: "draft",
	}
	require.NoError(t, db.Exec(
		"INSERT INTO messages (id, session_id, content, role, is_completed) VALUES (?, ?, ?, ?, ?)",
		message.ID, message.SessionID, message.Content, message.Role, false,
	).Error)
	chunk := &types.Chunk{
		ID: "chunk-a", TenantID: 202, KnowledgeBaseID: "kb-a", KnowledgeID: "knowledge-a",
		Content: "source", SourceContent: "source", RecallWeight: 1, IsEnabled: true,
	}
	require.NoError(t, db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, title) VALUES (?, ?, ?, ?)",
		chunk.KnowledgeID, chunk.TenantID, chunk.KnowledgeBaseID, "Document A",
	).Error)
	require.NoError(t, db.Create(chunk).Error)
	feedbackConfig := config.DefaultFeedbackConfig()
	// These repository lifecycle fixtures intentionally exercise every weight
	// transition with one vote. Production keeps the safer five-sample default.
	feedbackConfig.MinimumSampleCount = 1
	return &feedbackRepository{db: db, feedbackConfig: feedbackConfig}, db, session, message, chunk
}

func feedbackReference(chunk *types.Chunk) types.References {
	return types.References{&types.SearchResult{
		ID: chunk.ID, KnowledgeBaseID: chunk.KnowledgeBaseID, ChunkType: types.ChunkTypeText,
	}}
}

func feedbackReferences(chunks ...*types.Chunk) types.References {
	references := make(types.References, 0, len(chunks))
	for _, chunk := range chunks {
		references = append(references, &types.SearchResult{
			ID: chunk.ID, KnowledgeBaseID: chunk.KnowledgeBaseID, ChunkType: types.ChunkTypeText,
		})
	}
	return references
}

func loadFeedbackChunk(t *testing.T, db *gorm.DB, id string) types.Chunk {
	t.Helper()
	var chunk types.Chunk
	require.NoError(t, db.First(&chunk, "id = ?", id).Error)
	return chunk
}

func feedbackFloat64Pointer(value float64) *float64 {
	return &value
}

func TestListChunkFeedbackStatsUsesCompleteScopeAndDeduplicatesInput(t *testing.T) {
	repo, db, _, _, chunk := setupFeedbackTestRepository(t)
	other := &types.Chunk{
		ID: "chunk-b", TenantID: chunk.TenantID, KnowledgeBaseID: "kb-b", KnowledgeID: "knowledge-b",
		Content: "other", SourceContent: "other", RecallWeight: 0.8, LikeCount: 1, DislikeCount: 4, IsEnabled: true,
	}
	require.NoError(t, db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, title) VALUES (?, ?, ?, ?)",
		other.KnowledgeID, other.TenantID, other.KnowledgeBaseID, "Document B",
	).Error)
	require.NoError(t, db.Create(other).Error)

	scopes := []types.ChunkFeedbackScope{
		{TenantID: chunk.TenantID, KnowledgeBaseID: chunk.KnowledgeBaseID, ChunkID: chunk.ID},
		{TenantID: chunk.TenantID, KnowledgeBaseID: chunk.KnowledgeBaseID, ChunkID: chunk.ID},
		{TenantID: other.TenantID, KnowledgeBaseID: other.KnowledgeBaseID, ChunkID: other.ID},
		{TenantID: other.TenantID, KnowledgeBaseID: chunk.KnowledgeBaseID, ChunkID: other.ID},
	}
	stats, err := repo.ListChunkFeedbackStats(context.Background(), scopes)
	require.NoError(t, err)
	require.Len(t, stats, 2)
	assert.Equal(t, chunk.ID, stats[0].ChunkID)
	assert.Equal(t, other.ID, stats[1].ChunkID)
	assert.EqualValues(t, 4, stats[1].DislikeCount)
}

func TestChunkFeedbackGovernanceListAndHistoryStayWithinKBScope(t *testing.T) {
	repo, db, session, message, chunk := setupFeedbackTestRepository(t)
	ctx := context.Background()
	_, err := repo.CompleteAssistantMessageWithReferences(ctx, session.TenantID, message, feedbackReference(chunk))
	require.NoError(t, err)
	message.IsCompleted = true
	_, err = repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
		MessageTenantID: session.TenantID,
		ActorTenantID:   session.TenantID,
		ActorUserID:     session.UserID,
		SessionID:       session.ID,
		MessageID:       message.ID,
		Type:            types.FeedbackTypeLike,
	})
	require.NoError(t, err)

	other := &types.Chunk{
		ID: "chunk-b", TenantID: chunk.TenantID, KnowledgeBaseID: "kb-b", KnowledgeID: "knowledge-b",
		Content: "must not leak", SourceContent: "must not leak", RecallWeight: 1, IsEnabled: true,
	}
	require.NoError(t, db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, title) VALUES (?, ?, ?, ?)",
		other.KnowledgeID, other.TenantID, other.KnowledgeBaseID, "Document B",
	).Error)
	require.NoError(t, db.Create(other).Error)

	query := &types.ChunkFeedbackListQuery{
		Page: 1, PageSize: 20, FeedbackStatus: types.ChunkFeedbackStatusRated,
		SortBy: "like_count", SortOrder: "desc",
	}
	require.NoError(t, query.Validate())
	items, total, err := repo.ListChunkFeedbackGovernance(
		ctx, chunk.TenantID, chunk.KnowledgeBaseID, query,
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, chunk.ID, items[0].ChunkID)
	assert.Equal(t, "Document A", items[0].KnowledgeTitle)
	assert.EqualValues(t, 1, items[0].SessionCount)

	require.NoError(t, repo.ResetChunkFeedback(ctx, types.ResetChunkFeedbackInput{
		ChunkTenantID: chunk.TenantID, ActorTenantID: session.TenantID,
		ActorUserID: session.UserID, KnowledgeBaseID: chunk.KnowledgeBaseID, ChunkID: chunk.ID,
	}))
	history, historyTotal, err := repo.ListChunkFeedbackHistory(
		ctx,
		chunk.TenantID,
		chunk.KnowledgeBaseID,
		chunk.ID,
		&types.Pagination{Page: 1, PageSize: 20},
	)
	require.NoError(t, err)
	require.EqualValues(t, 2, historyTotal)
	require.Len(t, history, 2)
	assert.Equal(t, types.ChunkFeedbackAuditActionReset, history[0].Action)

	_, _, err = repo.ListChunkFeedbackHistory(
		ctx,
		chunk.TenantID,
		other.KnowledgeBaseID,
		chunk.ID,
		&types.Pagination{Page: 1, PageSize: 20},
	)
	assert.ErrorIs(t, err, ErrFeedbackChunkNotFound)
}

func TestChunkFeedbackGovernanceSortsByCurrentEffectiveWeightTier(t *testing.T) {
	repo, db, _, _, neutral := setupFeedbackTestRepository(t)
	repo.feedbackConfig.MinimumSampleCount = 5

	neutral.LikeCount = 1
	neutral.DislikeCount = 0
	neutral.PositiveRate = feedbackFloat64Pointer(1)
	require.NoError(t, db.Save(neutral).Error)

	high := &types.Chunk{
		ID: "chunk-high", TenantID: neutral.TenantID, KnowledgeBaseID: neutral.KnowledgeBaseID,
		KnowledgeID: "knowledge-high", Content: "high", SourceContent: "high",
		RecallWeight: 1, LikeCount: 4, DislikeCount: 1, PositiveRate: feedbackFloat64Pointer(0.8), IsEnabled: true,
	}
	low := &types.Chunk{
		ID: "chunk-low", TenantID: neutral.TenantID, KnowledgeBaseID: neutral.KnowledgeBaseID,
		KnowledgeID: "knowledge-low", Content: "low", SourceContent: "low",
		RecallWeight: 1, LikeCount: 0, DislikeCount: 5, PositiveRate: feedbackFloat64Pointer(0), IsEnabled: true,
	}
	for _, item := range []*types.Chunk{high, low} {
		require.NoError(t, db.Exec(
			"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, title) VALUES (?, ?, ?, ?)",
			item.KnowledgeID, item.TenantID, item.KnowledgeBaseID, item.ID,
		).Error)
		require.NoError(t, db.Create(item).Error)
	}

	query := &types.ChunkFeedbackListQuery{
		Page: 1, PageSize: 20, SortBy: "effective_recall_weight", SortOrder: "desc",
	}
	items, total, err := repo.ListChunkFeedbackGovernance(
		context.Background(), neutral.TenantID, neutral.KnowledgeBaseID, query,
	)
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	require.Len(t, items, 3)
	assert.Equal(t, []string{high.ID, neutral.ID, low.ID}, []string{
		items[0].ChunkID, items[1].ChunkID, items[2].ChunkID,
	})
}

func TestHydrateMessagesRequiresAnActiveChunkReference(t *testing.T) {
	repo, db, session, message, chunk := setupFeedbackTestRepository(t)
	ctx := context.Background()
	_, err := repo.CompleteAssistantMessageWithReferences(
		ctx, session.TenantID, message, feedbackReference(chunk),
	)
	require.NoError(t, err)
	message.IsCompleted = true

	require.NoError(t, repo.HydrateMessages(
		ctx, session.TenantID, session.UserID, []*types.Message{message},
	))
	assert.True(t, message.FeedbackEligible)

	// Preserve the reference to reproduce a legacy dangling association.
	require.NoError(t, db.Where("tenant_id = ? AND id = ?", chunk.TenantID, chunk.ID).
		Delete(&types.Chunk{}).Error)
	var referenceCount int64
	require.NoError(t, db.Model(&types.MessageChunkReference{}).
		Where("message_id = ?", message.ID).Count(&referenceCount).Error)
	require.EqualValues(t, 1, referenceCount)

	require.NoError(t, repo.HydrateMessages(
		ctx, session.TenantID, session.UserID, []*types.Message{message},
	))
	assert.False(t, message.FeedbackEligible)
}

func TestFeedbackRequiresExactSessionOwner(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		sessionSQL string
		actor      string
		allowed    bool
	}{
		{name: "exact owner", actor: "user-a", allowed: true},
		{name: "same tenant non-owner", actor: "user-b"},
		{name: "empty owner", sessionSQL: "UPDATE sessions SET user_id = ''", actor: "user-a"},
		{name: "null owner", sessionSQL: "UPDATE sessions SET user_id = NULL", actor: "user-a"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo, db, session, message, chunk := setupFeedbackTestRepository(t)
			if testCase.sessionSQL != "" {
				require.NoError(t, db.Exec(testCase.sessionSQL).Error)
			}
			_, err := repo.CompleteAssistantMessageWithReferences(
				context.Background(), session.TenantID, message, feedbackReference(chunk),
			)
			require.NoError(t, err)

			_, err = repo.ApplyMessageFeedback(context.Background(), types.ApplyMessageFeedbackInput{
				MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
				ActorUserID: testCase.actor, SessionID: session.ID, MessageID: message.ID,
				Type: types.FeedbackTypeLike,
			})
			if testCase.allowed {
				require.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, ErrFeedbackMessageNotFound)
			}
		})
	}
}

func TestHydrateMessagesRequiresExactSessionOwner(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		sessionSQL string
		viewer     string
		eligible   bool
	}{
		{name: "exact owner", viewer: "user-a", eligible: true},
		{name: "same tenant non-owner", viewer: "user-b"},
		{name: "empty owner", sessionSQL: "UPDATE sessions SET user_id = ''", viewer: "user-a"},
		{name: "null owner", sessionSQL: "UPDATE sessions SET user_id = NULL", viewer: "user-a"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo, db, session, message, chunk := setupFeedbackTestRepository(t)
			if testCase.sessionSQL != "" {
				require.NoError(t, db.Exec(testCase.sessionSQL).Error)
			}
			_, err := repo.CompleteAssistantMessageWithReferences(
				context.Background(), session.TenantID, message, feedbackReference(chunk),
			)
			require.NoError(t, err)
			message.IsCompleted = true

			require.NoError(t, repo.HydrateMessages(
				context.Background(), session.TenantID, testCase.viewer, []*types.Message{message},
			))
			assert.Equal(t, testCase.eligible, message.FeedbackEligible)
		})
	}
}

func TestAgentCanonicalCompletionFiltersScopeDeduplicatesAndIsIdempotent(t *testing.T) {
	repo, db, session, message, chunk := setupFeedbackTestRepository(t)
	ctx := context.Background()
	other := &types.Chunk{
		ID: "chunk-cross", TenantID: 303, KnowledgeBaseID: "kb-cross", KnowledgeID: "knowledge-cross",
		Content: "cross", SourceContent: "cross", RecallWeight: 1, IsEnabled: true,
	}
	require.NoError(t, db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, title) VALUES (?, ?, ?, ?)",
		other.KnowledgeID, other.TenantID, other.KnowledgeBaseID, "Cross tenant",
	).Error)
	require.NoError(t, db.Create(other).Error)

	message.Content = "agent answer"
	message.KnowledgeReferences = types.References{
		{ID: chunk.ID, KnowledgeBaseID: chunk.KnowledgeBaseID, ChunkType: types.ChunkTypeText},
		{ID: chunk.ID, KnowledgeBaseID: chunk.KnowledgeBaseID, ChunkType: types.ChunkTypeText},
		{ID: other.ID, KnowledgeBaseID: other.KnowledgeBaseID, ChunkType: types.ChunkTypeText},
	}
	message.CanonicalChunkReferencesSet = true
	message.CanonicalChunkReferences = []types.ChunkFeedbackScope{
		{TenantID: chunk.TenantID, KnowledgeBaseID: chunk.KnowledgeBaseID, ChunkID: chunk.ID},
		{TenantID: chunk.TenantID, KnowledgeBaseID: chunk.KnowledgeBaseID, ChunkID: chunk.ID},
		// The tool result claims an unauthorized tenant for this chunk. Exact
		// tenant+KB+chunk validation must filter it.
		{TenantID: 999, KnowledgeBaseID: other.KnowledgeBaseID, ChunkID: other.ID},
	}

	eligible, err := repo.CompleteAssistantMessageWithReferences(
		ctx, session.TenantID, message, message.KnowledgeReferences,
	)
	require.NoError(t, err)
	assert.True(t, eligible)

	var stored []types.MessageChunkReference
	require.NoError(t, db.Where("message_id = ?", message.ID).Find(&stored).Error)
	require.Len(t, stored, 1)
	assert.Equal(t, chunk.TenantID, stored[0].ChunkTenantID)
	assert.Equal(t, chunk.ID, stored[0].ChunkID)

	var persisted types.Message
	require.NoError(t, db.First(&persisted, "id = ?", message.ID).Error)
	require.Len(t, persisted.KnowledgeReferences, 1)
	assert.Equal(t, chunk.ID, persisted.KnowledgeReferences[0].ID)

	// Reordered and duplicate canonical input is the same immutable completion.
	message.KnowledgeReferences = types.References{
		{ID: chunk.ID, KnowledgeBaseID: chunk.KnowledgeBaseID, ChunkType: types.ChunkTypeText},
	}
	message.CanonicalChunkReferences = []types.ChunkFeedbackScope{
		{TenantID: chunk.TenantID, KnowledgeBaseID: chunk.KnowledgeBaseID, ChunkID: chunk.ID},
	}
	eligible, err = repo.CompleteAssistantMessageWithReferences(
		ctx, session.TenantID, message, message.KnowledgeReferences,
	)
	require.NoError(t, err)
	assert.True(t, eligible)
	require.NoError(t, db.Where("message_id = ?", message.ID).Find(&stored).Error)
	assert.Len(t, stored, 1)
}

func TestAgentWebOnlyCompletionIsNotFeedbackEligible(t *testing.T) {
	repo, db, session, _, _ := setupFeedbackTestRepository(t)
	message := &types.Message{
		ID: "message-web", SessionID: session.ID, Role: "assistant", Content: "web answer",
		KnowledgeReferences: types.References{{
			ID: "web-result", ChunkType: string(types.ChunkTypeWebSearch), KnowledgeSource: "web_search",
		}},
		CanonicalChunkReferencesSet: true,
	}
	require.NoError(t, db.Exec(
		"INSERT INTO messages (id, session_id, content, role, is_completed) VALUES (?, ?, ?, ?, ?)",
		message.ID, message.SessionID, "draft", message.Role, false,
	).Error)

	eligible, err := repo.CompleteAssistantMessageWithReferences(
		context.Background(), session.TenantID, message, message.KnowledgeReferences,
	)
	require.NoError(t, err)
	assert.False(t, eligible)

	var count int64
	require.NoError(t, db.Model(&types.MessageChunkReference{}).
		Where("message_id = ?", message.ID).Count(&count).Error)
	assert.Zero(t, count)
}

func TestAgentCanonicalCompletionRollsBackMessageWhenReferenceInsertFails(t *testing.T) {
	repo, db, session, message, chunk := setupFeedbackTestRepository(t)
	require.NoError(t, db.Exec(`
		CREATE TRIGGER reject_agent_reference
		BEFORE INSERT ON message_chunk_references
		BEGIN SELECT RAISE(ABORT, 'agent reference insert failed'); END;
	`).Error)
	message.Content = "agent answer"
	message.KnowledgeReferences = feedbackReference(chunk)
	message.CanonicalChunkReferencesSet = true
	message.CanonicalChunkReferences = []types.ChunkFeedbackScope{{
		TenantID:        chunk.TenantID,
		KnowledgeBaseID: chunk.KnowledgeBaseID,
		ChunkID:         chunk.ID,
	}}

	eligible, err := repo.CompleteAssistantMessageWithReferences(
		context.Background(), session.TenantID, message, message.KnowledgeReferences,
	)
	require.ErrorContains(t, err, "agent reference insert failed")
	assert.False(t, eligible)

	var persisted types.Message
	require.NoError(t, db.First(&persisted, "id = ?", message.ID).Error)
	assert.False(t, persisted.IsCompleted)
	assert.Equal(t, "draft", persisted.Content)
	var referenceCount int64
	require.NoError(t, db.Model(&types.MessageChunkReference{}).
		Where("message_id = ?", message.ID).Count(&referenceCount).Error)
	assert.Zero(t, referenceCount)
}

func TestFeedbackLifecycleAndResetBaseline(t *testing.T) {
	repo, db, session, message, chunk := setupFeedbackTestRepository(t)
	ctx := context.Background()
	message.Content = "final"

	eligible, err := repo.CompleteAssistantMessageWithReferences(ctx, session.TenantID, message, feedbackReference(chunk))
	require.NoError(t, err)
	assert.True(t, eligible)

	// The exact same completion is idempotent; attribution cannot later drift.
	eligible, err = repo.CompleteAssistantMessageWithReferences(ctx, session.TenantID, message, feedbackReference(chunk))
	require.NoError(t, err)
	assert.True(t, eligible)
	_, err = repo.CompleteAssistantMessageWithReferences(ctx, session.TenantID, message, nil)
	assert.ErrorIs(t, err, ErrFeedbackCompletionState)

	state, err := repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
		MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
		ActorUserID: "user-a", SessionID: session.ID, MessageID: message.ID, Type: types.FeedbackTypeLike,
	})
	require.NoError(t, err)
	assert.Equal(t, types.FeedbackTypeLike, state.Type)
	got := loadFeedbackChunk(t, db, chunk.ID)
	assert.EqualValues(t, 1, got.LikeCount)
	assert.Equal(t, 1.2, got.RecallWeight)

	reason := types.FeedbackReasonInaccurate
	_, err = repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
		MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
		ActorUserID: "user-a", SessionID: session.ID, MessageID: message.ID,
		Type: types.FeedbackTypeDislike, ReasonCode: &reason,
	})
	require.NoError(t, err)
	got = loadFeedbackChunk(t, db, chunk.ID)
	assert.Zero(t, got.LikeCount)
	assert.EqualValues(t, 1, got.DislikeCount)
	assert.Equal(t, 0.8, got.RecallWeight)

	require.NoError(t, repo.ResetChunkFeedback(ctx, types.ResetChunkFeedbackInput{
		ChunkTenantID: chunk.TenantID, ActorTenantID: session.TenantID,
		ActorUserID: "admin", KnowledgeBaseID: chunk.KnowledgeBaseID, ChunkID: chunk.ID,
	}))
	got = loadFeedbackChunk(t, db, chunk.ID)
	assert.Zero(t, got.LikeCount)
	assert.Zero(t, got.DislikeCount)
	assert.Nil(t, got.PositiveRate)
	assert.Equal(t, 1.0, got.RecallWeight)

	var resetFeedback types.MessageFeedback
	require.NoError(t, db.Where("message_id = ?", message.ID).First(&resetFeedback).Error)

	// Re-submitting the same pre-reset rating is a no-op and remains excluded.
	_, err = repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
		MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
		ActorUserID: "user-a", SessionID: session.ID, MessageID: message.ID,
		Type: types.FeedbackTypeDislike, ReasonCode: &reason,
	})
	require.NoError(t, err)
	got = loadFeedbackChunk(t, db, chunk.ID)
	assert.Zero(t, got.LikeCount)
	assert.Zero(t, got.DislikeCount)

	// A reason-only edit updates metadata but not the rating-event clock.
	otherReason := types.FeedbackReasonOutdated
	_, err = repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
		MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
		ActorUserID: "user-a", SessionID: session.ID, MessageID: message.ID,
		Type: types.FeedbackTypeDislike, ReasonCode: &otherReason,
	})
	require.NoError(t, err)
	var reasonUpdated types.MessageFeedback
	require.NoError(t, db.Where("message_id = ?", message.ID).First(&reasonUpdated).Error)
	assert.Equal(t, resetFeedback.FeedbackAt, reasonUpdated.FeedbackAt)
	assert.True(t, reasonUpdated.UpdatedAt.After(resetFeedback.UpdatedAt) ||
		reasonUpdated.UpdatedAt.Equal(resetFeedback.UpdatedAt))
	got = loadFeedbackChunk(t, db, chunk.ID)
	assert.Zero(t, got.LikeCount)
	assert.Zero(t, got.DislikeCount)

	// An explicit direction switch after reset is a new rating event.
	_, err = repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
		MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
		ActorUserID: "user-a", SessionID: session.ID, MessageID: message.ID, Type: types.FeedbackTypeLike,
	})
	require.NoError(t, err)
	got = loadFeedbackChunk(t, db, chunk.ID)
	assert.EqualValues(t, 1, got.LikeCount)
	assert.Zero(t, got.DislikeCount)
	assert.Equal(t, 1.2, got.RecallWeight)

	require.NoError(t, repo.DeleteMessageWithFeedback(ctx, session.TenantID, session.ID, message.ID, "user-a"))
	got = loadFeedbackChunk(t, db, chunk.ID)
	assert.Zero(t, got.LikeCount)
	assert.Zero(t, got.DislikeCount)
	assert.Equal(t, 1.0, got.RecallWeight)
	details, err := repo.GetChunkFeedbackDetails(ctx, chunk.TenantID, chunk.ID)
	require.NoError(t, err)
	require.NotEmpty(t, details.Audits)
	assert.Equal(t, types.FeedbackTriggerContentDelete, details.Audits[0].TriggerSource)
}

func TestFeedbackTransactionRollsBackOnAuditFailure(t *testing.T) {
	repo, db, session, message, chunk := setupFeedbackTestRepository(t)
	ctx := context.Background()
	_, err := repo.CompleteAssistantMessageWithReferences(ctx, session.TenantID, message, feedbackReference(chunk))
	require.NoError(t, err)
	require.NoError(t, db.Exec(`
		CREATE TRIGGER reject_feedback_audit
		BEFORE INSERT ON chunk_feedback_audits
		BEGIN SELECT RAISE(ABORT, 'audit failure'); END;
	`).Error)

	_, err = repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
		MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
		ActorUserID: "user-a", SessionID: session.ID, MessageID: message.ID, Type: types.FeedbackTypeLike,
	})
	require.Error(t, err)

	var count int64
	require.NoError(t, db.Model(&types.MessageFeedback{}).Count(&count).Error)
	assert.Zero(t, count)
	got := loadFeedbackChunk(t, db, chunk.ID)
	assert.Zero(t, got.LikeCount)
	assert.Equal(t, 1.0, got.RecallWeight)
}

func TestFeedbackAuditTriggerSourcesAndIdempotency(t *testing.T) {
	repo, _, session, message, chunk := setupFeedbackTestRepository(t)
	ctx := context.Background()
	_, err := repo.CompleteAssistantMessageWithReferences(ctx, session.TenantID, message, feedbackReference(chunk))
	require.NoError(t, err)

	apply := func(feedbackType types.FeedbackType) {
		t.Helper()
		_, applyErr := repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
			MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
			ActorUserID: "user-a", SessionID: session.ID, MessageID: message.ID, Type: feedbackType,
		})
		require.NoError(t, applyErr)
	}
	auditSources := func() []types.FeedbackTriggerSource {
		t.Helper()
		details, detailsErr := repo.GetChunkFeedbackDetails(ctx, chunk.TenantID, chunk.ID)
		require.NoError(t, detailsErr)
		sources := make([]types.FeedbackTriggerSource, 0, len(details.Audits))
		for i := len(details.Audits) - 1; i >= 0; i-- {
			sources = append(sources, details.Audits[i].TriggerSource)
		}
		return sources
	}

	apply(types.FeedbackTypeLike)
	assert.Equal(t, []types.FeedbackTriggerSource{types.FeedbackTriggerLike}, auditSources())

	apply(types.FeedbackTypeLike)
	assert.Equal(t, []types.FeedbackTriggerSource{types.FeedbackTriggerLike}, auditSources(),
		"idempotent feedback must not create another weight-change audit")

	apply(types.FeedbackTypeDislike)
	apply(types.FeedbackTypeNone)
	require.NoError(t, repo.ResetChunkFeedback(ctx, types.ResetChunkFeedbackInput{
		ChunkTenantID: chunk.TenantID, ActorTenantID: session.TenantID,
		ActorUserID: "admin", KnowledgeBaseID: chunk.KnowledgeBaseID, ChunkID: chunk.ID,
	}))
	assert.Equal(t, []types.FeedbackTriggerSource{
		types.FeedbackTriggerLike,
		types.FeedbackTriggerDislike,
		types.FeedbackTriggerCancel,
		types.FeedbackTriggerAdminReset,
	}, auditSources())
	details, err := repo.GetChunkFeedbackDetails(ctx, chunk.TenantID, chunk.ID)
	require.NoError(t, err)
	require.Len(t, details.Audits, 4)
	for _, audit := range details.Audits[1:] {
		assert.Equal(t, types.ChunkFeedbackAuditActionWeightChanged, audit.Action)
	}
	assert.Equal(t, types.ChunkFeedbackAuditActionReset, details.Audits[0].Action)
}

func TestCompletionExcludesWebOnlyReferences(t *testing.T) {
	repo, db, session, message, _ := setupFeedbackTestRepository(t)
	eligible, err := repo.CompleteAssistantMessageWithReferences(
		context.Background(), session.TenantID, message,
		types.References{&types.SearchResult{ID: "web-result", ChunkType: types.ChunkTypeWebSearch}},
	)
	require.NoError(t, err)
	assert.False(t, eligible)

	var count int64
	require.NoError(t, db.Model(&types.MessageChunkReference{}).Count(&count).Error)
	assert.Zero(t, count)
	_, err = repo.ApplyMessageFeedback(context.Background(), types.ApplyMessageFeedbackInput{
		MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
		ActorUserID: "user-a", SessionID: session.ID, MessageID: message.ID, Type: types.FeedbackTypeLike,
	})
	assert.True(t, errors.Is(err, ErrFeedbackNotEligible))
}

func TestCompletionPersistsCanonicalPrimaryParentAndSubchunks(t *testing.T) {
	repo, db, session, message, primary := setupFeedbackTestRepository(t)
	parent := &types.Chunk{
		ID: "chunk-parent", TenantID: primary.TenantID, KnowledgeBaseID: primary.KnowledgeBaseID,
		KnowledgeID: primary.KnowledgeID, Content: "parent", SourceContent: "parent",
		RecallWeight: 1, IsEnabled: true,
	}
	sub := &types.Chunk{
		ID: "chunk-sub", TenantID: primary.TenantID, KnowledgeBaseID: primary.KnowledgeBaseID,
		KnowledgeID: primary.KnowledgeID, Content: "sub", SourceContent: "sub",
		RecallWeight: 1, IsEnabled: true,
	}
	history := &types.Chunk{
		ID: "chunk-history", TenantID: primary.TenantID, KnowledgeBaseID: primary.KnowledgeBaseID,
		KnowledgeID: primary.KnowledgeID, Content: "history", SourceContent: "history",
		RecallWeight: 1, IsEnabled: true,
	}
	require.NoError(t, db.Create([]*types.Chunk{parent, sub, history}).Error)

	eligible, err := repo.CompleteAssistantMessageWithReferences(
		context.Background(), session.TenantID, message,
		types.References{
			&types.SearchResult{
				ID: primary.ID, KnowledgeBaseID: primary.KnowledgeBaseID,
				ParentChunkID: parent.ID, SubChunkID: []string{sub.ID, sub.ID},
				ChunkType: string(types.ChunkTypeText),
			},
			&types.SearchResult{
				ID: primary.ID, KnowledgeBaseID: primary.KnowledgeBaseID,
				ChunkType: string(types.ChunkTypeText),
			},
			&types.SearchResult{
				ID: history.ID, KnowledgeBaseID: history.KnowledgeBaseID,
				ChunkType: string(types.ChunkTypeText), MatchType: types.MatchTypeHistory,
			},
			&types.SearchResult{ID: "web", ChunkType: string(types.ChunkTypeWebSearch)},
		},
	)
	require.NoError(t, err)
	assert.True(t, eligible)

	var refs []types.MessageChunkReference
	require.NoError(t, db.Order("chunk_id").Find(&refs).Error)
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ChunkID)
	}
	assert.Equal(t, []string{primary.ID, parent.ID, sub.ID}, ids)
}

func TestFeedbackRejectsIncompleteAssistantMessage(t *testing.T) {
	repo, _, session, message, _ := setupFeedbackTestRepository(t)
	_, err := repo.ApplyMessageFeedback(context.Background(), types.ApplyMessageFeedbackInput{
		MessageTenantID: session.TenantID,
		ActorTenantID:   session.TenantID,
		ActorUserID:     "user-a",
		SessionID:       session.ID,
		MessageID:       message.ID,
		Type:            types.FeedbackTypeLike,
	})
	assert.ErrorIs(t, err, ErrFeedbackNotEligible)
}

func TestHydrateChunksUsesPersistedAttributionTable(t *testing.T) {
	repo, _, session, message, chunk := setupFeedbackTestRepository(t)
	ctx := context.Background()
	_, err := repo.CompleteAssistantMessageWithReferences(ctx, session.TenantID, message, feedbackReference(chunk))
	require.NoError(t, err)

	hydrated := *chunk
	require.NoError(t, repo.HydrateChunks(ctx, []*types.Chunk{&hydrated}, 0.5))
	assert.EqualValues(t, 1, hydrated.SessionCount)
}

func TestSessionDeleteRemovesFeedbackAndRecomputesChunk(t *testing.T) {
	repo, db, session, message, chunk := setupFeedbackTestRepository(t)
	ctx := context.Background()
	_, err := repo.CompleteAssistantMessageWithReferences(ctx, session.TenantID, message, feedbackReference(chunk))
	require.NoError(t, err)
	_, err = repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
		MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
		ActorUserID: "user-a", SessionID: session.ID, MessageID: message.ID,
		Type: types.FeedbackTypeLike,
	})
	require.NoError(t, err)

	require.NoError(t, repo.DeleteSessionMessagesWithFeedback(
		ctx, session.TenantID, []string{session.ID}, "user-a", true,
	))

	got := loadFeedbackChunk(t, db, chunk.ID)
	assert.Zero(t, got.LikeCount)
	assert.Zero(t, got.DislikeCount)
	assert.Equal(t, 1.0, got.RecallWeight)
	var feedbackCount, referenceCount, activeMessageCount, activeSessionCount int64
	require.NoError(t, db.Model(&types.MessageFeedback{}).Count(&feedbackCount).Error)
	require.NoError(t, db.Model(&types.MessageChunkReference{}).Count(&referenceCount).Error)
	require.NoError(t, db.Table("messages").Where("deleted_at IS NULL").Count(&activeMessageCount).Error)
	require.NoError(t, db.Table("sessions").Where("deleted_at IS NULL").Count(&activeSessionCount).Error)
	assert.Zero(t, feedbackCount)
	assert.Zero(t, referenceCount)
	assert.Zero(t, activeMessageCount)
	assert.Zero(t, activeSessionCount)
}

func TestDislikeReasonChangeReplacesReasonProjection(t *testing.T) {
	repo, _, session, message, chunk := setupFeedbackTestRepository(t)
	ctx := context.Background()
	_, err := repo.CompleteAssistantMessageWithReferences(ctx, session.TenantID, message, feedbackReference(chunk))
	require.NoError(t, err)
	apply := func(reason types.FeedbackReasonCode) {
		t.Helper()
		_, applyErr := repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
			MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
			ActorUserID: "user-a", SessionID: session.ID, MessageID: message.ID,
			Type: types.FeedbackTypeDislike, ReasonCode: &reason,
		})
		require.NoError(t, applyErr)
	}
	apply(types.FeedbackReasonInaccurate)
	apply(types.FeedbackReasonOutdated)

	details, err := repo.GetChunkFeedbackDetails(ctx, chunk.TenantID, chunk.ID)
	require.NoError(t, err)
	assert.Zero(t, details.ReasonCounts[types.FeedbackReasonInaccurate])
	assert.EqualValues(t, 1, details.ReasonCounts[types.FeedbackReasonOutdated])
}

func TestDeletingOneOfThreeReferencesStillAllowsLike(t *testing.T) {
	repo, db, session, message, chunkA := setupFeedbackTestRepository(t)
	ctx := context.Background()
	chunkB := &types.Chunk{
		ID: "chunk-b", TenantID: chunkA.TenantID, KnowledgeBaseID: chunkA.KnowledgeBaseID,
		KnowledgeID: chunkA.KnowledgeID, Content: "b", SourceContent: "b",
		RecallWeight: 1, IsEnabled: true,
	}
	chunkC := &types.Chunk{
		ID: "chunk-c", TenantID: chunkA.TenantID, KnowledgeBaseID: chunkA.KnowledgeBaseID,
		KnowledgeID: chunkA.KnowledgeID, Content: "c", SourceContent: "c",
		RecallWeight: 1, IsEnabled: true,
	}
	require.NoError(t, db.Create([]*types.Chunk{chunkB, chunkC}).Error)
	_, err := repo.CompleteAssistantMessageWithReferences(
		ctx, session.TenantID, message, feedbackReferences(chunkA, chunkB, chunkC),
	)
	require.NoError(t, err)
	require.NoError(t, (&chunkRepository{db: db}).DeleteChunk(ctx, chunkB.TenantID, chunkB.ID))

	_, err = repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
		MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
		ActorUserID: "user-a", SessionID: session.ID, MessageID: message.ID, Type: types.FeedbackTypeLike,
	})
	require.NoError(t, err)
	for _, id := range []string{chunkA.ID, chunkC.ID} {
		got := loadFeedbackChunk(t, db, id)
		assert.EqualValues(t, 1, got.LikeCount)
		assert.Equal(t, 1.2, got.RecallWeight)
	}

	var referenceCount int64
	require.NoError(t, db.Model(&types.MessageChunkReference{}).
		Where("message_id = ?", message.ID).Count(&referenceCount).Error)
	assert.EqualValues(t, 2, referenceCount)
	var deleted types.Chunk
	require.NoError(t, db.Unscoped().First(&deleted, "id = ?", chunkB.ID).Error)
	assert.True(t, deleted.DeletedAt.Valid)
	assert.Zero(t, deleted.LikeCount)
}

func TestDeletedReferencedChunkDoesNotBlockRemainingFeedback(t *testing.T) {
	repo, db, session, message, chunkA := setupFeedbackTestRepository(t)
	ctx := context.Background()
	chunkB := &types.Chunk{
		ID: "chunk-b", TenantID: chunkA.TenantID, KnowledgeBaseID: chunkA.KnowledgeBaseID,
		KnowledgeID: chunkA.KnowledgeID, Content: "b", SourceContent: "b",
		RecallWeight: 1, IsEnabled: true,
	}
	chunkC := &types.Chunk{
		ID: "chunk-c", TenantID: chunkA.TenantID, KnowledgeBaseID: chunkA.KnowledgeBaseID,
		KnowledgeID: chunkA.KnowledgeID, Content: "c", SourceContent: "c",
		RecallWeight: 1, IsEnabled: true,
	}
	require.NoError(t, db.Create([]*types.Chunk{chunkB, chunkC}).Error)
	_, err := repo.CompleteAssistantMessageWithReferences(
		ctx, session.TenantID, message, feedbackReferences(chunkA, chunkB, chunkC),
	)
	require.NoError(t, err)

	apply := func(feedbackType types.FeedbackType) {
		t.Helper()
		_, applyErr := repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
			MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
			ActorUserID: "user-a", SessionID: session.ID, MessageID: message.ID, Type: feedbackType,
		})
		require.NoError(t, applyErr)
	}
	apply(types.FeedbackTypeLike)
	for _, id := range []string{chunkA.ID, chunkB.ID, chunkC.ID} {
		got := loadFeedbackChunk(t, db, id)
		assert.EqualValues(t, 1, got.LikeCount)
		assert.Zero(t, got.DislikeCount)
	}

	chunkRepo := &chunkRepository{db: db}
	require.NoError(t, chunkRepo.DeleteChunk(ctx, chunkB.TenantID, chunkB.ID))

	var deletedRefCount int64
	require.NoError(t, db.Model(&types.MessageChunkReference{}).
		Where("chunk_id = ?", chunkB.ID).Count(&deletedRefCount).Error)
	assert.Zero(t, deletedRefCount)

	apply(types.FeedbackTypeDislike)
	for _, id := range []string{chunkA.ID, chunkC.ID} {
		got := loadFeedbackChunk(t, db, id)
		assert.Zero(t, got.LikeCount)
		assert.EqualValues(t, 1, got.DislikeCount)
		assert.Equal(t, 0.8, got.RecallWeight)
	}

	apply(types.FeedbackTypeNone)
	for _, id := range []string{chunkA.ID, chunkC.ID} {
		got := loadFeedbackChunk(t, db, id)
		assert.Zero(t, got.LikeCount)
		assert.Zero(t, got.DislikeCount)
		assert.Equal(t, 1.0, got.RecallWeight)
	}

	var deleted types.Chunk
	require.NoError(t, db.Unscoped().First(&deleted, "id = ?", chunkB.ID).Error)
	assert.True(t, deleted.DeletedAt.Valid)
	assert.EqualValues(t, 1, deleted.LikeCount, "a deleted chunk must never be restored or recomputed")
	assert.Zero(t, deleted.DislikeCount)
	assert.Equal(t, 1.2, deleted.RecallWeight)
}

func TestLegacyMissingReferenceDoesNotBlockRemainingFeedback(t *testing.T) {
	repo, db, session, message, chunkA := setupFeedbackTestRepository(t)
	ctx := context.Background()
	chunkB := &types.Chunk{
		ID: "chunk-b", TenantID: chunkA.TenantID, KnowledgeBaseID: chunkA.KnowledgeBaseID,
		KnowledgeID: chunkA.KnowledgeID, Content: "b", SourceContent: "b",
		RecallWeight: 1, IsEnabled: true,
	}
	require.NoError(t, db.Create(chunkB).Error)
	_, err := repo.CompleteAssistantMessageWithReferences(
		ctx, session.TenantID, message, feedbackReferences(chunkA, chunkB),
	)
	require.NoError(t, err)

	// Simulate a pre-fix soft delete that left its attribution row behind.
	require.NoError(t, db.Where("tenant_id = ? AND id = ?", chunkB.TenantID, chunkB.ID).
		Delete(&types.Chunk{}).Error)

	_, err = repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
		MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
		ActorUserID: "user-a", SessionID: session.ID, MessageID: message.ID, Type: types.FeedbackTypeLike,
	})
	require.NoError(t, err)

	got := loadFeedbackChunk(t, db, chunkA.ID)
	assert.EqualValues(t, 1, got.LikeCount)
	var staleCount int64
	require.NoError(t, db.Model(&types.MessageChunkReference{}).
		Where("chunk_id = ?", chunkB.ID).Count(&staleCount).Error)
	assert.Zero(t, staleCount)
}

func TestAllLegacyReferencedChunksMissingReturnsBusinessErrorAndCleansReferences(t *testing.T) {
	repo, db, session, message, chunk := setupFeedbackTestRepository(t)
	ctx := context.Background()
	_, err := repo.CompleteAssistantMessageWithReferences(ctx, session.TenantID, message, feedbackReference(chunk))
	require.NoError(t, err)
	// Simulate a pre-fix soft delete that left its attribution row behind.
	require.NoError(t, db.Where("tenant_id = ? AND id = ?", chunk.TenantID, chunk.ID).
		Delete(&types.Chunk{}).Error)

	_, err = repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
		MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
		ActorUserID: "user-a", SessionID: session.ID, MessageID: message.ID, Type: types.FeedbackTypeLike,
	})
	assert.ErrorIs(t, err, ErrFeedbackNotEligible)

	var feedbackCount, referenceCount, auditCount int64
	require.NoError(t, db.Model(&types.MessageFeedback{}).Count(&feedbackCount).Error)
	require.NoError(t, db.Model(&types.MessageChunkReference{}).Count(&referenceCount).Error)
	require.NoError(t, db.Model(&types.ChunkFeedbackAudit{}).Count(&auditCount).Error)
	assert.Zero(t, feedbackCount)
	assert.Zero(t, referenceCount)
	assert.Zero(t, auditCount)
}

func TestLegacyReferenceCleanupDatabaseErrorIsNotBusinessError(t *testing.T) {
	repo, db, session, message, chunk := setupFeedbackTestRepository(t)
	ctx := context.Background()
	_, err := repo.CompleteAssistantMessageWithReferences(ctx, session.TenantID, message, feedbackReference(chunk))
	require.NoError(t, err)
	require.NoError(t, db.Where("tenant_id = ? AND id = ?", chunk.TenantID, chunk.ID).
		Delete(&types.Chunk{}).Error)
	require.NoError(t, db.Exec(`
		CREATE TRIGGER reject_legacy_reference_cleanup
		BEFORE DELETE ON message_chunk_references
		BEGIN SELECT RAISE(ABORT, 'legacy reference cleanup failure'); END;
	`).Error)

	_, err = repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
		MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
		ActorUserID: "user-a", SessionID: session.ID, MessageID: message.ID, Type: types.FeedbackTypeLike,
	})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrFeedbackNotEligible)

	var feedbackCount, referenceCount int64
	require.NoError(t, db.Model(&types.MessageFeedback{}).Count(&feedbackCount).Error)
	require.NoError(t, db.Model(&types.MessageChunkReference{}).Count(&referenceCount).Error)
	assert.Zero(t, feedbackCount)
	assert.EqualValues(t, 1, referenceCount)
}

func TestChunkDeleteRollsBackWhenReferenceCleanupFails(t *testing.T) {
	repo, db, session, message, chunk := setupFeedbackTestRepository(t)
	ctx := context.Background()
	_, err := repo.CompleteAssistantMessageWithReferences(ctx, session.TenantID, message, feedbackReference(chunk))
	require.NoError(t, err)
	require.NoError(t, db.Exec(`
		CREATE TRIGGER reject_reference_cleanup
		BEFORE DELETE ON message_chunk_references
		BEGIN SELECT RAISE(ABORT, 'reference cleanup failure'); END;
	`).Error)

	err = (&chunkRepository{db: db}).DeleteChunk(ctx, chunk.TenantID, chunk.ID)
	require.Error(t, err)

	var activeCount, referenceCount int64
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", chunk.ID).Count(&activeCount).Error)
	require.NoError(t, db.Model(&types.MessageChunkReference{}).
		Where("chunk_id = ?", chunk.ID).Count(&referenceCount).Error)
	assert.EqualValues(t, 1, activeCount)
	assert.EqualValues(t, 1, referenceCount)
}

func TestRepeatedBatchChunkDeleteIsIdempotent(t *testing.T) {
	repo, db, session, message, chunkA := setupFeedbackTestRepository(t)
	ctx := context.Background()
	chunkB := &types.Chunk{
		ID: "chunk-b", TenantID: chunkA.TenantID, KnowledgeBaseID: chunkA.KnowledgeBaseID,
		KnowledgeID: chunkA.KnowledgeID, Content: "b", SourceContent: "b",
		RecallWeight: 1, IsEnabled: true,
	}
	require.NoError(t, db.Create(chunkB).Error)
	_, err := repo.CompleteAssistantMessageWithReferences(
		ctx, session.TenantID, message, feedbackReferences(chunkA, chunkB),
	)
	require.NoError(t, err)

	chunkRepo := &chunkRepository{db: db}
	require.NoError(t, chunkRepo.DeleteChunks(ctx, chunkA.TenantID, []string{chunkB.ID, chunkB.ID}))
	require.NoError(t, chunkRepo.DeleteChunks(ctx, chunkA.TenantID, []string{chunkB.ID}))

	var activeCount, referenceCount int64
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", chunkB.ID).Count(&activeCount).Error)
	require.NoError(t, db.Model(&types.MessageChunkReference{}).
		Where("chunk_id = ?", chunkB.ID).Count(&referenceCount).Error)
	assert.Zero(t, activeCount)
	assert.Zero(t, referenceCount)
}

func TestOrdinaryChunkSaveCannotOverwriteFeedbackProjection(t *testing.T) {
	_, db, _, _, chunk := setupFeedbackTestRepository(t)
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", chunk.ID).Updates(map[string]interface{}{
		"like_count": 4, "dislike_count": 1, "positive_rate": 0.8, "recall_weight": 1.2,
	}).Error)

	stale := *chunk
	stale.Content = "edited content"
	stale.LikeCount = 0
	stale.DislikeCount = 0
	stale.PositiveRate = nil
	stale.RecallWeight = 1
	require.NoError(t, (&chunkRepository{db: db}).UpdateChunk(context.Background(), &stale))

	got := loadFeedbackChunk(t, db, chunk.ID)
	assert.Equal(t, "edited content", got.Content)
	assert.EqualValues(t, 4, got.LikeCount)
	assert.EqualValues(t, 1, got.DislikeCount)
	require.NotNil(t, got.PositiveRate)
	assert.Equal(t, 0.8, *got.PositiveRate)
	assert.Equal(t, 1.2, got.RecallWeight)
}

func TestConcurrentFeedbackConvergesToExactProjection(t *testing.T) {
	repo, db, session, message, chunk := setupFeedbackTestRepository(t)
	ctx := context.Background()
	type ownedMessage struct {
		sessionID string
		messageID string
		userID    string
	}
	owned := make([]ownedMessage, 12)
	for i := range owned {
		if i == 0 {
			owned[i] = ownedMessage{sessionID: session.ID, messageID: message.ID, userID: session.UserID}
			_, err := repo.CompleteAssistantMessageWithReferences(
				ctx, session.TenantID, message, feedbackReference(chunk),
			)
			require.NoError(t, err)
			continue
		}
		owned[i] = ownedMessage{
			sessionID: fmt.Sprintf("session-%02d", i),
			messageID: fmt.Sprintf("message-%02d", i),
			userID:    fmt.Sprintf("concurrent-%02d", i),
		}
		require.NoError(t, db.Exec(
			"INSERT INTO sessions (id, tenant_id, user_id) VALUES (?, ?, ?)",
			owned[i].sessionID, session.TenantID, owned[i].userID,
		).Error)
		require.NoError(t, db.Exec(
			"INSERT INTO messages (id, session_id, content, role, is_completed) VALUES (?, ?, ?, ?, ?)",
			owned[i].messageID, owned[i].sessionID, "draft", "assistant", false,
		).Error)
		ownedModel := &types.Message{
			ID: owned[i].messageID, SessionID: owned[i].sessionID, Role: "assistant", Content: "final",
		}
		_, err := repo.CompleteAssistantMessageWithReferences(
			ctx, session.TenantID, ownedModel, feedbackReference(chunk),
		)
		require.NoError(t, err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			feedbackType := types.FeedbackTypeLike
			if index%3 == 0 {
				feedbackType = types.FeedbackTypeDislike
			}
			_, applyErr := repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
				MessageTenantID: session.TenantID,
				ActorTenantID:   session.TenantID,
				ActorUserID:     owned[index].userID,
				SessionID:       owned[index].sessionID,
				MessageID:       owned[index].messageID,
				Type:            feedbackType,
			})
			errs <- applyErr
		}(i)
	}
	wg.Wait()
	close(errs)
	for applyErr := range errs {
		require.NoError(t, applyErr)
	}

	got := loadFeedbackChunk(t, db, chunk.ID)
	assert.EqualValues(t, 8, got.LikeCount)
	assert.EqualValues(t, 4, got.DislikeCount)
	require.NotNil(t, got.PositiveRate)
	assert.InDelta(t, 2.0/3.0, *got.PositiveRate, 1e-9)
	assert.Equal(t, 1.0, got.RecallWeight)
}

func TestConcurrentSameUserFeedbackHasOneExactProjection(t *testing.T) {
	repo, db, session, message, chunk := setupFeedbackTestRepository(t)
	ctx := context.Background()
	_, err := repo.CompleteAssistantMessageWithReferences(ctx, session.TenantID, message, feedbackReference(chunk))
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			feedbackType := types.FeedbackTypeLike
			if index%2 == 0 {
				feedbackType = types.FeedbackTypeDislike
			}
			_, applyErr := repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
				MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
				ActorUserID: session.UserID, SessionID: session.ID, MessageID: message.ID,
				Type: feedbackType,
			})
			errs <- applyErr
		}(i)
	}
	wg.Wait()
	close(errs)
	for applyErr := range errs {
		require.NoError(t, applyErr)
	}

	var feedback types.MessageFeedback
	require.NoError(t, db.First(&feedback, "user_id = ?", session.UserID).Error)
	var count int64
	require.NoError(t, db.Model(&types.MessageFeedback{}).
		Where("user_id = ?", session.UserID).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	got := loadFeedbackChunk(t, db, chunk.ID)
	if feedback.FeedbackType == types.FeedbackTypeLike {
		assert.EqualValues(t, 1, got.LikeCount)
		assert.Zero(t, got.DislikeCount)
	} else {
		assert.Zero(t, got.LikeCount)
		assert.EqualValues(t, 1, got.DislikeCount)
	}
}
