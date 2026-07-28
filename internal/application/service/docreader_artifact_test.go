package service

import (
	"context"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type countingDocReader struct {
	mu     sync.Mutex
	calls  int
	result *types.ReadResult
}

func (r *countingDocReader) Read(context.Context, *types.ReadRequest) (*types.ReadResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	copy := *r.result
	copy.ImageRefs = append([]types.ImageRef(nil), r.result.ImageRefs...)
	copy.Metadata = make(map[string]string, len(r.result.Metadata))
	for key, value := range r.result.Metadata {
		copy.Metadata[key] = value
	}
	return &copy, nil
}

func (r *countingDocReader) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func setupDocReaderArtifactService(t *testing.T) *knowledgeService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.ProcessingArtifact{}))
	return &knowledgeService{
		artifactRuntime: artifact.NewRuntime(repository.NewProcessingArtifactRepository(db), nil),
	}
}

func docReaderArtifactContext() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
}

func TestDocReaderArtifactReusesExactSuccessfulResult(t *testing.T) {
	service := setupDocReaderArtifactService(t)
	reader := &countingDocReader{result: &types.ReadResult{
		MarkdownContent: "# Exact\r\ncontent ",
		Metadata:        map[string]string{"pages": "1"},
		ImageRefs: []types.ImageRef{{
			Filename:    "image.png",
			OriginalRef: "images/image.png",
			MimeType:    "image/png",
			ImageData:   []byte{1, 2, 3},
		}},
	}}
	request := &types.ReadRequest{
		FileContent:  []byte("document bytes"),
		FileName:     "document.pdf",
		FileType:     "pdf",
		Title:        "Document",
		ParserEngine: "builtin",
		RequestID:    "trace-one",
	}

	first, err := service.callDocReaderWithArtifact(docReaderArtifactContext(), reader, request)
	require.NoError(t, err)
	request.RequestID = "trace-two"
	second, err := service.callDocReaderWithArtifact(docReaderArtifactContext(), reader, request)
	require.NoError(t, err)

	assert.Equal(t, 1, reader.callCount())
	assert.Equal(t, first.MarkdownContent, second.MarkdownContent)
	require.Len(t, second.ImageRefs, 1)
	assert.Equal(t, []byte{1, 2, 3}, second.ImageRefs[0].ImageData)
}

func TestDocReaderArtifactInvalidatesExactContentAndOptions(t *testing.T) {
	service := setupDocReaderArtifactService(t)
	reader := &countingDocReader{result: &types.ReadResult{
		MarkdownContent: "content",
		Metadata:        map[string]string{},
	}}
	request := &types.ReadRequest{
		FileContent:  []byte("first"),
		FileName:     "document.pdf",
		FileType:     "pdf",
		ParserEngine: "builtin",
		ParserEngineOverrides: map[string]string{
			"pdf_force_scanned": "false",
		},
	}
	_, err := service.callDocReaderWithArtifact(docReaderArtifactContext(), reader, request)
	require.NoError(t, err)

	request.FileContent = []byte("second")
	_, err = service.callDocReaderWithArtifact(docReaderArtifactContext(), reader, request)
	require.NoError(t, err)
	request.ParserEngineOverrides["pdf_force_scanned"] = "true"
	_, err = service.callDocReaderWithArtifact(docReaderArtifactContext(), reader, request)
	require.NoError(t, err)
	assert.Equal(t, 3, reader.callCount())
}

func TestDocReaderArtifactExcludesCredentialRotation(t *testing.T) {
	service := setupDocReaderArtifactService(t)
	reader := &countingDocReader{result: &types.ReadResult{
		MarkdownContent: "content",
		Metadata:        map[string]string{},
	}}
	request := &types.ReadRequest{
		FileContent:  []byte("same"),
		FileName:     "document.pdf",
		FileType:     "pdf",
		ParserEngine: "mineru_cloud",
		ParserEngineOverrides: map[string]string{
			"mineru_api_key": "secret-one",
			"mineru_model":   "pipeline-v1",
		},
	}
	_, err := service.callDocReaderWithArtifact(docReaderArtifactContext(), reader, request)
	require.NoError(t, err)
	request.ParserEngineOverrides["mineru_api_key"] = "secret-two"
	_, err = service.callDocReaderWithArtifact(docReaderArtifactContext(), reader, request)
	require.NoError(t, err)

	assert.Equal(t, 1, reader.callCount())
}

func TestDocReaderArtifactDoesNotCacheProviderErrors(t *testing.T) {
	service := setupDocReaderArtifactService(t)
	reader := &countingDocReader{result: &types.ReadResult{
		Error:    "parse failed",
		Metadata: map[string]string{},
	}}
	request := &types.ReadRequest{
		FileContent:  []byte("same"),
		FileName:     "document.pdf",
		FileType:     "pdf",
		ParserEngine: "builtin",
	}

	first, err := service.callDocReaderWithArtifact(docReaderArtifactContext(), reader, request)
	require.NoError(t, err)
	second, err := service.callDocReaderWithArtifact(docReaderArtifactContext(), reader, request)
	require.NoError(t, err)
	assert.Equal(t, "parse failed", first.Error)
	assert.Equal(t, "parse failed", second.Error)
	assert.Equal(t, 2, reader.callCount())
}
