package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestDocParseCacheKeyInvalidatesByContentParserAndConfig(t *testing.T) {
	req := &types.ReadRequest{
		FileName:     "guide.pdf",
		FileType:     ".pdf",
		Title:        "Guide",
		ParserEngine: "builtin",
		ParserEngineOverrides: map[string]string{
			"mode": "fast",
		},
	}
	contentHash := docParseContentHash([]byte("file bytes"))
	configHash := docParseConfigHash(req)
	base := docParseCacheKey(contentHash, "builtin", configHash)
	if base == "" {
		t.Fatal("cache key is empty")
	}
	if got := docParseCacheKey(contentHash, "builtin", docParseConfigHash(req)); got != base {
		t.Fatalf("same doc parse inputs should reuse cache key: %s != %s", got, base)
	}
	if got := docParseCacheKey(docParseContentHash([]byte("other bytes")), "builtin", configHash); got == base {
		t.Fatal("file content changes must invalidate doc parse cache")
	}
	if got := docParseCacheKey(contentHash, "mineru", configHash); got == base {
		t.Fatal("parser engine changes must invalidate doc parse cache")
	}
	changedReq := *req
	changedReq.FileType = ".docx"
	if got := docParseCacheKey(contentHash, "builtin", docParseConfigHash(&changedReq)); got == base {
		t.Fatal("file type changes must invalidate doc parse cache")
	}
	changedReq = *req
	changedReq.ParserEngineOverrides = map[string]string{"mode": "accurate"}
	if got := docParseCacheKey(contentHash, "builtin", docParseConfigHash(&changedReq)); got == base {
		t.Fatal("parser overrides changes must invalidate doc parse cache")
	}
}
