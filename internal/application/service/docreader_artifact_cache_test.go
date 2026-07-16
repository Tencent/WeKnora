package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type docReaderArtifactFakeReader struct {
	result   *types.ReadResult
	err      error
	calls    int
	identity string
}

func (r *docReaderArtifactFakeReader) Read(context.Context, *types.ReadRequest) (*types.ReadResult, error) {
	r.calls++
	return r.result, r.err
}

func (r *docReaderArtifactFakeReader) Reconnect(string) error { return nil }
func (r *docReaderArtifactFakeReader) IsConnected() bool      { return true }
func (r *docReaderArtifactFakeReader) ArtifactIdentity() string {
	return r.identity
}
func (r *docReaderArtifactFakeReader) ListEngines(
	context.Context, map[string]string,
) ([]types.ParserEngineInfo, error) {
	return nil, nil
}

type docReaderArtifactFakeStore struct {
	values       map[types.ProcessingArtifactKey][]byte
	getErr       error
	putErr       error
	putCanonical []byte
	hasCanonical bool
	getCalls     int
	putCalls     int
}

func newDocReaderArtifactFakeStore() *docReaderArtifactFakeStore {
	return &docReaderArtifactFakeStore{values: make(map[types.ProcessingArtifactKey][]byte)}
}

func (s *docReaderArtifactFakeStore) Get(
	_ context.Context,
	key types.ProcessingArtifactKey,
) ([]byte, bool, error) {
	s.getCalls++
	if s.getErr != nil {
		return nil, false, s.getErr
	}
	value, ok := s.values[key]
	return append([]byte(nil), value...), ok, nil
}

func (s *docReaderArtifactFakeStore) PutIfAbsent(
	_ context.Context,
	key types.ProcessingArtifactKey,
	value []byte,
) ([]byte, bool, error) {
	s.putCalls++
	if s.putErr != nil {
		return nil, false, s.putErr
	}
	if s.hasCanonical {
		return append([]byte(nil), s.putCanonical...), false, nil
	}
	if canonical, ok := s.values[key]; ok {
		return append([]byte(nil), canonical...), false, nil
	}
	s.values[key] = append([]byte(nil), value...)
	return append([]byte(nil), value...), true, nil
}

func TestNewDocReaderArtifactKeyStabilityAndInvalidation(t *testing.T) {
	base := testDocReaderArtifactRequest(&docReaderArtifactFakeReader{})
	base.readRequest.ParserEngineOverrides = map[string]string{
		"mineru_enable_ocr": "true",
		"mineru_language":   "zh",
	}

	baseKey, eligible, err := newDocReaderArtifactKey(base)
	require.NoError(t, err)
	require.True(t, eligible)
	stableKey, eligible, err := newDocReaderArtifactKey(base)
	require.NoError(t, err)
	require.True(t, eligible)
	assert.Equal(t, baseKey, stableKey)
	assert.Equal(t, "docreader.parse", baseKey.Stage)
	assert.Equal(t, uint16(1), baseKey.KeyVersion)

	reordered := base
	reordered.readRequest = cloneDocReaderReadRequest(base.readRequest)
	reordered.readRequest.ParserEngineOverrides = map[string]string{
		"mineru_language":   "zh",
		"mineru_enable_ocr": "true",
	}
	reorderedKey, eligible, err := newDocReaderArtifactKey(reordered)
	require.NoError(t, err)
	require.True(t, eligible)
	assert.Equal(t, baseKey, reorderedKey)

	tests := []struct {
		name   string
		mutate func(*docReaderArtifactRequest)
	}{
		{name: "file bytes", mutate: func(r *docReaderArtifactRequest) { r.readRequest.FileContent = []byte("other") }},
		{name: "parser engine", mutate: func(r *docReaderArtifactRequest) { r.readRequest.ParserEngine = "opendataloader" }},
		{name: "file type", mutate: func(r *docReaderArtifactRequest) { r.readRequest.FileType = "docx" }},
		{name: "file name", mutate: func(r *docReaderArtifactRequest) { r.readRequest.FileName = "renamed.pdf" }},
		{name: "title", mutate: func(r *docReaderArtifactRequest) { r.readRequest.Title = "Other title" }},
		{name: "render version", mutate: func(r *docReaderArtifactRequest) { r.renderVersion = "2" }},
		{name: "effective override", mutate: func(r *docReaderArtifactRequest) {
			r.readRequest.ParserEngineOverrides["mineru_language"] = "en"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := base
			changed.readRequest = cloneDocReaderReadRequest(base.readRequest)
			tt.mutate(&changed)
			changedKey, eligible, err := newDocReaderArtifactKey(changed)
			require.NoError(t, err)
			require.True(t, eligible)
			assert.NotEqual(t, baseKey, changedKey)
		})
	}
}

func TestNewDocReaderArtifactKeyUsesEffectiveDefaultEngine(t *testing.T) {
	for _, test := range []struct {
		name           string
		fileName       string
		fileType       string
		explicitEngine string
	}{
		{name: "simple format", fileName: "document.md", fileType: "md", explicitEngine: "simple"},
		{name: "non-simple format", fileName: "document.pdf", fileType: "pdf", explicitEngine: "builtin"},
	} {
		t.Run(test.name, func(t *testing.T) {
			implicit := testDocReaderArtifactRequest(&docReaderArtifactFakeReader{})
			implicit.readRequest.FileName = test.fileName
			implicit.readRequest.FileType = test.fileType
			implicit.readRequest.ParserEngine = ""
			explicit := implicit
			explicit.readRequest = cloneDocReaderReadRequest(implicit.readRequest)
			explicit.readRequest.ParserEngine = test.explicitEngine

			implicitKey, eligible, err := newDocReaderArtifactKey(implicit)
			require.NoError(t, err)
			require.True(t, eligible)
			explicitKey, eligible, err := newDocReaderArtifactKey(explicit)
			require.NoError(t, err)
			require.True(t, eligible)
			assert.Equal(t, explicitKey, implicitKey)
		})
	}
}

func TestNormalizeDocReaderArtifactEngineMatchesReaderRouting(t *testing.T) {
	assert.Equal(t, "mineru", normalizeDocReaderArtifactEngine(" MINERU "))
	assert.Empty(t, normalizeDocReaderArtifactEngine("  "))
}

func TestNewDocReaderArtifactKeyRequiresSharedReaderIdentity(t *testing.T) {
	request := testDocReaderArtifactRequest(&docReaderArtifactFakeReader{})
	request.readRequest.ParserEngine = "builtin"
	request.readerIdentity = ""

	_, eligible, err := newDocReaderArtifactKey(request)
	require.NoError(t, err)
	assert.False(t, eligible)

	request.readerIdentity = "http:https://docreader-one.example.com"
	first, eligible, err := newDocReaderArtifactKey(request)
	require.NoError(t, err)
	require.True(t, eligible)

	request.readerIdentity = "http:https://docreader-two.example.com"
	second, eligible, err := newDocReaderArtifactKey(request)
	require.NoError(t, err)
	require.True(t, eligible)
	assert.NotEqual(t, first, second)
}

func TestDocReaderArtifactOverridesProtectSecretsAndFailClosed(t *testing.T) {
	base := testDocReaderArtifactRequest(&docReaderArtifactFakeReader{})
	base.readRequest.ParserEngineOverrides = map[string]string{
		"mineru_api_key":        "first-secret",
		"mineru_endpoint":       "HTTPS://EXAMPLE.com/api/",
		"mineru_model":          "pipeline",
		"paddleocr_vl_endpoint": "https://paddle.example.com/v1",
	}

	baseKey, eligible, err := newDocReaderArtifactKey(base)
	require.NoError(t, err)
	require.True(t, eligible)

	credentialsChanged := base
	credentialsChanged.readRequest = cloneDocReaderReadRequest(base.readRequest)
	credentialsChanged.readRequest.ParserEngineOverrides["mineru_api_key"] = "second-secret"
	credentialsChanged.readRequest.ParserEngineOverrides["paddleocr_vl_endpoint"] =
		"https://other-paddle.example.com/v2"
	credentialsKey, eligible, err := newDocReaderArtifactKey(credentialsChanged)
	require.NoError(t, err)
	require.True(t, eligible)
	assert.Equal(t, baseKey, credentialsKey)

	canonical, eligible, err := canonicalDocReaderArtifactOverrides(
		base.readRequest.ParserEngine,
		base.readRequest.FileType,
		base.readRequest.ParserEngineOverrides,
	)
	require.NoError(t, err)
	require.True(t, eligible)
	assert.NotContains(t, string(canonical), "first-secret")
	assert.Contains(t, string(canonical), "https://example.com/api")
	assert.NotContains(t, string(canonical), "paddle.example.com")

	unknown := base
	unknown.readRequest = cloneDocReaderReadRequest(base.readRequest)
	unknown.readRequest.ParserEngineOverrides["mineru_new_parser_knob"] = "value"
	_, eligible, err = newDocReaderArtifactKey(unknown)
	require.NoError(t, err)
	assert.False(t, eligible)

	unrelatedUnknown := base
	unrelatedUnknown.readRequest = cloneDocReaderReadRequest(base.readRequest)
	unrelatedUnknown.readRequest.ParserEngineOverrides["paddleocr_vl_new_parser_knob"] = "value"
	unrelatedKey, eligible, err := newDocReaderArtifactKey(unrelatedUnknown)
	require.NoError(t, err)
	require.True(t, eligible)
	assert.Equal(t, baseKey, unrelatedKey)

	unsafeEndpoint := base
	unsafeEndpoint.readRequest = cloneDocReaderReadRequest(base.readRequest)
	unsafeEndpoint.readRequest.ParserEngineOverrides["mineru_endpoint"] = "file:///tmp/parser"
	_, eligible, err = newDocReaderArtifactKey(unsafeEndpoint)
	require.NoError(t, err)
	assert.False(t, eligible)
}

func TestDocReaderArtifactNonEquivalentEndpointBypassesCache(t *testing.T) {
	for _, endpoint := range []string{
		"https://user:password@example.com/api",
		"https://example.com/api?route=private",
		"https://example.com/api#private",
		" https://example.com/api",
		"https://example.com/api ",
	} {
		request := testDocReaderArtifactRequest(&docReaderArtifactFakeReader{})
		request.readRequest.ParserEngineOverrides = map[string]string{"mineru_endpoint": endpoint}

		_, eligible, err := newDocReaderArtifactKey(request)
		require.NoError(t, err)
		assert.False(t, eligible)
	}
}

func TestDocReaderArtifactOpenDataLoaderIgnoresMinerUEndpoint(t *testing.T) {
	base := testDocReaderArtifactRequest(&docReaderArtifactFakeReader{})
	base.readRequest.ParserEngine = "opendataloader"
	base.readerIdentity = "grpc:docreader:50051"

	baseKey, eligible, err := newDocReaderArtifactKey(base)
	require.NoError(t, err)
	require.True(t, eligible)

	changed := base
	changed.readRequest = cloneDocReaderReadRequest(base.readRequest)
	changed.readRequest.ParserEngineOverrides = map[string]string{
		"mineru_endpoint": "https://example.com/api?ignored=true",
	}
	changedKey, eligible, err := newDocReaderArtifactKey(changed)
	require.NoError(t, err)
	require.True(t, eligible)
	assert.Equal(t, baseKey, changedKey)
}

func TestNewDocReaderArtifactKeyBypassesUnstableInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*docReaderArtifactRequest)
	}{
		{name: "URL", mutate: func(r *docReaderArtifactRequest) { r.readRequest.URL = "https://example.com/document" }},
		{name: "temporary path", mutate: func(r *docReaderArtifactRequest) {
			r.sourcePath = filepath.Join(os.TempDir(), "upload", "document.pdf")
		}},
		{name: "audio file type", mutate: func(r *docReaderArtifactRequest) { r.readRequest.FileType = "audio/mpeg" }},
		{name: "audio extension file type", mutate: func(r *docReaderArtifactRequest) {
			r.readRequest.FileType = "mp3"
			r.readRequest.FileName = ""
		}},
		{name: "audio extension", mutate: func(r *docReaderArtifactRequest) { r.readRequest.FileName = "recording.mp3" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := testDocReaderArtifactRequest(&docReaderArtifactFakeReader{})
			tt.mutate(&request)
			_, eligible, err := newDocReaderArtifactKey(request)
			require.NoError(t, err)
			assert.False(t, eligible)
		})
	}
}

func TestDocReaderArtifactCodecRoundTripDropsUnsafeFields(t *testing.T) {
	original := &types.ReadResult{
		MarkdownContent: "# Parsed",
		Metadata:        map[string]string{"pages": "2"},
		ImageDirPath:    "/tmp/docreader-images",
		Error:           "must not persist",
		IsAudio:         true,
		AudioData:       []byte("audio"),
		ImageRefs: []types.ImageRef{{
			Filename:    "page-1.png",
			OriginalRef: "images/page-1.png",
			MimeType:    "image/png",
			StorageKey:  "tenant-bound/key",
			ImageData:   []byte("image-bytes"),
			IsOriginal:  true,
		}},
	}

	payload, err := encodeDocReaderArtifact(original)
	require.NoError(t, err)
	require.NotEmpty(t, payload)
	assert.Equal(t, byte(1), payload[0])

	decoded, err := decodeDocReaderArtifact(payload)
	require.NoError(t, err)
	assert.Equal(t, original.MarkdownContent, decoded.MarkdownContent)
	assert.Equal(t, original.Metadata, decoded.Metadata)
	require.Len(t, decoded.ImageRefs, 1)
	assert.Equal(t, original.ImageRefs[0].Filename, decoded.ImageRefs[0].Filename)
	assert.Equal(t, original.ImageRefs[0].OriginalRef, decoded.ImageRefs[0].OriginalRef)
	assert.Equal(t, original.ImageRefs[0].MimeType, decoded.ImageRefs[0].MimeType)
	assert.Equal(t, original.ImageRefs[0].ImageData, decoded.ImageRefs[0].ImageData)
	assert.True(t, decoded.ImageRefs[0].IsOriginal)
	assert.Empty(t, decoded.ImageRefs[0].StorageKey)
	assert.Empty(t, decoded.Error)
	assert.Empty(t, decoded.ImageDirPath)
	assert.False(t, decoded.IsAudio)
	assert.Empty(t, decoded.AudioData)
}

func TestDecodeDocReaderArtifactRejectsTrailingJSONValue(t *testing.T) {
	var payload bytes.Buffer
	payload.WriteByte(docReaderArtifactCodecVersion)
	writer := gzip.NewWriter(&payload)
	_, err := writer.Write([]byte(`{"markdown_content":"first"}{"markdown_content":"second"}`))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	_, err = decodeDocReaderArtifact(payload.Bytes())
	assert.ErrorIs(t, err, errDocReaderArtifactCodec)
}

func TestReadDocReaderArtifactCachesSuccessfulResult(t *testing.T) {
	store := newDocReaderArtifactFakeStore()
	reader := &docReaderArtifactFakeReader{result: testDocReaderResult("fresh")}
	request := testDocReaderArtifactRequest(reader)

	first, hit, err := readDocReaderArtifact(context.Background(), store, request)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, "fresh", first.MarkdownContent)

	reader.result = testDocReaderResult("unexpected")
	second, hit, err := readDocReaderArtifact(context.Background(), store, request)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Equal(t, "fresh", second.MarkdownContent)
	assert.Equal(t, 1, reader.calls)
	assert.Equal(t, 1, store.putCalls)
}

func TestReadDocReaderArtifactBypassCallsReaderWithoutStore(t *testing.T) {
	store := newDocReaderArtifactFakeStore()
	reader := &docReaderArtifactFakeReader{result: testDocReaderResult("fresh")}
	request := testDocReaderArtifactRequest(reader)
	request.readRequest.URL = "https://example.com/document"

	result, hit, err := readDocReaderArtifact(context.Background(), store, request)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, "fresh", result.MarkdownContent)
	assert.Equal(t, 1, reader.calls)
	assert.Zero(t, store.getCalls)
	assert.Zero(t, store.putCalls)
}

func TestReadDocReaderArtifactDoesNotCacheFailuresOrAudio(t *testing.T) {
	t.Run("provider error", func(t *testing.T) {
		store := newDocReaderArtifactFakeStore()
		providerErr := errors.New("reader unavailable")
		reader := &docReaderArtifactFakeReader{err: providerErr}

		_, _, err := readDocReaderArtifact(context.Background(), store, testDocReaderArtifactRequest(reader))
		assert.ErrorIs(t, err, errDocReaderArtifactProvider)
		assert.ErrorContains(t, err, providerErr.Error())
		assert.Zero(t, store.putCalls)
	})

	t.Run("result error", func(t *testing.T) {
		store := newDocReaderArtifactFakeStore()
		reader := &docReaderArtifactFakeReader{result: &types.ReadResult{Error: "parse failed"}}

		result, hit, err := readDocReaderArtifact(context.Background(), store, testDocReaderArtifactRequest(reader))
		require.NoError(t, err)
		assert.False(t, hit)
		assert.Equal(t, "parse failed", result.Error)
		assert.Zero(t, store.putCalls)
	})

	t.Run("audio result", func(t *testing.T) {
		store := newDocReaderArtifactFakeStore()
		reader := &docReaderArtifactFakeReader{result: &types.ReadResult{IsAudio: true, AudioData: []byte("audio")}}

		result, hit, err := readDocReaderArtifact(context.Background(), store, testDocReaderArtifactRequest(reader))
		require.NoError(t, err)
		assert.False(t, hit)
		assert.True(t, result.IsAudio)
		assert.Zero(t, store.putCalls)
	})
}

func TestReadDocReaderArtifactUsesValidConcurrentWinner(t *testing.T) {
	store := newDocReaderArtifactFakeStore()
	winner, err := encodeDocReaderArtifact(testDocReaderResult("winner"))
	require.NoError(t, err)
	store.putCanonical = winner
	store.hasCanonical = true
	reader := &docReaderArtifactFakeReader{result: testDocReaderResult("fresh")}

	result, hit, err := readDocReaderArtifact(
		context.Background(), store, testDocReaderArtifactRequest(reader),
	)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, "winner", result.MarkdownContent)
	assert.Equal(t, 1, reader.calls)
}

func TestReadDocReaderArtifactMalformedPayloadFallsBackToFreshResult(t *testing.T) {
	t.Run("hit", func(t *testing.T) {
		store := newDocReaderArtifactFakeStore()
		reader := &docReaderArtifactFakeReader{result: testDocReaderResult("fresh")}
		request := testDocReaderArtifactRequest(reader)
		key, eligible, err := newDocReaderArtifactKey(request)
		require.NoError(t, err)
		require.True(t, eligible)
		store.values[key] = []byte("malformed")

		result, hit, err := readDocReaderArtifact(context.Background(), store, request)
		require.NoError(t, err)
		assert.False(t, hit)
		assert.Equal(t, "fresh", result.MarkdownContent)
		assert.Equal(t, 1, reader.calls)
	})

	t.Run("concurrent winner", func(t *testing.T) {
		store := newDocReaderArtifactFakeStore()
		store.putCanonical = []byte("malformed")
		store.hasCanonical = true
		reader := &docReaderArtifactFakeReader{result: testDocReaderResult("fresh")}

		result, hit, err := readDocReaderArtifact(
			context.Background(), store, testDocReaderArtifactRequest(reader),
		)
		require.NoError(t, err)
		assert.False(t, hit)
		assert.Equal(t, "fresh", result.MarkdownContent)
		assert.Equal(t, 1, reader.calls)
	})
}

func TestReadDocReaderArtifactDistinguishesStoreErrors(t *testing.T) {
	t.Run("get", func(t *testing.T) {
		storeErr := errors.New("get failed")
		store := newDocReaderArtifactFakeStore()
		store.getErr = storeErr
		reader := &docReaderArtifactFakeReader{result: testDocReaderResult("fresh")}

		_, _, err := readDocReaderArtifact(context.Background(), store, testDocReaderArtifactRequest(reader))
		assert.ErrorIs(t, err, errDocReaderArtifactStore)
		assert.ErrorContains(t, err, storeErr.Error())
		assert.Zero(t, reader.calls)
	})

	t.Run("put", func(t *testing.T) {
		storeErr := errors.New("put failed")
		store := newDocReaderArtifactFakeStore()
		store.putErr = storeErr
		reader := &docReaderArtifactFakeReader{result: testDocReaderResult("fresh")}

		_, _, err := readDocReaderArtifact(context.Background(), store, testDocReaderArtifactRequest(reader))
		assert.ErrorIs(t, err, errDocReaderArtifactStore)
		assert.ErrorContains(t, err, storeErr.Error())
		assert.Equal(t, 1, reader.calls)
	})
}

func testDocReaderArtifactRequest(reader *docReaderArtifactFakeReader) docReaderArtifactRequest {
	return docReaderArtifactRequest{
		tenantID:       7,
		sourcePath:     filepath.Join("knowledge", "document.pdf"),
		read:           reader.Read,
		readerIdentity: "test:docreader",
		renderVersion:  "1",
		readRequest: &types.ReadRequest{
			FileContent:  []byte("document bytes"),
			FileName:     "document.pdf",
			FileType:     "pdf",
			Title:        "Document title",
			ParserEngine: "mineru",
		},
	}
}

func cloneDocReaderReadRequest(request *types.ReadRequest) *types.ReadRequest {
	clone := *request
	clone.FileContent = append([]byte(nil), request.FileContent...)
	clone.ParserEngineOverrides = make(map[string]string, len(request.ParserEngineOverrides))
	for key, value := range request.ParserEngineOverrides {
		clone.ParserEngineOverrides[key] = value
	}
	return &clone
}

func testDocReaderResult(markdown string) *types.ReadResult {
	return &types.ReadResult{
		MarkdownContent: markdown,
		Metadata:        map[string]string{"pages": "1"},
		ImageRefs: []types.ImageRef{{
			Filename:    "page.png",
			OriginalRef: "images/page.png",
			MimeType:    "image/png",
			ImageData:   []byte("image"),
		}},
	}
}
