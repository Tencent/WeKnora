package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type memoryExportLearningStub struct {
	interfaces.MemoryService
	learningLimit int
}

type memoryExportEmptyStub struct {
	interfaces.MemoryService
}

func (s *memoryExportEmptyStub) ListItems(
	context.Context, string, int, int,
) ([]*types.MemoryItem, int64, error) {
	return nil, 0, nil
}

func (s *memoryExportEmptyStub) LearningDocuments(
	context.Context, string, int,
) ([]*types.MemoryDocView, error) {
	return nil, nil
}

func (s *memoryExportLearningStub) ListItems(
	context.Context, string, int, int,
) ([]*types.MemoryItem, int64, error) {
	return []*types.MemoryItem{{ID: "memory-1", Content: "kept for compatibility"}}, 1, nil
}

func (s *memoryExportLearningStub) LearningDocuments(
	_ context.Context, knowledgeBaseID string, limit int,
) ([]*types.MemoryDocView, error) {
	if knowledgeBaseID != "" {
		return nil, nil
	}
	s.learningLimit = limit
	return []*types.MemoryDocView{{KnowledgeID: "document-1", Hits: 2}}, nil
}

func TestMemoryExportIncludesAuditableLearningProfile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &memoryExportLearningStub{}
	handler := NewMemoryHandler(service)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/memory/export", nil)

	handler.Export(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, `attachment; filename="weknora-memories.json"`, recorder.Header().Get("Content-Disposition"))
	require.Equal(t, memoryExportMaxItems+1, service.learningLimit)
	var body struct {
		Success         bool                `json:"success"`
		Data            []*types.MemoryItem `json:"data"`
		LearningProfile struct {
			SchemaVersion             int                    `json:"schema_version"`
			KnowledgeNode             string                 `json:"knowledge_node"`
			EvidenceKind              string                 `json:"evidence_kind"`
			MaxScoreWithoutAssessment int                    `json:"max_score_without_assessment"`
			Documents                 []*types.MemoryDocView `json:"documents"`
			DocumentsTruncated        bool                   `json:"documents_truncated"`
		} `json:"learning_profile"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Len(t, body.Data, 1, "the original memory export remains backward-compatible")
	require.Equal(t, 1, body.LearningProfile.SchemaVersion)
	require.Equal(t, "wiki_page", body.LearningProfile.KnowledgeNode)
	require.Equal(t, "answer_source_use", body.LearningProfile.EvidenceKind)
	require.Equal(t, 80, body.LearningProfile.MaxScoreWithoutAssessment)
	require.False(t, body.LearningProfile.DocumentsTruncated)
	require.Len(t, body.LearningProfile.Documents, 1)
	require.Equal(t, "document-1", body.LearningProfile.Documents[0].KnowledgeID)
}

func TestMemoryExportUsesEmptyArraysInsteadOfNull(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewMemoryHandler(&memoryExportEmptyStub{})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/memory/export", nil)

	handler.Export(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var body struct {
		Data            []*types.MemoryItem `json:"data"`
		LearningProfile struct {
			Documents []*types.MemoryDocView `json:"documents"`
		} `json:"learning_profile"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.NotNil(t, body.Data)
	require.Empty(t, body.Data)
	require.NotNil(t, body.LearningProfile.Documents)
	require.Empty(t, body.LearningProfile.Documents)
}
