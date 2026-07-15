package handler

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type folderUploadKnowledgeServiceStub struct {
	interfaces.KnowledgeService
	folderID string
	kbID     string
}

func (s *folderUploadKnowledgeServiceStub) CreateKnowledgeFromFile(
	ctx context.Context,
	kbID string,
	_ *multipart.FileHeader,
	_ map[string]string,
	_ *bool,
	_ string,
	_ []string,
	_ string,
	_ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	s.kbID = kbID
	s.folderID, _ = ctx.Value(types.KnowledgeFolderIDContextKey).(string)
	return &types.Knowledge{ID: "doc-1", KnowledgeBaseID: kbID, FolderID: s.folderID, Title: "document.txt"}, nil
}

type folderUploadKBServiceStub struct {
	interfaces.KnowledgeBaseService
}

func (s *folderUploadKBServiceStub) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: "kb-1", TenantID: 9, Type: types.KnowledgeBaseTypeDocument}, nil
}

func TestCreateKnowledgeFromFileForwardsFolderID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	knowledgeService := &folderUploadKnowledgeServiceStub{}
	h := NewKnowledgeHandler(nil, knowledgeService, &folderUploadKBServiceStub{}, nil, nil, nil, nil)
	router := gin.New()
	router.POST("/knowledge-bases/:id/knowledge/file", func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(9))
		h.CreateKnowledgeFromFile(c)
	})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	filePart, err := writer.CreateFormFile("file", "document.txt")
	require.NoError(t, err)
	_, err = filePart.Write([]byte("folder upload"))
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("folder_id", "folder-7"))
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-1/knowledge/file", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "kb-1", knowledgeService.kbID)
	require.Equal(t, "folder-7", knowledgeService.folderID)
}
