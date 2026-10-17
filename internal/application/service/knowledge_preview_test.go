package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	appconfig "github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type previewRepoStub struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
}

func (s *previewRepoStub) GetKnowledgeByID(
	context.Context, uint64, string,
) (*types.Knowledge, error) {
	return s.knowledge, nil
}

type previewKBServiceStub struct {
	interfaces.KnowledgeBaseService
}

func (s *previewKBServiceStub) GetKnowledgeBaseByID(
	context.Context, string,
) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{}, nil
}

type previewFileServiceStub struct {
	interfaces.FileService
	data  []byte
	opens int
	err   error
}

func (s *previewFileServiceStub) GetFile(context.Context, string) (io.ReadCloser, error) {
	s.opens++
	if s.err != nil {
		return nil, s.err
	}
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

// countingPreviewReader is seekable like a local/MinIO object, and counts
// actual Read calls so tests can assert the directory-only fast path never
// buffers the file.
type countingPreviewReader struct {
	*bytes.Reader
	reads int
}

func (r *countingPreviewReader) Read(p []byte) (int, error) {
	r.reads++
	return r.Reader.Read(p)
}

func (r *countingPreviewReader) Close() error { return nil }

type seekablePreviewFileServiceStub struct {
	interfaces.FileService
	data  []byte
	opens int
}

func (s *seekablePreviewFileServiceStub) GetFile(
	context.Context, string,
) (io.ReadCloser, error) {
	s.opens++
	return &countingPreviewReader{Reader: bytes.NewReader(s.data)}, nil
}

type previewDocReaderStub struct {
	interfaces.DocumentReader
	calls   int
	lastReq *types.ConvertPreviewRequest
	result  *types.ConvertPreviewResult
	err     error
}

func (s *previewDocReaderStub) Read(
	context.Context, *types.ReadRequest,
) (*types.ReadResult, error) {
	return nil, errors.New("not used")
}

func (s *previewDocReaderStub) ConvertPreview(
	_ context.Context, req *types.ConvertPreviewRequest,
) (*types.ConvertPreviewResult, error) {
	s.calls++
	s.lastReq = req
	return s.result, s.err
}

// previewDocReaderWithoutConverter only implements DocumentReader.
type previewDocReaderWithoutConverter struct {
	interfaces.DocumentReader
}

func previewTestContext() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))
}

func buildPreviewDocx(t *testing.T, media map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	entries := map[string][]byte{
		"[Content_Types].xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Default Extension="png" ContentType="image/png"/>` +
			`<Default Extension="emf" ContentType="image/x-emf"/></Types>`),
		"word/document.xml": []byte(`<w:document/>`),
		"word/_rels/document.xml.rels": []byte(`<?xml version="1.0"?>` +
			`<Relationships><Relationship Id="rId1" ` +
			`Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" ` +
			`Target="media/image1.emf"/></Relationships>`),
	}
	for name, data := range media {
		entries[name] = data
	}
	for name, data := range entries {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	return buffer.Bytes()
}

func newPreviewService(
	knowledge *types.Knowledge,
	files interfaces.FileService,
	reader interfaces.DocumentReader,
	cacheDir string,
	enabled bool,
) (*knowledgeService, *previewRepoStub) {
	repo := &previewRepoStub{knowledge: knowledge}
	service := &knowledgeService{
		config: &appconfig.Config{Preview: &appconfig.PreviewConfig{
			DocxEMF:  enabled,
			CacheDir: cacheDir,
		}},
		repo:           repo,
		kbService:      &previewKBServiceStub{},
		fileSvc:        files,
		documentReader: reader,
	}
	return service, repo
}

func docxKnowledge() *types.Knowledge {
	return &types.Knowledge{
		ID:              "k1",
		TenantID:        42,
		KnowledgeBaseID: "kb1",
		FileName:        "manual.docx",
		FileType:        "docx",
		FilePath:        "",
		FileHash:        strings.Repeat("a", 32),
	}
}

func TestGetKnowledgePreviewFileDisabledStreamsOriginal(t *testing.T) {
	original := buildPreviewDocx(t, map[string][]byte{
		"word/media/image1.emf": []byte("emf-bytes"),
	})
	files := &previewFileServiceStub{data: original}
	service, _ := newPreviewService(
		docxKnowledge(), files, &previewDocReaderStub{}, t.TempDir(), false,
	)

	preview, err := service.GetKnowledgePreviewFile(previewTestContext(), "k1")
	if err != nil {
		t.Fatalf("GetKnowledgePreviewFile: %v", err)
	}
	defer preview.Reader.Close()
	got, _ := io.ReadAll(preview.Reader)
	if !bytes.Equal(got, original) {
		t.Fatal("disabled feature must serve the original bytes")
	}
	if preview.Converted || preview.FallbackReason != "" {
		t.Fatalf("unexpected conversion state: %+v", preview)
	}
}

func TestGetKnowledgePreviewFileNonDocxStreamsOriginal(t *testing.T) {
	knowledge := docxKnowledge()
	knowledge.FileName = "manual.pdf"
	files := &previewFileServiceStub{data: []byte("%PDF-1.7")}
	reader := &previewDocReaderStub{}
	service, _ := newPreviewService(knowledge, files, reader, t.TempDir(), true)

	preview, err := service.GetKnowledgePreviewFile(previewTestContext(), "k1")
	if err != nil {
		t.Fatalf("GetKnowledgePreviewFile: %v", err)
	}
	defer preview.Reader.Close()
	got, _ := io.ReadAll(preview.Reader)
	if string(got) != "%PDF-1.7" {
		t.Fatalf("got %q, want the original PDF", got)
	}
	if reader.calls != 0 {
		t.Fatalf("converter calls = %d, want 0", reader.calls)
	}
}

func TestGetKnowledgePreviewFileWithoutVectorMediaSkipsConversion(t *testing.T) {
	original := buildPreviewDocx(t, map[string][]byte{
		"word/media/image1.png": []byte("png-bytes"),
	})
	files := &previewFileServiceStub{data: original}
	reader := &previewDocReaderStub{}
	service, _ := newPreviewService(docxKnowledge(), files, reader, t.TempDir(), true)

	preview, err := service.GetKnowledgePreviewFile(previewTestContext(), "k1")
	if err != nil {
		t.Fatalf("GetKnowledgePreviewFile: %v", err)
	}
	defer preview.Reader.Close()
	got, _ := io.ReadAll(preview.Reader)
	if !bytes.Equal(got, original) {
		t.Fatal("PNG-only DOCX must be served unchanged")
	}
	if reader.calls != 0 {
		t.Fatalf("converter calls = %d, want 0", reader.calls)
	}
	if preview.Converted {
		t.Fatal("Converted = true for a file that needed no conversion")
	}
}

func TestGetKnowledgePreviewFileStreamsNonVectorFileWithoutBuffering(t *testing.T) {
	original := buildPreviewDocx(t, map[string][]byte{
		"word/media/image1.png": []byte("png-bytes"),
	})
	files := &seekablePreviewFileServiceStub{data: original}
	reader := &previewDocReaderStub{}
	service, _ := newPreviewService(docxKnowledge(), files, reader, t.TempDir(), true)

	preview, err := service.GetKnowledgePreviewFile(previewTestContext(), "k1")
	if err != nil {
		t.Fatalf("GetKnowledgePreviewFile: %v", err)
	}
	defer preview.Reader.Close()
	counting, ok := preview.Reader.(*countingPreviewReader)
	if !ok {
		t.Fatalf("reader = %T, want the original seekable stream", preview.Reader)
	}
	if counting.reads != 0 {
		t.Fatalf("Read calls = %d before the handler streams; the zip directory scan must use ReaderAt only", counting.reads)
	}
	got, _ := io.ReadAll(counting)
	if !bytes.Equal(got, original) {
		t.Fatal("streamed bytes differ from the original file")
	}
	if reader.calls != 0 {
		t.Fatalf("converter calls = %d, want 0", reader.calls)
	}
}

func TestGetKnowledgePreviewFileConvertsAndCaches(t *testing.T) {
	original := buildPreviewDocx(t, map[string][]byte{
		"word/media/image1.emf": []byte("emf-bytes"),
	})
	converted := []byte("converted-docx-bytes")
	files := &previewFileServiceStub{data: original}
	reader := &previewDocReaderStub{result: &types.ConvertPreviewResult{
		FileContent: converted,
		Converted:   true,
	}}
	cacheDir := t.TempDir()
	service, _ := newPreviewService(docxKnowledge(), files, reader, cacheDir, true)
	ctx := previewTestContext()

	first, err := service.GetKnowledgePreviewFile(ctx, "k1")
	if err != nil {
		t.Fatalf("first preview: %v", err)
	}
	got, _ := io.ReadAll(first.Reader)
	_ = first.Reader.Close()
	if !bytes.Equal(got, converted) || !first.Converted {
		t.Fatalf("first preview = %q converted=%v, want the converted copy", got, first.Converted)
	}
	if reader.calls != 1 || files.opens != 1 {
		t.Fatalf("converter calls=%d source opens=%d, want 1/1", reader.calls, files.opens)
	}
	if reader.lastReq == nil || reader.lastReq.FileType != "docx" || reader.lastReq.FileName != "manual.docx" {
		t.Fatalf("unexpected conversion request: %+v", reader.lastReq)
	}

	second, err := service.GetKnowledgePreviewFile(ctx, "k1")
	if err != nil {
		t.Fatalf("second preview: %v", err)
	}
	got, _ = io.ReadAll(second.Reader)
	_ = second.Reader.Close()
	if !bytes.Equal(got, converted) {
		t.Fatal("cached preview bytes differ from the converted copy")
	}
	if reader.calls != 1 {
		t.Fatalf("converter calls = %d after cache hit, want 1", reader.calls)
	}
	if files.opens != 1 {
		t.Fatalf("source opens = %d after cache hit, want 1 (cache must answer before reading storage)", files.opens)
	}
}

func TestGetKnowledgePreviewFileHashesContentWhenFileHashMissing(t *testing.T) {
	original := buildPreviewDocx(t, map[string][]byte{
		"word/media/image1.emf": []byte("emf-bytes"),
	})
	converted := []byte("converted-docx-bytes")
	files := &previewFileServiceStub{data: original}
	reader := &previewDocReaderStub{result: &types.ConvertPreviewResult{
		FileContent: converted,
		Converted:   true,
	}}
	knowledge := docxKnowledge()
	knowledge.FileHash = ""
	service, _ := newPreviewService(knowledge, files, reader, t.TempDir(), true)
	ctx := previewTestContext()

	for i := 0; i < 2; i++ {
		preview, err := service.GetKnowledgePreviewFile(ctx, "k1")
		if err != nil {
			t.Fatalf("preview %d: %v", i+1, err)
		}
		got, _ := io.ReadAll(preview.Reader)
		_ = preview.Reader.Close()
		if !bytes.Equal(got, converted) {
			t.Fatalf("preview %d bytes = %q, want the converted copy", i+1, got)
		}
	}
	if reader.calls != 1 {
		t.Fatalf("converter calls = %d, want 1 (content-hash cache must answer the second request)", reader.calls)
	}
}

func TestGetKnowledgePreviewFileFallsBackOnConversionError(t *testing.T) {
	original := buildPreviewDocx(t, map[string][]byte{
		"word/media/image1.emf": []byte("emf-bytes"),
	})
	files := &previewFileServiceStub{data: original}
	reader := &previewDocReaderStub{err: errors.New("docreader down")}
	service, _ := newPreviewService(docxKnowledge(), files, reader, t.TempDir(), true)

	preview, err := service.GetKnowledgePreviewFile(previewTestContext(), "k1")
	if err != nil {
		t.Fatalf("GetKnowledgePreviewFile: %v", err)
	}
	defer preview.Reader.Close()
	got, _ := io.ReadAll(preview.Reader)
	if !bytes.Equal(got, original) {
		t.Fatal("conversion failure must fall back to the original bytes")
	}
	if preview.FallbackReason == "" {
		t.Fatal("FallbackReason is empty, want a degradation marker")
	}
	if preview.Converted {
		t.Fatal("Converted = true after a failed conversion")
	}
}

func TestGetKnowledgePreviewFileFallsBackWithoutPreviewConverter(t *testing.T) {
	original := buildPreviewDocx(t, map[string][]byte{
		"word/media/image1.emf": []byte("emf-bytes"),
	})
	files := &previewFileServiceStub{data: original}
	service, _ := newPreviewService(
		docxKnowledge(), files, &previewDocReaderWithoutConverter{}, t.TempDir(), true,
	)

	preview, err := service.GetKnowledgePreviewFile(previewTestContext(), "k1")
	if err != nil {
		t.Fatalf("GetKnowledgePreviewFile: %v", err)
	}
	defer preview.Reader.Close()
	got, _ := io.ReadAll(preview.Reader)
	if !bytes.Equal(got, original) {
		t.Fatal("a reader without PreviewConverter must serve the original bytes")
	}
	if preview.FallbackReason == "" {
		t.Fatal("FallbackReason is empty for an unsupported transport")
	}
}

func TestDocxHasVectorMedia(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{
			name: "emf media",
			data: buildPreviewDocx(t, map[string][]byte{"word/media/image3.emf": []byte("x")}),
			want: true,
		},
		{
			name: "wmf media",
			data: buildPreviewDocx(t, map[string][]byte{"word/media/image2.wmf": []byte("x")}),
			want: true,
		},
		{
			name: "png only",
			data: buildPreviewDocx(t, map[string][]byte{"word/media/image1.png": []byte("x")}),
			want: false,
		},
		{
			name: "vector outside media",
			data: buildPreviewDocx(t, map[string][]byte{"word/embeddings/ole.emf": []byte("x")}),
			want: false,
		},
		{
			name: "not a zip",
			data: []byte("not-a-docx"),
			want: false,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := docxHasVectorMedia(test.data); got != test.want {
				t.Fatalf("docxHasVectorMedia = %v, want %v", got, test.want)
			}
		})
	}
}
