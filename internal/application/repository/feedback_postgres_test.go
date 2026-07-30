package repository

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
)

const feedbackPostgresTestDSNEnv = "WEKNORA_TEST_POSTGRES_DSN"

type feedbackPostgresBarrier struct {
	reached     chan struct{}
	release     chan struct{}
	reachedOnce sync.Once
	releaseOnce sync.Once
}

func newFeedbackPostgresBarrier() *feedbackPostgresBarrier {
	return &feedbackPostgresBarrier{
		reached: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (b *feedbackPostgresBarrier) block() {
	b.reachedOnce.Do(func() { close(b.reached) })
	<-b.release
}

func (b *feedbackPostgresBarrier) signal() {
	b.reachedOnce.Do(func() { close(b.reached) })
}

func (b *feedbackPostgresBarrier) unblock() {
	b.releaseOnce.Do(func() { close(b.release) })
}

func waitForFeedbackPostgresSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitForFeedbackPostgresError(t *testing.T, result <-chan error, description string) {
	t.Helper()
	select {
	case err := <-result:
		require.NoError(t, err, description)
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func isFeedbackPostgresChunkQuery(tx *gorm.DB) bool {
	return isFeedbackPostgresTableQuery(tx, "chunks")
}

func isFeedbackPostgresMessageQuery(tx *gorm.DB) bool {
	return isFeedbackPostgresTableQuery(tx, "messages")
}

func isFeedbackPostgresTableQuery(tx *gorm.DB, table string) bool {
	if tx == nil || tx.Statement == nil {
		return false
	}
	if tx.Statement.Table == table ||
		(tx.Statement.Schema != nil && tx.Statement.Schema.Table == table) {
		return true
	}
	return strings.Contains(strings.ToLower(tx.Statement.SQL.String()), `from "`+table+`"`)
}

func installFeedbackPostgresQueryBarrier(
	t *testing.T,
	db *gorm.DB,
	position string,
	matches func(*gorm.DB) bool,
	barrier *feedbackPostgresBarrier,
	block bool,
) {
	t.Helper()
	name := "feedback_postgres_" + position + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	callback := func(tx *gorm.DB) {
		if !matches(tx) {
			return
		}
		if block {
			barrier.block()
		} else {
			barrier.signal()
		}
	}
	var err error
	switch position {
	case "before":
		err = db.Callback().Query().Before("gorm:query").Register(name, callback)
	case "after":
		err = db.Callback().Query().After("gorm:query").Register(name, callback)
	default:
		t.Fatalf("unsupported callback position %q", position)
	}
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Query().Remove(name))
	})
}

func installFeedbackPostgresChunkQueryBarrier(
	t *testing.T,
	db *gorm.DB,
	position string,
	barrier *feedbackPostgresBarrier,
	block bool,
) {
	t.Helper()
	installFeedbackPostgresQueryBarrier(
		t, db, position, isFeedbackPostgresChunkQuery, barrier, block,
	)
}

func installFeedbackPostgresMessageQueryBarrier(
	t *testing.T,
	db *gorm.DB,
	position string,
	barrier *feedbackPostgresBarrier,
	block bool,
) {
	t.Helper()
	installFeedbackPostgresQueryBarrier(
		t, db, position, isFeedbackPostgresMessageQuery, barrier, block,
	)
}

func setupFeedbackPostgresTestDatabases(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv(feedbackPostgresTestDSNEnv)
	if dsn == "" {
		t.Skipf("%s is not set", feedbackPostgresTestDSNEnv)
	}

	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)

	schema := "feedback_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, admin.Exec(`CREATE SCHEMA "`+schema+`"`).Error)

	openSchemaConnection := func() *gorm.DB {
		db, openErr := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		require.NoError(t, openErr)
		sqlDB, dbErr := db.DB()
		require.NoError(t, dbErr)
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		require.NoError(t, db.Exec(`SET search_path TO "`+schema+`"`).Error)
		return db
	}

	completionDB := openSchemaConnection()
	deletionDB := openSchemaConnection()
	completionSQL, err := completionDB.DB()
	require.NoError(t, err)
	deletionSQL, err := deletionDB.DB()
	require.NoError(t, err)

	var completionPID, deletionPID int
	require.NoError(t, completionDB.Raw("SELECT pg_backend_pid()").Scan(&completionPID).Error)
	require.NoError(t, deletionDB.Raw("SELECT pg_backend_pid()").Scan(&deletionPID).Error)
	require.NotEqual(t, completionPID, deletionPID, "the concurrency test requires two database connections")

	t.Cleanup(func() {
		require.NoError(t, completionSQL.Close())
		require.NoError(t, deletionSQL.Close())
		require.NoError(t, admin.Exec(`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`).Error)
		require.NoError(t, adminSQL.Close())
	})

	require.NoError(t, completionDB.Exec(`
		CREATE TABLE sessions (
			id varchar(36) PRIMARY KEY,
			tenant_id bigint NOT NULL,
			user_id varchar(512) NOT NULL DEFAULT '',
			deleted_at timestamptz
		);
		CREATE TABLE messages (
			id varchar(36) PRIMARY KEY,
			session_id varchar(36) NOT NULL,
			content text,
			role varchar(16),
			knowledge_references jsonb,
			agent_steps jsonb,
			is_completed boolean NOT NULL DEFAULT false,
			is_fallback boolean NOT NULL DEFAULT false,
			created_at timestamptz,
			updated_at timestamptz,
			deleted_at timestamptz
		);
		CREATE TABLE chunks (
			id varchar(36) PRIMARY KEY,
			tenant_id bigint NOT NULL,
			knowledge_id varchar(36) NOT NULL,
			knowledge_base_id varchar(36) NOT NULL,
			content text,
			source_content text,
			is_enabled boolean NOT NULL DEFAULT true,
			created_at timestamptz,
			updated_at timestamptz,
			deleted_at timestamptz,
			like_count bigint NOT NULL DEFAULT 0,
			dislike_count bigint NOT NULL DEFAULT 0,
			positive_rate double precision,
			recall_weight double precision NOT NULL DEFAULT 1,
			feedback_reset_at timestamptz
		);
		CREATE TABLE message_chunk_references (
			id varchar(36) PRIMARY KEY,
			message_tenant_id bigint NOT NULL,
			chunk_tenant_id bigint NOT NULL,
			message_id varchar(36) NOT NULL,
			chunk_id varchar(36) NOT NULL,
			created_at timestamptz NOT NULL,
			UNIQUE (message_tenant_id, chunk_tenant_id, message_id, chunk_id)
		);
		CREATE TABLE message_feedbacks (
			id varchar(36) PRIMARY KEY,
			tenant_id bigint NOT NULL,
			user_id varchar(64) NOT NULL,
			session_id varchar(36) NOT NULL,
			message_id varchar(36) NOT NULL,
			feedback_type varchar(16) NOT NULL,
			reason_code varchar(16),
			feedback_at timestamptz NOT NULL,
			created_at timestamptz NOT NULL,
			updated_at timestamptz NOT NULL,
			UNIQUE (tenant_id, user_id, message_id)
		);
		CREATE TABLE chunk_feedback_audits (
			id bigserial PRIMARY KEY,
			chunk_tenant_id bigint NOT NULL,
			chunk_id varchar(36) NOT NULL,
			actor_tenant_id bigint NOT NULL,
			actor_user_id varchar(64) NOT NULL,
			action varchar(32) NOT NULL,
			trigger_source varchar(16) NOT NULL DEFAULT 'legacy',
			old_weight double precision NOT NULL,
			new_weight double precision NOT NULL,
			created_at timestamptz NOT NULL
		);
	`).Error)

	return completionDB, deletionDB
}

func seedFeedbackPostgresTestCase(
	t *testing.T, db *gorm.DB, suffix string, chunkIDs ...string,
) (*types.Session, *types.Message, map[string]*types.Chunk) {
	t.Helper()
	session, message := seedFeedbackPostgresOwnedMessage(t, db, 101, suffix, "owner-"+suffix)
	chunks := make(map[string]*types.Chunk, len(chunkIDs))
	for _, id := range chunkIDs {
		chunk := &types.Chunk{
			ID:              id + "-" + suffix,
			TenantID:        202,
			KnowledgeBaseID: "kb-" + suffix,
			KnowledgeID:     "knowledge-" + suffix,
			Content:         id,
			SourceContent:   id,
			RecallWeight:    1,
			IsEnabled:       true,
		}
		require.NoError(t, db.Exec(`
			INSERT INTO chunks (
				id, tenant_id, knowledge_id, knowledge_base_id, content, source_content,
				is_enabled, recall_weight, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
			chunk.ID, chunk.TenantID, chunk.KnowledgeID, chunk.KnowledgeBaseID,
			chunk.Content, chunk.SourceContent, chunk.IsEnabled, chunk.RecallWeight,
		).Error)
		chunks[id] = chunk
	}
	return session, message, chunks
}

func seedFeedbackPostgresOwnedMessage(
	t *testing.T, db *gorm.DB, tenantID uint64, suffix, userID string,
) (*types.Session, *types.Message) {
	t.Helper()
	session := &types.Session{
		ID:       "session-" + suffix,
		TenantID: tenantID,
		UserID:   userID,
	}
	message := &types.Message{
		ID:        "message-" + suffix,
		SessionID: session.ID,
		Role:      "assistant",
		Content:   "final",
	}
	require.NoError(t, db.Exec(
		"INSERT INTO sessions (id, tenant_id, user_id) VALUES (?, ?, ?)",
		session.ID, session.TenantID, session.UserID,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO messages (id, session_id, content, role) VALUES (?, ?, ?, ?)",
		message.ID, message.SessionID, "draft", message.Role,
	).Error)
	return session, message
}

func feedbackPostgresReferences(chunks ...*types.Chunk) types.References {
	refs := make(types.References, 0, len(chunks))
	for _, chunk := range chunks {
		refs = append(refs, &types.SearchResult{
			ID: chunk.ID, KnowledgeBaseID: chunk.KnowledgeBaseID, ChunkType: types.ChunkTypeText,
		})
	}
	return refs
}

func assertFeedbackPostgresNoDanglingReferences(t *testing.T, db *gorm.DB) {
	t.Helper()
	var count int64
	require.NoError(t, db.Raw(`
		SELECT COUNT(*)
		FROM message_chunk_references AS r
		JOIN chunks AS c
			ON c.tenant_id = r.chunk_tenant_id
			AND c.id = r.chunk_id
		WHERE c.deleted_at IS NOT NULL
	`).Scan(&count).Error)
	assert.Zero(t, count)
}

func feedbackPostgresTestRepository(db *gorm.DB) *feedbackRepository {
	policy := config.DefaultFeedbackConfig()
	policy.MinimumSampleCount = 1
	return &feedbackRepository{db: db, feedbackConfig: policy}
}

func applyFeedbackPostgresRating(
	t *testing.T,
	repo *feedbackRepository,
	session *types.Session,
	message *types.Message,
	feedbackType types.FeedbackType,
	reason *types.FeedbackReasonCode,
) {
	t.Helper()
	_, err := repo.ApplyMessageFeedback(context.Background(), types.ApplyMessageFeedbackInput{
		MessageTenantID: session.TenantID,
		ActorTenantID:   session.TenantID,
		ActorUserID:     session.UserID,
		SessionID:       session.ID,
		MessageID:       message.ID,
		Type:            feedbackType,
		ReasonCode:      reason,
	})
	require.NoError(t, err)
}

func loadFeedbackPostgresProjection(t *testing.T, db *gorm.DB, chunkID string) types.Chunk {
	t.Helper()
	var chunk types.Chunk
	require.NoError(t, db.First(&chunk, "id = ?", chunkID).Error)
	return chunk
}

func loadFeedbackPostgresEvent(t *testing.T, db *gorm.DB, messageID string) types.MessageFeedback {
	t.Helper()
	var feedback types.MessageFeedback
	require.NoError(t, db.First(&feedback, "message_id = ?", messageID).Error)
	return feedback
}

func TestFeedbackPostgresCompletionAndChunkDeletionConcurrency(t *testing.T) {
	t.Run("completion locks chunk before deletion", func(t *testing.T) {
		completionDB, deletionDB := setupFeedbackPostgresTestDatabases(t)
		session, message, chunks := seedFeedbackPostgresTestCase(t, completionDB, "scenario-a", "b")
		completionLocked := newFeedbackPostgresBarrier()
		deletionAttempted := newFeedbackPostgresBarrier()
		installFeedbackPostgresChunkQueryBarrier(t, completionDB, "after", completionLocked, true)
		installFeedbackPostgresChunkQueryBarrier(t, deletionDB, "before", deletionAttempted, false)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		completionResult := make(chan error, 1)
		go func() {
			_, err := (&feedbackRepository{db: completionDB}).CompleteAssistantMessageWithReferences(
				ctx, session.TenantID, message, feedbackPostgresReferences(chunks["b"]),
			)
			completionResult <- err
		}()
		waitForFeedbackPostgresSignal(t, completionLocked.reached, "completion to lock chunk B")

		deletionResult := make(chan error, 1)
		go func() {
			deletionResult <- (&chunkRepository{db: deletionDB}).
				DeleteChunk(ctx, chunks["b"].TenantID, chunks["b"].ID)
		}()
		waitForFeedbackPostgresSignal(t, deletionAttempted.reached, "deletion to attempt locking chunk B")
		completionLocked.unblock()

		waitForFeedbackPostgresError(t, completionResult, "completion")
		waitForFeedbackPostgresError(t, deletionResult, "deletion")
		assertFeedbackPostgresNoDanglingReferences(t, completionDB)

		var referenceCount int64
		require.NoError(t, completionDB.Model(&types.MessageChunkReference{}).
			Where("chunk_id = ?", chunks["b"].ID).Count(&referenceCount).Error)
		assert.Zero(t, referenceCount)
	})

	t.Run("deletion locks chunk before completion", func(t *testing.T) {
		completionDB, deletionDB := setupFeedbackPostgresTestDatabases(t)
		session, message, chunks := seedFeedbackPostgresTestCase(t, completionDB, "scenario-b", "a", "b")
		deletionLocked := newFeedbackPostgresBarrier()
		completionAttempted := newFeedbackPostgresBarrier()
		installFeedbackPostgresChunkQueryBarrier(t, deletionDB, "after", deletionLocked, true)
		installFeedbackPostgresChunkQueryBarrier(t, completionDB, "before", completionAttempted, false)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		deletionResult := make(chan error, 1)
		go func() {
			deletionResult <- (&chunkRepository{db: deletionDB}).
				DeleteChunk(ctx, chunks["b"].TenantID, chunks["b"].ID)
		}()
		waitForFeedbackPostgresSignal(t, deletionLocked.reached, "deletion to lock chunk B")

		completionResult := make(chan error, 1)
		go func() {
			_, err := (&feedbackRepository{db: completionDB}).CompleteAssistantMessageWithReferences(
				ctx, session.TenantID, message,
				feedbackPostgresReferences(chunks["b"], chunks["a"]),
			)
			completionResult <- err
		}()
		waitForFeedbackPostgresSignal(t, completionAttempted.reached, "completion to attempt locking chunks")
		deletionLocked.unblock()

		waitForFeedbackPostgresError(t, deletionResult, "deletion")
		waitForFeedbackPostgresError(t, completionResult, "completion")
		assertFeedbackPostgresNoDanglingReferences(t, completionDB)

		var referenceIDs []string
		require.NoError(t, completionDB.Model(&types.MessageChunkReference{}).
			Where("message_id = ?", message.ID).
			Order("chunk_id").Pluck("chunk_id", &referenceIDs).Error)
		assert.Equal(t, []string{chunks["a"].ID}, referenceIDs)
		var completed bool
		require.NoError(t, completionDB.Table("messages").
			Select("is_completed").Where("id = ?", message.ID).Scan(&completed).Error)
		assert.True(t, completed)
	})

	t.Run("deleting one of three references preserves feedback on the others", func(t *testing.T) {
		completionDB, deletionDB := setupFeedbackPostgresTestDatabases(t)
		session, message, chunks := seedFeedbackPostgresTestCase(
			t, completionDB, "scenario-c", "a", "b", "c",
		)
		completionLocked := newFeedbackPostgresBarrier()
		deletionAttempted := newFeedbackPostgresBarrier()
		installFeedbackPostgresChunkQueryBarrier(t, completionDB, "after", completionLocked, true)
		installFeedbackPostgresChunkQueryBarrier(t, deletionDB, "before", deletionAttempted, false)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		completionResult := make(chan error, 1)
		go func() {
			_, err := (&feedbackRepository{db: completionDB}).CompleteAssistantMessageWithReferences(
				ctx, session.TenantID, message,
				feedbackPostgresReferences(chunks["c"], chunks["b"], chunks["a"]),
			)
			completionResult <- err
		}()
		waitForFeedbackPostgresSignal(t, completionLocked.reached, "completion to lock chunks A, B, and C")

		deletionResult := make(chan error, 1)
		go func() {
			deletionResult <- (&chunkRepository{db: deletionDB}).
				DeleteChunk(ctx, chunks["b"].TenantID, chunks["b"].ID)
		}()
		waitForFeedbackPostgresSignal(t, deletionAttempted.reached, "deletion to attempt locking chunk B")
		completionLocked.unblock()

		waitForFeedbackPostgresError(t, completionResult, "completion")
		waitForFeedbackPostgresError(t, deletionResult, "deletion")
		assertFeedbackPostgresNoDanglingReferences(t, completionDB)

		repo := &feedbackRepository{db: completionDB}
		for _, input := range []types.ApplyMessageFeedbackInput{
			{
				MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
				ActorUserID: session.UserID, SessionID: session.ID, MessageID: message.ID,
				Type: types.FeedbackTypeLike,
			},
			{
				MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
				ActorUserID: session.UserID, SessionID: session.ID, MessageID: message.ID,
				Type: types.FeedbackTypeDislike, ReasonCode: func() *types.FeedbackReasonCode {
					reason := types.FeedbackReasonInaccurate
					return &reason
				}(),
			},
			{
				MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
				ActorUserID: session.UserID, SessionID: session.ID, MessageID: message.ID,
				Type: types.FeedbackTypeNone,
			},
		} {
			_, err := repo.ApplyMessageFeedback(ctx, input)
			require.NoError(t, err)
		}

		var referenceIDs []string
		require.NoError(t, completionDB.Model(&types.MessageChunkReference{}).
			Where("message_id = ?", message.ID).
			Order("chunk_id").Pluck("chunk_id", &referenceIDs).Error)
		assert.Equal(t, []string{chunks["a"].ID, chunks["c"].ID}, referenceIDs)

		for _, id := range []string{"a", "c"} {
			var chunk types.Chunk
			require.NoError(t, completionDB.First(&chunk, "id = ?", chunks[id].ID).Error)
			assert.Zero(t, chunk.LikeCount)
			assert.Zero(t, chunk.DislikeCount)
			assert.Equal(t, 1.0, chunk.RecallWeight)
		}
		var deleted types.Chunk
		require.NoError(t, completionDB.Unscoped().First(&deleted, "id = ?", chunks["b"].ID).Error)
		assert.True(t, deleted.DeletedAt.Valid)
		assert.Zero(t, deleted.LikeCount)
		assert.Zero(t, deleted.DislikeCount)
		assert.Equal(t, 1.0, deleted.RecallWeight)

		var deletedChunkAuditCount int64
		require.NoError(t, completionDB.Model(&types.ChunkFeedbackAudit{}).
			Where("chunk_id = ?", chunks["b"].ID).Count(&deletedChunkAuditCount).Error)
		assert.Zero(t, deletedChunkAuditCount)
	})
}

func TestFeedbackPostgresFeedbackAndLifecycleConcurrency(t *testing.T) {
	t.Run("message delete serializes after feedback", func(t *testing.T) {
		feedbackDB, lifecycleDB := setupFeedbackPostgresTestDatabases(t)
		session, message, chunks := seedFeedbackPostgresTestCase(t, feedbackDB, "message-delete", "a")
		repo := &feedbackRepository{db: feedbackDB}
		_, err := repo.CompleteAssistantMessageWithReferences(
			context.Background(), session.TenantID, message, feedbackPostgresReferences(chunks["a"]),
		)
		require.NoError(t, err)

		feedbackLocked := newFeedbackPostgresBarrier()
		deleteAttempted := newFeedbackPostgresBarrier()
		installFeedbackPostgresChunkQueryBarrier(t, feedbackDB, "after", feedbackLocked, true)
		installFeedbackPostgresMessageQueryBarrier(t, lifecycleDB, "before", deleteAttempted, false)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		feedbackResult := make(chan error, 1)
		go func() {
			_, applyErr := repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
				MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
				ActorUserID: session.UserID, SessionID: session.ID, MessageID: message.ID,
				Type: types.FeedbackTypeLike,
			})
			feedbackResult <- applyErr
		}()
		waitForFeedbackPostgresSignal(t, feedbackLocked.reached, "feedback to lock its chunk")

		deleteResult := make(chan error, 1)
		go func() {
			deleteResult <- (&feedbackRepository{db: lifecycleDB}).DeleteMessageWithFeedback(
				ctx, session.TenantID, session.ID, message.ID, session.UserID,
			)
		}()
		waitForFeedbackPostgresSignal(t, deleteAttempted.reached, "message delete to attempt its message lock")
		feedbackLocked.unblock()
		waitForFeedbackPostgresError(t, feedbackResult, "feedback")
		waitForFeedbackPostgresError(t, deleteResult, "message delete")

		var feedbackCount int64
		require.NoError(t, feedbackDB.Model(&types.MessageFeedback{}).Count(&feedbackCount).Error)
		assert.Zero(t, feedbackCount)
		var chunk types.Chunk
		require.NoError(t, feedbackDB.First(&chunk, "id = ?", chunks["a"].ID).Error)
		assert.Zero(t, chunk.LikeCount)
		assert.Zero(t, chunk.DislikeCount)
		assert.Equal(t, 1.0, chunk.RecallWeight)
	})

	t.Run("reset serializes after feedback", func(t *testing.T) {
		feedbackDB, resetDB := setupFeedbackPostgresTestDatabases(t)
		session, message, chunks := seedFeedbackPostgresTestCase(t, feedbackDB, "reset", "a")
		repo := &feedbackRepository{db: feedbackDB}
		_, err := repo.CompleteAssistantMessageWithReferences(
			context.Background(), session.TenantID, message, feedbackPostgresReferences(chunks["a"]),
		)
		require.NoError(t, err)

		feedbackLocked := newFeedbackPostgresBarrier()
		resetAttempted := newFeedbackPostgresBarrier()
		installFeedbackPostgresChunkQueryBarrier(t, feedbackDB, "after", feedbackLocked, true)
		installFeedbackPostgresChunkQueryBarrier(t, resetDB, "before", resetAttempted, false)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		feedbackResult := make(chan error, 1)
		go func() {
			_, applyErr := repo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
				MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
				ActorUserID: session.UserID, SessionID: session.ID, MessageID: message.ID,
				Type: types.FeedbackTypeLike,
			})
			feedbackResult <- applyErr
		}()
		waitForFeedbackPostgresSignal(t, feedbackLocked.reached, "feedback to lock its chunk")

		resetResult := make(chan error, 1)
		go func() {
			resetResult <- (&feedbackRepository{db: resetDB}).ResetChunkFeedback(
				ctx,
				types.ResetChunkFeedbackInput{
					ChunkTenantID: chunks["a"].TenantID, ActorTenantID: session.TenantID,
					ActorUserID: "admin", KnowledgeBaseID: chunks["a"].KnowledgeBaseID,
					ChunkID: chunks["a"].ID,
				},
			)
		}()
		waitForFeedbackPostgresSignal(t, resetAttempted.reached, "reset to attempt its chunk lock")
		feedbackLocked.unblock()
		waitForFeedbackPostgresError(t, feedbackResult, "feedback")
		waitForFeedbackPostgresError(t, resetResult, "reset")

		var chunk types.Chunk
		require.NoError(t, feedbackDB.First(&chunk, "id = ?", chunks["a"].ID).Error)
		assert.Zero(t, chunk.LikeCount)
		assert.Zero(t, chunk.DislikeCount)
		assert.Equal(t, 1.0, chunk.RecallWeight)
		assert.NotNil(t, chunk.FeedbackResetAt)
	})
}

func TestFeedbackPostgresFeedbackWriteConcurrency(t *testing.T) {
	t.Run("reversed references use one deterministic chunk order", func(t *testing.T) {
		firstDB, secondDB := setupFeedbackPostgresTestDatabases(t)
		firstSession, firstMessage, chunks := seedFeedbackPostgresTestCase(t, firstDB, "order-a", "a", "b")
		secondSession, secondMessage := seedFeedbackPostgresOwnedMessage(
			t, firstDB, firstSession.TenantID, "order-b", firstSession.UserID,
		)
		firstRepo := &feedbackRepository{db: firstDB}
		secondRepo := &feedbackRepository{db: secondDB}
		_, err := firstRepo.CompleteAssistantMessageWithReferences(
			context.Background(), firstSession.TenantID, firstMessage,
			feedbackPostgresReferences(chunks["a"], chunks["b"]),
		)
		require.NoError(t, err)
		_, err = secondRepo.CompleteAssistantMessageWithReferences(
			context.Background(), secondSession.TenantID, secondMessage,
			feedbackPostgresReferences(chunks["b"], chunks["a"]),
		)
		require.NoError(t, err)

		firstLocked := newFeedbackPostgresBarrier()
		secondAttempted := newFeedbackPostgresBarrier()
		installFeedbackPostgresChunkQueryBarrier(t, firstDB, "after", firstLocked, true)
		installFeedbackPostgresChunkQueryBarrier(t, secondDB, "before", secondAttempted, false)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		firstResult := make(chan error, 1)
		go func() {
			_, applyErr := firstRepo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
				MessageTenantID: firstSession.TenantID, ActorTenantID: firstSession.TenantID,
				ActorUserID: firstSession.UserID, SessionID: firstSession.ID, MessageID: firstMessage.ID,
				Type: types.FeedbackTypeLike,
			})
			firstResult <- applyErr
		}()
		waitForFeedbackPostgresSignal(t, firstLocked.reached, "first feedback to lock both chunks")
		secondResult := make(chan error, 1)
		go func() {
			_, applyErr := secondRepo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
				MessageTenantID: secondSession.TenantID, ActorTenantID: secondSession.TenantID,
				ActorUserID: secondSession.UserID, SessionID: secondSession.ID, MessageID: secondMessage.ID,
				Type: types.FeedbackTypeLike,
			})
			secondResult <- applyErr
		}()
		waitForFeedbackPostgresSignal(t, secondAttempted.reached, "second feedback to attempt both chunks")
		firstLocked.unblock()
		waitForFeedbackPostgresError(t, firstResult, "first feedback")
		waitForFeedbackPostgresError(t, secondResult, "second feedback")

		for _, id := range []string{"a", "b"} {
			var chunk types.Chunk
			require.NoError(t, firstDB.First(&chunk, "id = ?", chunks[id].ID).Error)
			assert.EqualValues(t, 2, chunk.LikeCount)
			assert.Zero(t, chunk.DislikeCount)
		}
	})

	t.Run("same user writes serialize on its message", func(t *testing.T) {
		firstDB, secondDB := setupFeedbackPostgresTestDatabases(t)
		session, message, chunks := seedFeedbackPostgresTestCase(t, firstDB, "same-user", "a")
		firstRepo := &feedbackRepository{db: firstDB}
		secondRepo := &feedbackRepository{db: secondDB}
		_, err := firstRepo.CompleteAssistantMessageWithReferences(
			context.Background(), session.TenantID, message, feedbackPostgresReferences(chunks["a"]),
		)
		require.NoError(t, err)

		firstLocked := newFeedbackPostgresBarrier()
		secondAttempted := newFeedbackPostgresBarrier()
		installFeedbackPostgresChunkQueryBarrier(t, firstDB, "after", firstLocked, true)
		installFeedbackPostgresMessageQueryBarrier(t, secondDB, "before", secondAttempted, false)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		firstResult := make(chan error, 1)
		go func() {
			_, applyErr := firstRepo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
				MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
				ActorUserID: session.UserID, SessionID: session.ID, MessageID: message.ID,
				Type: types.FeedbackTypeLike,
			})
			firstResult <- applyErr
		}()
		waitForFeedbackPostgresSignal(t, firstLocked.reached, "first same-user write to lock the chunk")
		secondResult := make(chan error, 1)
		reason := types.FeedbackReasonInaccurate
		go func() {
			_, applyErr := secondRepo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
				MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
				ActorUserID: session.UserID, SessionID: session.ID, MessageID: message.ID,
				Type: types.FeedbackTypeDislike, ReasonCode: &reason,
			})
			secondResult <- applyErr
		}()
		waitForFeedbackPostgresSignal(t, secondAttempted.reached, "second same-user write to attempt the message")
		firstLocked.unblock()
		waitForFeedbackPostgresError(t, firstResult, "first same-user feedback")
		waitForFeedbackPostgresError(t, secondResult, "second same-user feedback")

		var chunk types.Chunk
		require.NoError(t, firstDB.First(&chunk, "id = ?", chunks["a"].ID).Error)
		assert.Zero(t, chunk.LikeCount)
		assert.EqualValues(t, 1, chunk.DislikeCount)
		var feedbackCount int64
		require.NoError(t, firstDB.Model(&types.MessageFeedback{}).Count(&feedbackCount).Error)
		assert.EqualValues(t, 1, feedbackCount)
	})

	t.Run("two owners converge on shared chunks", func(t *testing.T) {
		firstDB, secondDB := setupFeedbackPostgresTestDatabases(t)
		firstSession, firstMessage, chunks := seedFeedbackPostgresTestCase(t, firstDB, "owners-a", "a", "b")
		firstSession.UserID = "owner-a"
		require.NoError(t, firstDB.Exec(
			"UPDATE sessions SET user_id = ? WHERE id = ?", firstSession.UserID, firstSession.ID,
		).Error)
		secondSession, secondMessage := seedFeedbackPostgresOwnedMessage(
			t, firstDB, firstSession.TenantID, "owners-b", "owner-b",
		)
		firstRepo := &feedbackRepository{db: firstDB}
		secondRepo := &feedbackRepository{db: secondDB}
		_, err := firstRepo.CompleteAssistantMessageWithReferences(
			context.Background(), firstSession.TenantID, firstMessage,
			feedbackPostgresReferences(chunks["a"], chunks["b"]),
		)
		require.NoError(t, err)
		_, err = secondRepo.CompleteAssistantMessageWithReferences(
			context.Background(), secondSession.TenantID, secondMessage,
			feedbackPostgresReferences(chunks["a"], chunks["b"]),
		)
		require.NoError(t, err)

		firstLocked := newFeedbackPostgresBarrier()
		secondAttempted := newFeedbackPostgresBarrier()
		installFeedbackPostgresChunkQueryBarrier(t, firstDB, "after", firstLocked, true)
		installFeedbackPostgresChunkQueryBarrier(t, secondDB, "before", secondAttempted, false)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		firstResult := make(chan error, 1)
		secondResult := make(chan error, 1)
		go func() {
			_, applyErr := firstRepo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
				MessageTenantID: firstSession.TenantID, ActorTenantID: firstSession.TenantID,
				ActorUserID: firstSession.UserID, SessionID: firstSession.ID, MessageID: firstMessage.ID,
				Type: types.FeedbackTypeLike,
			})
			firstResult <- applyErr
		}()
		waitForFeedbackPostgresSignal(t, firstLocked.reached, "first owner to lock shared chunks")
		go func() {
			_, applyErr := secondRepo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
				MessageTenantID: secondSession.TenantID, ActorTenantID: secondSession.TenantID,
				ActorUserID: secondSession.UserID, SessionID: secondSession.ID, MessageID: secondMessage.ID,
				Type: types.FeedbackTypeLike,
			})
			secondResult <- applyErr
		}()
		waitForFeedbackPostgresSignal(t, secondAttempted.reached, "second owner to attempt shared chunks")
		firstLocked.unblock()
		waitForFeedbackPostgresError(t, firstResult, "first owner feedback")
		waitForFeedbackPostgresError(t, secondResult, "second owner feedback")

		for _, id := range []string{"a", "b"} {
			var chunk types.Chunk
			require.NoError(t, firstDB.First(&chunk, "id = ?", chunks[id].ID).Error)
			assert.EqualValues(t, 2, chunk.LikeCount)
			assert.Zero(t, chunk.DislikeCount)
		}
	})
}

func TestFeedbackPostgresResetEventBoundary(t *testing.T) {
	t.Run("old like reset same-rating no-op stays excluded", func(t *testing.T) {
		db, _ := setupFeedbackPostgresTestDatabases(t)
		session, message, chunks := seedFeedbackPostgresTestCase(t, db, "reset-noop", "a")
		repo := feedbackPostgresTestRepository(db)
		_, err := repo.CompleteAssistantMessageWithReferences(
			context.Background(), session.TenantID, message, feedbackPostgresReferences(chunks["a"]),
		)
		require.NoError(t, err)
		applyFeedbackPostgresRating(t, repo, session, message, types.FeedbackTypeLike, nil)
		before := loadFeedbackPostgresEvent(t, db, message.ID)
		require.NoError(t, repo.ResetChunkFeedback(context.Background(), types.ResetChunkFeedbackInput{
			ChunkTenantID: chunks["a"].TenantID, ActorTenantID: session.TenantID,
			ActorUserID: "admin", KnowledgeBaseID: chunks["a"].KnowledgeBaseID, ChunkID: chunks["a"].ID,
		}))

		applyFeedbackPostgresRating(t, repo, session, message, types.FeedbackTypeLike, nil)
		after := loadFeedbackPostgresEvent(t, db, message.ID)
		projection := loadFeedbackPostgresProjection(t, db, chunks["a"].ID)
		assert.Equal(t, before.FeedbackAt, after.FeedbackAt)
		assert.Zero(t, projection.LikeCount)
		assert.Zero(t, projection.DislikeCount)
	})

	t.Run("old dislike reset reason-only update stays excluded", func(t *testing.T) {
		db, _ := setupFeedbackPostgresTestDatabases(t)
		session, message, chunks := seedFeedbackPostgresTestCase(t, db, "reset-reason", "a")
		repo := feedbackPostgresTestRepository(db)
		_, err := repo.CompleteAssistantMessageWithReferences(
			context.Background(), session.TenantID, message, feedbackPostgresReferences(chunks["a"]),
		)
		require.NoError(t, err)
		inaccurate := types.FeedbackReasonInaccurate
		applyFeedbackPostgresRating(t, repo, session, message, types.FeedbackTypeDislike, &inaccurate)
		before := loadFeedbackPostgresEvent(t, db, message.ID)
		require.NoError(t, repo.ResetChunkFeedback(context.Background(), types.ResetChunkFeedbackInput{
			ChunkTenantID: chunks["a"].TenantID, ActorTenantID: session.TenantID,
			ActorUserID: "admin", KnowledgeBaseID: chunks["a"].KnowledgeBaseID, ChunkID: chunks["a"].ID,
		}))

		outdated := types.FeedbackReasonOutdated
		applyFeedbackPostgresRating(t, repo, session, message, types.FeedbackTypeDislike, &outdated)
		after := loadFeedbackPostgresEvent(t, db, message.ID)
		projection := loadFeedbackPostgresProjection(t, db, chunks["a"].ID)
		assert.Equal(t, before.FeedbackAt, after.FeedbackAt)
		assert.True(t, after.UpdatedAt.After(before.UpdatedAt))
		assert.Zero(t, projection.LikeCount)
		assert.Zero(t, projection.DislikeCount)
	})

	t.Run("explicit direction switch after reset is a new event", func(t *testing.T) {
		db, _ := setupFeedbackPostgresTestDatabases(t)
		session, message, chunks := seedFeedbackPostgresTestCase(t, db, "reset-switch", "a")
		repo := feedbackPostgresTestRepository(db)
		_, err := repo.CompleteAssistantMessageWithReferences(
			context.Background(), session.TenantID, message, feedbackPostgresReferences(chunks["a"]),
		)
		require.NoError(t, err)
		applyFeedbackPostgresRating(t, repo, session, message, types.FeedbackTypeLike, nil)
		require.NoError(t, repo.ResetChunkFeedback(context.Background(), types.ResetChunkFeedbackInput{
			ChunkTenantID: chunks["a"].TenantID, ActorTenantID: session.TenantID,
			ActorUserID: "admin", KnowledgeBaseID: chunks["a"].KnowledgeBaseID, ChunkID: chunks["a"].ID,
		}))
		resetProjection := loadFeedbackPostgresProjection(t, db, chunks["a"].ID)

		incomplete := types.FeedbackReasonIncomplete
		applyFeedbackPostgresRating(t, repo, session, message, types.FeedbackTypeDislike, &incomplete)
		event := loadFeedbackPostgresEvent(t, db, message.ID)
		projection := loadFeedbackPostgresProjection(t, db, chunks["a"].ID)
		require.NotNil(t, resetProjection.FeedbackResetAt)
		assert.True(t, event.FeedbackAt.After(*resetProjection.FeedbackResetAt))
		assert.Zero(t, projection.LikeCount)
		assert.EqualValues(t, 1, projection.DislikeCount)
	})

	t.Run("clear and resubmit after reset is a new event", func(t *testing.T) {
		db, _ := setupFeedbackPostgresTestDatabases(t)
		session, message, chunks := seedFeedbackPostgresTestCase(t, db, "reset-resubmit", "a")
		repo := feedbackPostgresTestRepository(db)
		_, err := repo.CompleteAssistantMessageWithReferences(
			context.Background(), session.TenantID, message, feedbackPostgresReferences(chunks["a"]),
		)
		require.NoError(t, err)
		applyFeedbackPostgresRating(t, repo, session, message, types.FeedbackTypeLike, nil)
		require.NoError(t, repo.ResetChunkFeedback(context.Background(), types.ResetChunkFeedbackInput{
			ChunkTenantID: chunks["a"].TenantID, ActorTenantID: session.TenantID,
			ActorUserID: "admin", KnowledgeBaseID: chunks["a"].KnowledgeBaseID, ChunkID: chunks["a"].ID,
		}))
		resetProjection := loadFeedbackPostgresProjection(t, db, chunks["a"].ID)
		applyFeedbackPostgresRating(t, repo, session, message, types.FeedbackTypeNone, nil)
		applyFeedbackPostgresRating(t, repo, session, message, types.FeedbackTypeLike, nil)

		event := loadFeedbackPostgresEvent(t, db, message.ID)
		projection := loadFeedbackPostgresProjection(t, db, chunks["a"].ID)
		require.NotNil(t, resetProjection.FeedbackResetAt)
		assert.True(t, event.FeedbackAt.After(*resetProjection.FeedbackResetAt))
		assert.EqualValues(t, 1, projection.LikeCount)
		assert.Zero(t, projection.DislikeCount)
	})

	t.Run("reset affects only the target of a multi-chunk attribution", func(t *testing.T) {
		db, _ := setupFeedbackPostgresTestDatabases(t)
		session, message, chunks := seedFeedbackPostgresTestCase(t, db, "reset-target", "a", "b")
		repo := feedbackPostgresTestRepository(db)
		_, err := repo.CompleteAssistantMessageWithReferences(
			context.Background(), session.TenantID, message,
			feedbackPostgresReferences(chunks["a"], chunks["b"]),
		)
		require.NoError(t, err)
		applyFeedbackPostgresRating(t, repo, session, message, types.FeedbackTypeLike, nil)
		require.NoError(t, repo.ResetChunkFeedback(context.Background(), types.ResetChunkFeedbackInput{
			ChunkTenantID: chunks["a"].TenantID, ActorTenantID: session.TenantID,
			ActorUserID: "admin", KnowledgeBaseID: chunks["a"].KnowledgeBaseID, ChunkID: chunks["a"].ID,
		}))

		target := loadFeedbackPostgresProjection(t, db, chunks["a"].ID)
		other := loadFeedbackPostgresProjection(t, db, chunks["b"].ID)
		assert.Zero(t, target.LikeCount)
		assert.EqualValues(t, 1, other.LikeCount)
		assert.Equal(t, 1.2, other.RecallWeight)
	})

	t.Run("reset audit failure rolls back projection and baseline", func(t *testing.T) {
		db, _ := setupFeedbackPostgresTestDatabases(t)
		session, message, chunks := seedFeedbackPostgresTestCase(t, db, "reset-rollback", "a")
		repo := feedbackPostgresTestRepository(db)
		_, err := repo.CompleteAssistantMessageWithReferences(
			context.Background(), session.TenantID, message, feedbackPostgresReferences(chunks["a"]),
		)
		require.NoError(t, err)
		applyFeedbackPostgresRating(t, repo, session, message, types.FeedbackTypeLike, nil)
		before := loadFeedbackPostgresProjection(t, db, chunks["a"].ID)
		var beforeAuditCount int64
		require.NoError(t, db.Model(&types.ChunkFeedbackAudit{}).Count(&beforeAuditCount).Error)
		require.NoError(t, db.Exec(`
			ALTER TABLE chunk_feedback_audits
			ADD CONSTRAINT reject_feedback_reset CHECK (action <> 'feedback_reset')
		`).Error)

		err = repo.ResetChunkFeedback(context.Background(), types.ResetChunkFeedbackInput{
			ChunkTenantID: chunks["a"].TenantID, ActorTenantID: session.TenantID,
			ActorUserID: "admin", KnowledgeBaseID: chunks["a"].KnowledgeBaseID, ChunkID: chunks["a"].ID,
		})
		require.Error(t, err)
		after := loadFeedbackPostgresProjection(t, db, chunks["a"].ID)
		var afterAuditCount int64
		require.NoError(t, db.Model(&types.ChunkFeedbackAudit{}).Count(&afterAuditCount).Error)
		assert.Equal(t, before.LikeCount, after.LikeCount)
		assert.Equal(t, before.DislikeCount, after.DislikeCount)
		assert.Equal(t, before.RecallWeight, after.RecallWeight)
		assert.Equal(t, before.FeedbackResetAt, after.FeedbackResetAt)
		assert.Equal(t, beforeAuditCount, afterAuditCount)
	})
}

func TestFeedbackPostgresResetLinearizesBeforeRatingMutation(t *testing.T) {
	resetDB, feedbackDB := setupFeedbackPostgresTestDatabases(t)
	session, message, chunks := seedFeedbackPostgresTestCase(t, resetDB, "reset-before-mutation", "a")
	resetRepo := feedbackPostgresTestRepository(resetDB)
	feedbackRepo := feedbackPostgresTestRepository(feedbackDB)
	_, err := resetRepo.CompleteAssistantMessageWithReferences(
		context.Background(), session.TenantID, message, feedbackPostgresReferences(chunks["a"]),
	)
	require.NoError(t, err)
	applyFeedbackPostgresRating(t, resetRepo, session, message, types.FeedbackTypeLike, nil)

	resetLocked := newFeedbackPostgresBarrier()
	feedbackAttempted := newFeedbackPostgresBarrier()
	installFeedbackPostgresChunkQueryBarrier(t, resetDB, "after", resetLocked, true)
	installFeedbackPostgresChunkQueryBarrier(t, feedbackDB, "before", feedbackAttempted, false)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	resetResult := make(chan error, 1)
	go func() {
		resetResult <- resetRepo.ResetChunkFeedback(ctx, types.ResetChunkFeedbackInput{
			ChunkTenantID: chunks["a"].TenantID, ActorTenantID: session.TenantID,
			ActorUserID: "admin", KnowledgeBaseID: chunks["a"].KnowledgeBaseID, ChunkID: chunks["a"].ID,
		})
	}()
	waitForFeedbackPostgresSignal(t, resetLocked.reached, "reset to lock the target chunk")

	feedbackResult := make(chan error, 1)
	reason := types.FeedbackReasonInaccurate
	go func() {
		_, mutationErr := feedbackRepo.ApplyMessageFeedback(ctx, types.ApplyMessageFeedbackInput{
			MessageTenantID: session.TenantID, ActorTenantID: session.TenantID,
			ActorUserID: session.UserID, SessionID: session.ID, MessageID: message.ID,
			Type: types.FeedbackTypeDislike, ReasonCode: &reason,
		})
		feedbackResult <- mutationErr
	}()
	waitForFeedbackPostgresSignal(t, feedbackAttempted.reached, "rating mutation to attempt the target chunk")
	resetLocked.unblock()
	waitForFeedbackPostgresError(t, resetResult, "reset")
	waitForFeedbackPostgresError(t, feedbackResult, "rating mutation")

	event := loadFeedbackPostgresEvent(t, resetDB, message.ID)
	projection := loadFeedbackPostgresProjection(t, resetDB, chunks["a"].ID)
	require.NotNil(t, projection.FeedbackResetAt)
	assert.True(t, event.FeedbackAt.After(*projection.FeedbackResetAt))
	assert.Zero(t, projection.LikeCount)
	assert.EqualValues(t, 1, projection.DislikeCount)
	assert.Equal(t, 0.8, projection.RecallWeight)
}
