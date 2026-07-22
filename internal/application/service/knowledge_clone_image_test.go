package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// countingFileService is a minimal FileService stub for cloneChunkImageInfo tests.
// CopyFile records each invocation and returns a deterministic destination path
// derived from (knowledgeID, srcPath) so dedup and rewrite behaviour are verifiable.
type countingFileService struct {
	copyCalls   int
	copiedFrom  []string
	failOnURL   string // when non-empty, CopyFile returns an error for this srcPath
	deleteCalls int
}

func (c *countingFileService) CheckConnectivity(ctx context.Context) error { return nil }

func (c *countingFileService) SaveFile(ctx context.Context, file *multipart.FileHeader, tenantID uint64, knowledgeID string) (string, error) {
	return "", nil
}

func (c *countingFileService) SaveBytes(ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool) (string, error) {
	return "", nil
}

func (c *countingFileService) GetFile(ctx context.Context, filePath string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (c *countingFileService) GetFileURL(ctx context.Context, filePath string) (string, error) {
	return filePath, nil
}

func (c *countingFileService) DeleteFile(ctx context.Context, filePath string) error {
	c.deleteCalls++
	return nil
}

func (c *countingFileService) CopyFile(ctx context.Context, srcPath string, tenantID uint64, knowledgeID string) (string, error) {
	if c.failOnURL != "" && srcPath == c.failOnURL {
		return "", fmt.Errorf("simulated copy failure for %s", srcPath)
	}
	c.copyCalls++
	c.copiedFrom = append(c.copiedFrom, srcPath)
	return fmt.Sprintf("local://%d/%s/copy-of-%s", tenantID, knowledgeID, srcPath), nil
}

func mustImageInfoJSON(t *testing.T, imgs []types.ImageInfo) string {
	t.Helper()
	b, err := json.Marshal(imgs)
	if err != nil {
		t.Fatalf("marshal image_info: %v", err)
	}
	return string(b)
}

func TestCloneChunkImageInfo_Empty(t *testing.T) {
	svc := &countingFileService{}
	out, copied, err := cloneChunkImageInfo(context.Background(), svc, "", 1, "kb-1", map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" || copied != nil {
		t.Fatalf("expected empty result, got out=%q copied=%v", out, copied)
	}
	if svc.copyCalls != 0 {
		t.Fatalf("expected 0 copies, got %d", svc.copyCalls)
	}
}

func TestCloneChunkImageInfo_RewritesURLAndMatchedOriginal(t *testing.T) {
	svc := &countingFileService{}
	src := mustImageInfoJSON(t, []types.ImageInfo{
		{URL: "local://1/k0/a.png", OriginalURL: "local://1/k0/a.png", Caption: "cap"},
	})
	out, copied, err := cloneChunkImageInfo(context.Background(), svc, src, 7, "k-dst", map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc.copyCalls != 1 || len(copied) != 1 {
		t.Fatalf("expected exactly 1 copy, got calls=%d copied=%v", svc.copyCalls, copied)
	}
	var got []types.ImageInfo
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal out: %v", err)
	}
	want := "local://7/k-dst/copy-of-local://1/k0/a.png"
	if got[0].URL != want {
		t.Errorf("URL not rewritten: got %q want %q", got[0].URL, want)
	}
	// OriginalURL equalled URL -> must also be rewritten to the new object.
	if got[0].OriginalURL != want {
		t.Errorf("matched OriginalURL not rewritten: got %q want %q", got[0].OriginalURL, want)
	}
	if got[0].Caption != "cap" {
		t.Errorf("Caption mutated: got %q", got[0].Caption)
	}
}

// TestRewriteContentImageURLs_ParentTextChunk covers the core scenario: an
// image lives in an independent child chunk (so its image_info yields the
// old->new URL mapping in urlCache), while the PARENT text chunk carries the
// ![](url) reference with an empty image_info. The parent's content must still
// be rewritten from the shared urlCache.
func TestRewriteContentImageURLs_ParentTextChunk(t *testing.T) {
	svc := &countingFileService{}
	// Child image chunk populates urlCache via its image_info.
	childImageInfo := mustImageInfoJSON(t, []types.ImageInfo{
		{URL: "local://1/k0/a.png", OriginalURL: "local://1/k0/a.png"},
	})
	urlCache := map[string]string{}
	if _, _, err := cloneChunkImageInfo(context.Background(), svc, childImageInfo, 7, "k-dst", urlCache); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Parent text chunk has NO image_info but embeds the markdown reference.
	parentContent := "See ![diagram](local://1/k0/a.png) here."
	got := rewriteContentImageURLs(parentContent, urlCache)
	want := "See ![diagram](local://7/k-dst/copy-of-local://1/k0/a.png) here."
	if got != want {
		t.Errorf("parent content image URL not rewritten:\n got %q\nwant %q", got, want)
	}
}

// TestRewriteContentImageURLs_NoMappingIsNoop ensures content without any known
// old URL is returned unchanged, and an empty cache is a no-op.
func TestRewriteContentImageURLs_NoMappingIsNoop(t *testing.T) {
	content := "See ![diagram](local://1/k0/a.png) here."
	if got := rewriteContentImageURLs(content, map[string]string{}); got != content {
		t.Errorf("empty cache must be no-op, got %q", got)
	}
	cache := map[string]string{"local://1/k0/other.png": "local://7/k-dst/copy.png"}
	if got := rewriteContentImageURLs(content, cache); got != content {
		t.Errorf("unrelated mapping must be no-op, got %q", got)
	}
}

func TestCloneChunkImageInfo_PreservesUnmatchedOriginalURL(t *testing.T) {
	svc := &countingFileService{}
	src := mustImageInfoJSON(t, []types.ImageInfo{
		{URL: "local://1/k0/a.png", OriginalURL: "https://external.example.com/a.png"},
	})
	out, _, err := cloneChunkImageInfo(context.Background(), svc, src, 1, "k-dst", map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got []types.ImageInfo
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal out: %v", err)
	}
	if got[0].OriginalURL != "https://external.example.com/a.png" {
		t.Errorf("external OriginalURL must be preserved, got %q", got[0].OriginalURL)
	}
}

func TestCloneChunkImageInfo_DedupsIdenticalURLs(t *testing.T) {
	svc := &countingFileService{}
	src := mustImageInfoJSON(t, []types.ImageInfo{
		{URL: "local://1/k0/same.png"},
		{URL: "local://1/k0/same.png"},
		{URL: "local://1/k0/other.png"},
	})
	_, copied, err := cloneChunkImageInfo(context.Background(), svc, src, 1, "k-dst", map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc.copyCalls != 2 {
		t.Fatalf("expected 2 unique copies (dedup), got %d", svc.copyCalls)
	}
	if len(copied) != 2 {
		t.Fatalf("expected 2 copied URLs, got %v", copied)
	}
}

func TestCloneChunkImageInfo_DedupsAcrossCallsViaSharedCache(t *testing.T) {
	svc := &countingFileService{}
	cache := map[string]string{}
	src := mustImageInfoJSON(t, []types.ImageInfo{{URL: "local://1/k0/shared.png"}})
	if _, _, err := cloneChunkImageInfo(context.Background(), svc, src, 1, "k-dst", cache); err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if _, copied, err := cloneChunkImageInfo(context.Background(), svc, src, 1, "k-dst", cache); err != nil {
		t.Fatalf("second call error: %v", err)
	} else if len(copied) != 0 {
		t.Fatalf("second call should reuse cache (0 new copies), got %v", copied)
	}
	if svc.copyCalls != 1 {
		t.Fatalf("expected 1 copy total across calls, got %d", svc.copyCalls)
	}
}

func TestCloneChunkImageInfo_ParseFailureAbortsClone(t *testing.T) {
	svc := &countingFileService{}
	_, _, err := cloneChunkImageInfo(context.Background(), svc, "{not valid json", 1, "k-dst", map[string]string{})
	if err == nil {
		t.Fatal("expected error on invalid image_info JSON, got nil")
	}
	if svc.copyCalls != 0 {
		t.Fatalf("expected no copies on parse failure, got %d", svc.copyCalls)
	}
}

func TestCloneChunkImageInfo_CopyFailureReturnsPartialForCleanup(t *testing.T) {
	svc := &countingFileService{failOnURL: "local://1/k0/bad.png"}
	src := mustImageInfoJSON(t, []types.ImageInfo{
		{URL: "local://1/k0/good.png"},
		{URL: "local://1/k0/bad.png"},
	})
	_, copied, err := cloneChunkImageInfo(context.Background(), svc, src, 1, "k-dst", map[string]string{})
	if err == nil {
		t.Fatal("expected error when an image copy fails")
	}
	// The already-copied "good.png" must be returned so the caller can clean it up.
	if len(copied) != 1 {
		t.Fatalf("expected 1 already-copied URL for rollback, got %v", copied)
	}
}

func TestCloneChunkImageInfo_SkipsEmptyURL(t *testing.T) {
	svc := &countingFileService{}
	src := mustImageInfoJSON(t, []types.ImageInfo{{URL: "", Caption: "no-image"}})
	out, copied, err := cloneChunkImageInfo(context.Background(), svc, src, 1, "k-dst", map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc.copyCalls != 0 || len(copied) != 0 {
		t.Fatalf("empty URL must be skipped, calls=%d copied=%v", svc.copyCalls, copied)
	}
	var got []types.ImageInfo
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal out: %v", err)
	}
	if got[0].URL != "" {
		t.Errorf("empty URL should stay empty, got %q", got[0].URL)
	}
}
