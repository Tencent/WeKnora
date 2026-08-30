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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type chunkListTestRepository struct {
	interfaces.ChunkRepository
	children []*types.Chunk
}

func (r *chunkListTestRepository) ListChunksByParentIDs(
	context.Context, uint64, []string,
) ([]*types.Chunk, error) {
	return r.children, nil
}

type chunkListTestService struct {
	interfaces.ChunkService
	result *types.PageResult
	repo   interfaces.ChunkRepository
}

func (s *chunkListTestService) ListPagedChunksByKnowledgeID(
	context.Context, string, *types.Pagination, []types.ChunkType,
) (*types.PageResult, error) {
	return s.result, nil
}

func (s *chunkListTestService) GetRepository() interfaces.ChunkRepository {
	return s.repo
}

type chunkResourceFileService struct {
	interfaces.FileService
	url string
}

func (s *chunkResourceFileService) GetFileURL(context.Context, string) (string, error) {
	return s.url, nil
}

func newChunkListTestContext(t *testing.T, query string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	request := httptest.NewRequest(http.MethodGet, "/chunks/knowledge-1"+query, nil)
	request = request.WithContext(context.WithValue(request.Context(), types.TenantIDContextKey, uint64(1)))
	c.Request = request
	c.Params = gin.Params{{Key: "knowledge_id", Value: "knowledge-1"}}
	return c, response
}

func TestListKnowledgeChunksEnrichesAndRewritesImageInfo(t *testing.T) {
	const resourceHandle = "resource://image-1"
	textChunk := &types.Chunk{
		ID:        "text-1",
		Content:   "流程图：![diagram](" + resourceHandle + ")",
		ImageInfo: "",
	}
	imageChunk := &types.Chunk{
		ID:            "image-1",
		ParentChunkID: "text-1",
		ChunkType:     types.ChunkTypeImageCaption,
		IsEnabled:     true,
		ImageInfo:     `[{"url":"` + resourceHandle + `","caption":"流程图"}]`,
	}
	service := &chunkListTestService{
		result: types.NewPageResult(1, &types.Pagination{Page: 1, PageSize: 10}, []*types.Chunk{textChunk}),
		repo: &chunkListTestRepository{
			children: []*types.Chunk{imageChunk},
		},
	}
	h := &ChunkHandler{
		service:     service,
		fileService: &chunkResourceFileService{url: "https://cdn.example.com/image.png"},
	}
	c, response := newChunkListTestContext(t, "?resource_urls=public")

	h.ListKnowledgeChunks(c)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.NotContains(t, textChunk.Content, resourceHandle)
	assert.Contains(t, textChunk.Content, "https://cdn.example.com/image.png")
	assert.NotContains(t, textChunk.ImageInfo, resourceHandle)
	assert.Contains(t, textChunk.ImageInfo, "https://cdn.example.com/image.png")

	var body struct {
		Data []types.Chunk `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)
	assert.Equal(t, textChunk.Content, body.Data[0].Content)
	assert.Equal(t, textChunk.ImageInfo, body.Data[0].ImageInfo)
}
