package service

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"strings"
	"testing"
)

type cacheParseReader struct {
	interfaces.DocumentReader
	identity  string
	calls     int
	result    *types.ReadResult
	err       error
	afterRead func()
}

func TestKnowledgeParseCacheProcessDocumentCurrentImageReferences(t *testing.T) {
	k := &types.Knowledge{ID: "cached-parse-entry", TenantID: 1, KnowledgeBaseID: "kb-1", FilePath: "input.docx", ParseStatus: types.ParseStatusPending}
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1, ChunkingConfig: types.ChunkingConfig{ParserEngineRules: []types.ParserEngineRule{{FileTypes: []string{"docx"}, Engine: "cache-test"}}}}
	reader := &cacheParseReader{identity: "parser-v1", result: &types.ReadResult{MarkdownContent: "Useful document text\n![figure](images/a.png)", ImageRefs: []types.ImageRef{{Filename: "a.png", OriginalRef: "images/a.png", ImageData: inlineImageTestPNG(t)}}}}
	store := &imageCacheStore{rows: map[string]*processingcache.Record{}}
	opts := processingcache.Options{ReadEnabled: true, WriteEnabled: true, MaxBytes: 1 << 20}
	catalog := &fakeBindCatalog{resources: make(map[string]*types.StoredResource)}
	storage := &inlineImageStorageState{catalog: catalog}
	chunks := &inlineImageChunkRepo{}
	s := &knowledgeService{repo: &embedFailureKnowledgeRepo{knowledge: k}, kbService: &reparseFailureKBService{kb: kb}, tenantRepo: &inlineImageTenantRepo{tenant: &types.Tenant{ID: 1}}, tenantService: &inlineImageTenantService{}, fileSvc: inlineImageFileService{content: "same bytes", state: storage}, imageResolver: docparser.NewImageResolver(), chunkRepo: chunks, graphEngine: parentChildGraphRepo{}, resourceCatalog: catalog, documentReader: reader, processingCache: processingcache.New(store, opts)}
	payload := types.DocumentProcessPayload{TenantID: 1, KnowledgeID: k.ID, KnowledgeBaseID: kb.ID, FilePath: k.FilePath, FileName: "input.docx", FileType: "docx", Attempt: 1}
	for i := 0; i < 2; i++ {
		k.ParseStatus = types.ParseStatusPending
		if i == 1 {
			kb.ChunkingConfig.ChunkSize = 128
			kb.ChunkingConfig.ChunkOverlap = 16
			s.processingCache = processingcache.New(store, opts)
		}
		if err := s.ProcessDocument(context.Background(), asynq.NewTask(types.TypeDocumentProcess, mustMarshalDocumentPayload(t, payload))); err != nil {
			t.Fatal(err)
		}
		if k.ParseStatus == types.ParseStatusFailed {
			t.Fatalf("parse failed: %s", k.ErrorMessage)
		}
		if len(storage.saved) != i+1 {
			t.Fatalf("image not rebound saves=%v", storage.saved)
		}
		var text strings.Builder
		for _, chunk := range chunks.chunks {
			text.WriteString(chunk.Content)
		}
		if !strings.Contains(text.String(), storage.saved[i]) {
			t.Fatalf("current reference absent: %s", text.String())
		}
		if i > 0 && strings.Contains(text.String(), storage.saved[0]) {
			t.Fatal("old image reference retained")
		}
	}
	if reader.calls != 1 {
		t.Fatalf("chunk-only settings invalidated parser: %d", reader.calls)
	}
}

func TestKnowledgeParseCacheAdmissionAndBudget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result *types.ReadResult
		limit  int
		err    error
	}{
		{name: "directory without bytes", result: &types.ReadResult{MarkdownContent: "text", ImageDirPath: "E:/old"}},
		{name: "image without bytes", result: &types.ReadResult{MarkdownContent: "![a](a.png)", ImageRefs: []types.ImageRef{{Filename: "a.png", OriginalRef: "a.png", StorageKey: "old"}}}},
		{name: "partial", result: &types.ReadResult{MarkdownContent: "partial", Error: "failed"}},
		{name: "nil"},
		{name: "failure", err: errors.New("reader failed")},
		{name: "oversize", result: &types.ReadResult{MarkdownContent: "oversized"}, limit: 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &imageCacheStore{rows: map[string]*processingcache.Record{}}
			limit := tc.limit
			if limit == 0 {
				limit = 1 << 20
			}
			s := &knowledgeService{processingCache: processingcache.New(store, processingcache.Options{ReadEnabled: true, WriteEnabled: true, MaxBytes: limit})}
			r := &cacheParseReader{identity: "v1", result: tc.result, err: tc.err}
			for i := 0; i < 2; i++ {
				s.readDocumentWithCache(context.Background(), r, &types.ReadRequest{FileContent: []byte("file")}, &types.Knowledge{ID: "doc", TenantID: 1})
			}
			if r.calls != 2 || len(store.rows) != 0 {
				t.Fatalf("invalid artifact reused calls=%d rows=%d", r.calls, len(store.rows))
			}
		})
	}
}

func TestKnowledgeParseCacheCancelAndCorrupt(t *testing.T) {
	store := &imageCacheStore{rows: map[string]*processingcache.Record{}}
	s := &knowledgeService{processingCache: processingcache.New(store, processingcache.Options{ReadEnabled: true, WriteEnabled: true, MaxBytes: 1 << 20})}
	r := &cacheParseReader{identity: "v1", result: &types.ReadResult{MarkdownContent: "text"}}
	req := &types.ReadRequest{FileContent: []byte("file")}
	k := &types.Knowledge{ID: "doc", TenantID: 1}
	ctx, cancel := context.WithCancel(context.Background())
	r.afterRead = cancel
	if _, err := s.readDocumentWithCache(ctx, r, req, k); !errors.Is(err, context.Canceled) || len(store.rows) != 0 {
		t.Fatalf("canceled persisted: %v", err)
	}
	r.afterRead = nil
	if _, err := s.readDocumentWithCache(context.Background(), r, req, k); err != nil {
		t.Fatal(err)
	}
	for _, row := range store.rows {
		row.Payload = []byte("corrupt")
	}
	if _, err := s.readDocumentWithCache(context.Background(), r, req, k); err != nil {
		t.Fatal(err)
	}
	if r.calls != 3 {
		t.Fatalf("corrupt hit calls=%d", r.calls)
	}
}

func TestKnowledgeParseCacheRequiresBoundedPayload(t *testing.T) {
	store := &imageCacheStore{rows: map[string]*processingcache.Record{}}
	s := &knowledgeService{processingCache: processingcache.New(store, processingcache.Options{ReadEnabled: true, WriteEnabled: true})}
	r := &cacheParseReader{identity: "v1", result: &types.ReadResult{MarkdownContent: "text"}}
	for i := 0; i < 2; i++ {
		s.readDocumentWithCache(context.Background(), r, &types.ReadRequest{}, &types.Knowledge{ID: "doc", TenantID: 1})
	}
	if r.calls != 2 || len(store.rows) != 0 {
		t.Fatal("parser cache accepted an unlimited payload budget")
	}
}

func TestKnowledgeParseCacheStageBudgetAndCanonicalText(t *testing.T) {
	store := &imageCacheStore{rows: map[string]*processingcache.Record{}}
	opts := processingcache.Options{ReadEnabled: true, WriteEnabled: true, MaxBytes: 8, StageMaxBytes: map[string]int{"document.parse.v1": 1 << 20}, Epoch: "1"}
	s := &knowledgeService{processingCache: processingcache.New(store, opts)}
	r := &cacheParseReader{identity: "v1", result: &types.ReadResult{MarkdownContent: string([]byte{'a', 0xff, 'b'})}}
	req := &types.ReadRequest{FileContent: []byte("file")}
	k := &types.Knowledge{ID: "doc", TenantID: 1}
	a, err := s.readDocumentWithCache(context.Background(), r, req, k)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.readDocumentWithCache(context.Background(), r, req, k)
	if err != nil || a.MarkdownContent != b.MarkdownContent || r.calls != 1 {
		t.Fatalf("canonical stage reuse calls=%d err=%v", r.calls, err)
	}
	k.TenantID = 2
	s.readDocumentWithCache(context.Background(), r, req, k)
	k.ID = "another"
	s.readDocumentWithCache(context.Background(), r, req, k)
	opts.Epoch = "2"
	s.processingCache = processingcache.New(store, opts)
	s.readDocumentWithCache(context.Background(), r, req, k)
	if r.calls != 4 {
		t.Fatalf("isolation/epoch calls=%d", r.calls)
	}
}

func (r *cacheParseReader) IsConnected() bool { return true }
func (r *cacheParseReader) ListEngines(context.Context, map[string]string) ([]types.ParserEngineInfo, error) {
	return nil, nil
}
func (r *cacheParseReader) ProcessingCacheIdentity() string { return r.identity }
func (r *cacheParseReader) Read(context.Context, *types.ReadRequest) (*types.ReadResult, error) {
	r.calls++
	if r.afterRead != nil {
		r.afterRead()
	}
	return r.result, r.err
}
func TestKnowledgeParseCacheActualBytesAndArtifacts(t *testing.T) {
	store := &imageCacheStore{rows: map[string]*processingcache.Record{}}
	s := &knowledgeService{processingCache: processingcache.New(store, processingcache.Options{ReadEnabled: true, WriteEnabled: true, MaxBytes: 1 << 20})}
	reader := &cacheParseReader{identity: "parser-v1", result: &types.ReadResult{MarkdownContent: "# Document\n![image](images/a.png)", ImageDirPath: "E:/temporary/old", ImageRefs: []types.ImageRef{{Filename: "a.png", OriginalRef: "images/a.png", StorageKey: "old storage", ImageData: testImageCachePNG(t)}}}}
	knowledge := &types.Knowledge{ID: "doc", TenantID: 1}
	req := &types.ReadRequest{FileContent: []byte("same file"), FileName: "a.docx", FileType: "docx", Title: "Document", ParserEngine: "test", RequestID: "first"}
	a, err := s.readDocumentWithCache(context.Background(), reader, req, knowledge)
	if err != nil {
		t.Fatal(err)
	}
	if a.ImageDirPath != "" || a.ImageRefs[0].StorageKey != "" {
		t.Fatal("cached ephemeral paths")
	}
	a.ImageRefs[0].ImageData[0] = 0
	req.RequestID = "current"
	s.processingCache = processingcache.New(store, processingcache.Options{ReadEnabled: true, WriteEnabled: true, MaxBytes: 1 << 20})
	b, err := s.readDocumentWithCache(context.Background(), reader, req, knowledge)
	if err != nil || b.ImageRefs[0].ImageData[0] == 0 || reader.calls != 1 {
		t.Fatalf("restart/retry clone calls=%d err=%v", reader.calls, err)
	}
	req.FileContent = []byte("changed file")
	s.readDocumentWithCache(context.Background(), reader, req, knowledge)
	reader.identity = "parser-v2"
	s.readDocumentWithCache(context.Background(), reader, req, knowledge)
	req.ParserEngineOverrides = map[string]string{"format": "new"}
	s.readDocumentWithCache(context.Background(), reader, req, knowledge)
	if reader.calls != 4 {
		t.Fatalf("bytes/engine/config invalidation calls=%d", reader.calls)
	}
	req.URL = "https://dynamic/content"
	for i := 0; i < 2; i++ {
		s.readDocumentWithCache(context.Background(), reader, req, knowledge)
	}
	if reader.calls != 6 {
		t.Fatal("cached dynamic URL by string")
	}
}
