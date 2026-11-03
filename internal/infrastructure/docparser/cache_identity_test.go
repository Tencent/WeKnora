package docparser

import (
	"encoding/base64"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser/anydoc"
	"github.com/Tencent/WeKnora/internal/types"
)

type identifiedFallback struct {
	stubDocReader
	identity string
}

func (r *identifiedFallback) ProcessingCacheIdentity() string { return r.identity }
func TestParserCachePPTXFallbackIdentity(t *testing.T) {
	req := &types.ReadRequest{FileType: "pptx", FileContent: presentationArchive(t, "ppt/media/chart.emf")}
	fallback := &identifiedFallback{identity: "first"}
	reader := NewAnydocReader(nil, fallback)
	first := ParserCacheIdentity(reader, req)
	fallback.identity = "second"
	if first == ParserCacheIdentity(reader, req) {
		t.Fatal("PPTX fallback identity ignored")
	}
	fallback.identity = ""
	if ParserCacheIdentity(reader, req) != "" {
		t.Fatal("unversioned PPTX fallback cached")
	}
	if ParserCacheIdentity(NewAnydocReader(map[string]string{"anydoc_extract_images": "false"}, fallback), req) == "" {
		t.Fatal("disabled image extraction unnecessarily required fallback identity")
	}
}

func TestParserCachePortableImageMarkdownSyntax(t *testing.T) {
	image := createTestPNG(2, 2)
	for _, markdown := range []string{
		`![a](images/a(b).png "title")`,
		`![a](<images/a(b).png> "title")`,
		`![a](images/a(b).png)`,
		`<img src="images/a(b).png">`,
	} {
		result := &types.ReadResult{
			MarkdownContent: markdown,
			ImageRefs:       []types.ImageRef{{OriginalRef: "images/a(b).png", Filename: "a(b).png", ImageData: image}},
		}
		if err := ValidateProcessingCacheArtifact(result); err != nil {
			t.Fatalf("%s: %v", markdown, err)
		}
	}
	inline := `![a](data:image/png;base64,` + base64.StdEncoding.EncodeToString(image) + ` "title")`
	if err := ValidateProcessingCacheArtifact(&types.ReadResult{MarkdownContent: inline}); err != nil {
		t.Fatal(err)
	}
	if ValidateProcessingCacheArtifact(&types.ReadResult{MarkdownContent: `![a](<missing.png> "title")`}) == nil {
		t.Fatal("missing image accepted")
	}
}

func TestAnydocPartialArtifactRefusesCaching(t *testing.T) {
	result := anydocReadResult(
		&anydoc.Result{Markdown: "usable text", AssetsError: errors.New("asset extraction failed")},
		"docx",
	)
	if result.MarkdownContent != "usable text" || ValidateProcessingCacheArtifact(result) == nil {
		t.Fatal("partial artifact must remain live but refuse persistence")
	}
}

func TestParserCacheIdentityEffectiveConfiguration(t *testing.T) {
	req := &types.ReadRequest{FileType: "docx"}
	t.Setenv("DOCREADER_CACHE_VERSION", "")
	if ParserCacheIdentity(NewMinerUReader(nil), req) != "" {
		t.Fatal("unversioned remote cached")
	}
	t.Setenv("DOCREADER_CACHE_VERSION", "v1")
	a := ParserCacheIdentity(
		NewMinerUReader(map[string]string{"mineru_endpoint": "https://a", "mineru_server_api_key": "secret"}),
		req,
	)
	for _, overrides := range []map[string]string{
		{"mineru_endpoint": "https://b", "mineru_server_api_key": "secret"},
		{"mineru_endpoint": "https://a", "mineru_server_api_key": "rotated"},
		{"mineru_endpoint": "https://a", "mineru_server_api_key": "secret", "mineru_language": "en"},
	} {
		if a == ParserCacheIdentity(NewMinerUReader(overrides), req) {
			t.Fatal("effective endpoint/credentials/config not included")
		}
	}
	if ParserCacheIdentity(
		NewAnydocReader(nil, nil),
		req,
	) == ParserCacheIdentity(
		NewAnydocReader(map[string]string{"anydoc_extract_images": "false"}, nil),
		req,
	) {
		t.Fatal("builtin config not included")
	}
}

func TestGRPCProcessingCacheIdentityRequiresVersion(t *testing.T) {
	t.Setenv("DOCREADER_CACHE_VERSION", "")
	a, e := NewGRPCDocumentReader("localhost:59991")
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	}()
	if a.ProcessingCacheIdentity() != "" {
		t.Fatal("unversioned remote must bypass")
	}
	t.Setenv("DOCREADER_CACHE_VERSION", "deploy-v1")
	b, e := NewGRPCDocumentReader("localhost:59991")
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	}()
	first := b.ProcessingCacheIdentity()
	if first == "" {
		t.Fatal("missing identity")
	}
	t.Setenv("DOCREADER_CACHE_VERSION", "deploy-v2")
	if b.ProcessingCacheIdentity() != first {
		t.Fatal("existing identity changed with environment")
	}
	c, e := NewGRPCDocumentReader("localhost:59991")
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	}()
	if c.ProcessingCacheIdentity() == first {
		t.Fatal("version failed to invalidate")
	}
}
