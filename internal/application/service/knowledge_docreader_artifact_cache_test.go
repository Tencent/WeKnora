package service

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type docReaderArtifactFileService struct {
	interfaces.FileService
	content []byte
}

func (s docReaderArtifactFileService) GetFile(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.content)), nil
}

func TestConvertReusesDocReaderArtifact(t *testing.T) {
	store := newDocReaderArtifactFakeStore()
	reader := &docReaderArtifactFakeReader{
		result:   testDocReaderResult("first parse"),
		identity: "http:https://docreader-one.example.com",
	}
	service := &knowledgeService{
		documentReader: reader,
		fileSvc:        docReaderArtifactFileService{content: []byte("document bytes")},
		artifactStore:  store,
	}
	payload := types.DocumentProcessPayload{
		TenantID: 7,
		FilePath: "knowledge/document.pdf",
		FileName: "document.pdf",
		FileType: "pdf",
	}
	kb := &types.KnowledgeBase{ID: "kb-1"}
	knowledge := &types.Knowledge{ID: "knowledge-1", TenantID: 7, Title: "Document"}
	effective := types.EffectiveProcessConfig{ChunkingConfig: types.ChunkingConfig{
		ParserEngineRules: []types.ParserEngineRule{{FileTypes: []string{"pdf"}, Engine: "builtin"}},
	}}

	first, err := service.convert(context.Background(), payload, kb, knowledge, effective, false)
	require.NoError(t, err)
	require.Equal(t, "first parse", first.MarkdownContent)

	reader.result = testDocReaderResult("second parse")
	second, err := service.convert(context.Background(), payload, kb, knowledge, effective, false)
	require.NoError(t, err)
	assert.Equal(t, "first parse", second.MarkdownContent)
	assert.Equal(t, 1, reader.calls)
	assert.Equal(t, 1, store.putCalls)
}

func TestConvertInvalidatesDocReaderArtifactWhenReaderIdentityChanges(t *testing.T) {
	store := newDocReaderArtifactFakeStore()
	reader := &docReaderArtifactFakeReader{
		result:   testDocReaderResult("first parse"),
		identity: "http:https://docreader-one.example.com",
	}
	service := &knowledgeService{
		documentReader: reader,
		fileSvc:        docReaderArtifactFileService{content: []byte("document bytes")},
		artifactStore:  store,
	}
	payload := types.DocumentProcessPayload{
		TenantID: 7,
		FilePath: "knowledge/document.pdf",
		FileName: "document.pdf",
		FileType: "pdf",
	}
	kb := &types.KnowledgeBase{ID: "kb-1"}
	knowledge := &types.Knowledge{ID: "knowledge-1", TenantID: 7, Title: "Document"}
	effective := types.EffectiveProcessConfig{ChunkingConfig: types.ChunkingConfig{
		ParserEngineRules: []types.ParserEngineRule{{FileTypes: []string{"pdf"}, Engine: "builtin"}},
	}}

	first, err := service.convert(context.Background(), payload, kb, knowledge, effective, false)
	require.NoError(t, err)
	require.Equal(t, "first parse", first.MarkdownContent)

	reader.identity = "http:https://docreader-two.example.com"
	reader.result = testDocReaderResult("second parse")
	second, err := service.convert(context.Background(), payload, kb, knowledge, effective, false)
	require.NoError(t, err)
	assert.Equal(t, "second parse", second.MarkdownContent)
	assert.Equal(t, 2, reader.calls)
}

func TestConvertDocReaderArtifactInvalidationAndBypass(t *testing.T) {
	t.Run("parser change invalidates", func(t *testing.T) {
		store := newDocReaderArtifactFakeStore()
		reader := &docReaderArtifactFakeReader{
			result:   testDocReaderResult("parsed"),
			identity: "test:docreader",
		}
		service := &knowledgeService{
			documentReader: reader,
			fileSvc:        docReaderArtifactFileService{content: []byte("document bytes")},
			artifactStore:  store,
		}
		payload := types.DocumentProcessPayload{TenantID: 7, FilePath: "knowledge/document.pdf", FileName: "document.pdf", FileType: "pdf"}
		kb := &types.KnowledgeBase{ID: "kb-1"}
		knowledge := &types.Knowledge{ID: "knowledge-1", TenantID: 7}
		builtin := types.EffectiveProcessConfig{ChunkingConfig: types.ChunkingConfig{ParserEngineRules: []types.ParserEngineRule{{FileTypes: []string{"pdf"}, Engine: "builtin"}}}}
		changed := types.EffectiveProcessConfig{ChunkingConfig: types.ChunkingConfig{ParserEngineRules: []types.ParserEngineRule{{FileTypes: []string{"pdf"}, Engine: "opendataloader"}}}}

		_, err := service.convert(context.Background(), payload, kb, knowledge, builtin, false)
		require.NoError(t, err)
		_, err = service.convert(context.Background(), payload, kb, knowledge, changed, false)
		require.NoError(t, err)
		assert.Equal(t, 2, reader.calls)
	})

	for _, test := range []struct {
		name      string
		withStore bool
		fileURL   string
	}{
		{name: "nil store", withStore: false},
		{name: "downloaded temporary URL", withStore: true, fileURL: "https://example.com/document.pdf"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &docReaderArtifactFakeReader{result: testDocReaderResult("parsed")}
			service := &knowledgeService{
				documentReader: reader,
				fileSvc:        docReaderArtifactFileService{content: []byte("document bytes")},
			}
			if test.withStore {
				service.artifactStore = newDocReaderArtifactFakeStore()
			}
			payload := types.DocumentProcessPayload{TenantID: 7, FilePath: "knowledge/document.pdf", FileName: "document.pdf", FileType: "pdf", FileURL: test.fileURL}
			kb := &types.KnowledgeBase{ID: "kb-1"}
			knowledge := &types.Knowledge{ID: "knowledge-1", TenantID: 7}
			effective := types.EffectiveProcessConfig{ChunkingConfig: types.ChunkingConfig{ParserEngineRules: []types.ParserEngineRule{{FileTypes: []string{"pdf"}, Engine: "builtin"}}}}

			_, err := service.convert(context.Background(), payload, kb, knowledge, effective, false)
			require.NoError(t, err)
			_, err = service.convert(context.Background(), payload, kb, knowledge, effective, false)
			require.NoError(t, err)
			assert.Equal(t, 2, reader.calls)
		})
	}
}
