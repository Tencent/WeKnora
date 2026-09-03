package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type metadataBatchKnowledgeLookup struct {
	items map[string]*types.Knowledge
}

func (s metadataBatchKnowledgeLookup) GetKnowledgeByIDOnly(
	_ context.Context,
	id string,
) (*types.Knowledge, error) {
	knowledge := s.items[id]
	if knowledge == nil {
		return nil, repository.ErrKnowledgeNotFound
	}
	return knowledge, nil
}

func TestKBIDFromKnowledgeIDsJSONRequiresOneKnowledgeBaseAndPreservesBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lookup := metadataBatchKnowledgeLookup{items: map[string]*types.Knowledge{
		"doc-a": {ID: "doc-a", KnowledgeBaseID: "kb-a"},
		"doc-b": {ID: "doc-b", KnowledgeBaseID: "kb-a"},
		"doc-c": {ID: "doc-c", KnowledgeBaseID: "kb-b"},
	}}
	resolver := KBIDFromKnowledgeIDsJSON(lookup)

	requestBody := []byte(`{"knowledge_ids":["doc-a","doc-b"]}`)
	request := httptest.NewRequest("POST", "/metadata", bytes.NewReader(requestBody))
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = request

	knowledgeBaseID, err := resolver(ctx)
	require.NoError(t, err)
	require.Equal(t, "kb-a", knowledgeBaseID)
	preserved, err := io.ReadAll(ctx.Request.Body)
	require.NoError(t, err)
	require.Equal(t, requestBody, preserved)

	ctx.Request = httptest.NewRequest(
		"POST",
		"/metadata",
		bytes.NewBufferString(`{"knowledge_ids":["doc-a","doc-c"]}`),
	)
	_, err = resolver(ctx)
	require.Error(t, err)
}

func TestKBIDFromKnowledgeIDsJSONRejectsUnknownDocument(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resolver := KBIDFromKnowledgeIDsJSON(metadataBatchKnowledgeLookup{items: map[string]*types.Knowledge{}})
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(
		"POST",
		"/metadata",
		bytes.NewBufferString(`{"knowledge_ids":["missing"]}`),
	)

	_, err := resolver(ctx)
	require.Error(t, err)
}

func TestKBIDFromKnowledgeIDsJSONRejectsMoreThanTwoHundredDocuments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resolver := KBIDFromKnowledgeIDsJSON(metadataBatchKnowledgeLookup{items: map[string]*types.Knowledge{}})
	knowledgeIDs := make([]string, 201)
	for index := range knowledgeIDs {
		knowledgeIDs[index] = "doc-a"
	}
	body, err := json.Marshal(map[string]any{"knowledge_ids": knowledgeIDs})
	require.NoError(t, err)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/metadata", bytes.NewReader(body))

	_, err = resolver(ctx)
	require.ErrorContains(t, err, "more than 200")
}
