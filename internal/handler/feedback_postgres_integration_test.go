package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestAgentFeedbackPostgresRouterIntegration(t *testing.T) {
	dsn := os.Getenv("WEKNORA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WEKNORA_TEST_POSTGRES_DSN is not set")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	schema := "agent_feedback_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, admin.Exec(`CREATE SCHEMA "`+schema+`"`).Error)
	t.Cleanup(func() {
		require.NoError(t, admin.Exec(`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`).Error)
	})

	parsedDSN, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsedDSN.Query()
	query.Set("search_path", schema)
	parsedDSN.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsedDSN.String()), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`
		CREATE TABLE sessions (
			id varchar(36) PRIMARY KEY,
			tenant_id bigint NOT NULL,
			user_id varchar(512) NOT NULL,
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
	`).Error)
	require.NoError(t, db.AutoMigrate(
		&types.Chunk{},
		&types.MessageChunkReference{},
		&types.MessageFeedback{},
		&types.ChunkFeedbackAudit{},
	))

	const messageTenantID uint64 = 101
	const chunkTenantID uint64 = 202
	const sessionID = "session-agent"
	const messageID = "message-agent"
	require.NoError(t, db.Exec(
		"INSERT INTO sessions (id, tenant_id, user_id) VALUES (?, ?, ?)",
		sessionID, messageTenantID, "user-a",
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO messages (id, session_id, content, role, is_completed) VALUES (?, ?, ?, ?, ?)",
		messageID, sessionID, "draft", "assistant", false,
	).Error)
	chunks := []*types.Chunk{
		{
			ID: "chunk-agent-a", TenantID: chunkTenantID, KnowledgeBaseID: "kb-agent",
			KnowledgeID: "knowledge-agent-a", Content: "source a", SourceContent: "source a",
			RecallWeight: 1, IsEnabled: true,
		},
		{
			ID: "chunk-agent-b", TenantID: chunkTenantID, KnowledgeBaseID: "kb-agent",
			KnowledgeID: "knowledge-agent-b", Content: "source b", SourceContent: "source b",
			RecallWeight: 1, IsEnabled: true,
		},
	}
	for _, chunk := range chunks {
		require.NoError(t, db.Create(chunk).Error)
	}

	cfg := &config.Config{Feedback: config.DefaultFeedbackConfig()}
	feedbackRepo := repository.NewFeedbackRepository(db, cfg)
	message := &types.Message{
		ID: messageID, SessionID: sessionID, Role: "assistant", Content: "agent answer",
		KnowledgeReferences: types.References{
			{ID: chunks[0].ID, KnowledgeBaseID: chunks[0].KnowledgeBaseID, ChunkType: string(types.ChunkTypeText)},
			{ID: chunks[1].ID, KnowledgeBaseID: chunks[1].KnowledgeBaseID, ChunkType: string(types.ChunkTypeText)},
		},
		CanonicalChunkReferencesSet: true,
		CanonicalChunkReferences: []types.ChunkFeedbackScope{
			{TenantID: chunkTenantID, KnowledgeBaseID: "kb-agent", ChunkID: chunks[0].ID},
			{TenantID: chunkTenantID, KnowledgeBaseID: "kb-agent", ChunkID: chunks[1].ID},
		},
	}
	eligible, err := feedbackRepo.CompleteAssistantMessageWithReferences(
		context.Background(), messageTenantID, message, message.KnowledgeReferences,
	)
	require.NoError(t, err)
	require.True(t, eligible)

	feedbackService := service.NewFeedbackService(feedbackRepo, cfg)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, messageTenantID)
		ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "user-a"})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	engine.PUT(
		"/api/v1/sessions/:session_id/messages/:message_id/feedback",
		NewFeedbackHandler(feedbackService).PutMessageFeedback,
	)

	putFeedback := func(feedbackType string) *httptest.ResponseRecorder {
		body, marshalErr := json.Marshal(map[string]string{"type": feedbackType})
		require.NoError(t, marshalErr)
		request := httptest.NewRequest(
			http.MethodPut,
			"/api/v1/sessions/"+sessionID+"/messages/"+messageID+"/feedback",
			bytes.NewReader(body),
		)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		return recorder
	}

	assert.Equal(t, http.StatusOK, putFeedback("like").Code)
	for _, chunk := range chunks {
		var persisted types.Chunk
		require.NoError(t, db.First(&persisted, "tenant_id = ? AND id = ?", chunkTenantID, chunk.ID).Error)
		assert.EqualValues(t, 1, persisted.LikeCount)
	}
	assert.Equal(t, http.StatusOK, putFeedback("none").Code)
	for _, chunk := range chunks {
		var persisted types.Chunk
		require.NoError(t, db.First(&persisted, "tenant_id = ? AND id = ?", chunkTenantID, chunk.ID).Error)
		assert.Zero(t, persisted.LikeCount)
	}
}
