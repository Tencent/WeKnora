package docparser

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/infrastructure/checkpoint"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

func testPDF(pages int) []byte {
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offset := []int{0}
	obj := func(n int, s string) {
		offset = append(offset, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", n, s)
	}
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	var kids strings.Builder
	for i := 0; i < pages; i++ {
		fmt.Fprintf(&kids, "%d 0 R ", i+3)
	}
	obj(2, fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", pages, kids.String()))
	for i := 0; i < pages; i++ {
		obj(i+3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 400] /Resources << >> >>")
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offset))
	for _, o := range offset[1:] {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offset), xref)
	return []byte(b.String())
}

func TestSplitCloudPDFPageCoverage(t *testing.T) {
	if _, e := exec.LookPath("qpdf"); e != nil {
		t.Skip("qpdf required")
	}
	parts, e := splitCloudPDF(context.Background(), testPDF(205))
	if e != nil {
		t.Fatal(e)
	}
	again, err := splitCloudPDF(context.Background(), testPDF(205))
	if err != nil {
		t.Fatal(err)
	}
	for i := range parts {
		if !bytes.Equal(parts[i].content, again[i].content) {
			t.Fatal("splitting changed PDF bytes and would invalidate resume checkpoints")
		}
	}
	if len(parts) != 3 || parts[0].first != 1 || parts[0].last != 100 ||
		parts[1].first != 101 || parts[1].last != 200 || parts[2].first != 201 || parts[2].last != 205 {
		t.Fatalf("incorrect page coverage: %v", parts)
	}
}

func TestCloudPartResumeDoesNotUpload(t *testing.T) {
	t.Setenv("WEKNORA_PROCESSING_CHECKPOINT_DIR", t.TempDir())
	req := &types.ReadRequest{FileName: "part.pdf", FileContent: []byte("input")}
	result := &types.ReadResult{MarkdownContent: "completed"}
	if e := checkpoint.Save("mineru-results", ReadCheckpointKey(req), result); e != nil {
		t.Fatal(e)
	}
	c := &MinerUCloudReader{}
	got, e := c.readCloudPart(context.Background(), req)
	if e != nil || got.MarkdownContent != "completed" {
		t.Fatalf("resume %v %v", got, e)
	}
}

func TestCloudPartResumesExistingJob(t *testing.T) {
	t.Setenv("WEKNORA_PROCESSING_CHECKPOINT_DIR", t.TempDir())
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = fmt.Fprint(w, `{"code":0,"data":{"extract_result":[{"state":"done","markdown":"resumed"}]}}`)
	}))
	defer srv.Close()
	req := &types.ReadRequest{FileName: "part.pdf", FileContent: []byte("input")}
	job := cloudJobCheckpoint{BatchID: "existing"}
	if err := checkpoint.Save("mineru-jobs", ReadCheckpointKey(req), job); err != nil {
		t.Fatal(err)
	}
	c := &MinerUCloudReader{baseURL: srv.URL, timeout: time.Second, pollInterval: time.Millisecond}
	got, e := c.readCloudPart(context.Background(), req)
	if e != nil || got.MarkdownContent != "resumed" || calls != 1 {
		t.Fatalf("resume %v %v calls=%d", got, e, calls)
	}
}

func TestSplitCloudPDFMergesPartsWithoutImageCollision(t *testing.T) {
	if _, err := exec.LookPath("qpdf"); err != nil {
		t.Skip("qpdf required")
	}
	t.Setenv("WEKNORA_PROCESSING_CHECKPOINT_DIR", t.TempDir())
	req := &types.ReadRequest{FileName: "book.pdf", FileType: "pdf", FileContent: testPDF(105)}
	parts, err := splitCloudPDF(context.Background(), req.FileContent)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range parts {
		sub := *req
		sub.FileContent = part.content
		sub.FileName = fmt.Sprintf("book-pages-%d-%d.pdf", part.first, part.last)
		result := &types.ReadResult{
			MarkdownContent: fmt.Sprintf("pages %d-%d ![image](images/0.jpg)", part.first, part.last),
			ImageRefs: []types.ImageRef{
				{Filename: "0.jpg", OriginalRef: "images/0.jpg", ImageData: []byte("image")},
			},
		}
		result.MarkdownContent += ` <img src="images/0.jpg">`
		result.SourceBlocks = []types.SourceBlock{{
			Start: 0, End: len([]rune(result.MarkdownContent)),
			Locator: types.SourceLocator{Type: types.SourceLocatorPDF, Page: 1, SourceID: "block-1"},
		}}
		if err = checkpoint.Save("mineru-results", ReadCheckpointKey(&sub), result); err != nil {
			t.Fatal(err)
		}
	}
	c := &MinerUCloudReader{}
	result, err := c.readSplitPDF(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Metadata["pages"] != "105" || len(result.ImageRefs) != 2 {
		t.Fatalf("missing merged pages/images: %v", result.Metadata)
	}
	md := result.MarkdownContent
	if len(result.SourceBlocks) != 2 || result.SourceBlocks[1].Locator.Page != 101 {
		t.Fatalf("original page locators lost: %v", result.SourceBlocks)
	}
	for i, block := range result.SourceBlocks {
		text := string([]rune(md)[block.Start:block.End])
		if !strings.Contains(text, fmt.Sprintf("part-%d/images/0.jpg", i+1)) ||
			!strings.HasPrefix(block.Locator.SourceID, fmt.Sprintf("part-%d/", i+1)) {
			t.Fatalf("locator range or source identity lost: %v %s", block, text)
		}
	}
	if !strings.Contains(md, `src="part-2/images/0.jpg"`) {
		t.Fatal("HTML image references collided")
	}
	if !strings.Contains(md, "](part-1/images/0.jpg)") || !strings.Contains(md, "](part-2/images/0.jpg)") {
		t.Fatal("images from different parts collided")
	}
	if strings.Index(md, "pages 1-100") > strings.Index(md, "pages 101-105") {
		t.Fatal("parts merged out of order")
	}
}

func TestSmallCloudPDFWithoutQPDFPreservesExistingParsing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("WEKNORA_PROCESSING_CHECKPOINT_DIR", t.TempDir())
	req := &types.ReadRequest{FileName: "small.pdf", FileContent: testPDF(2)}
	saved := &types.ReadResult{MarkdownContent: "small PDF"}
	if err := checkpoint.Save("mineru-results", ReadCheckpointKey(req), saved); err != nil {
		t.Fatal(err)
	}
	c := &MinerUCloudReader{}
	result, err := c.readSplitPDF(context.Background(), req)
	if err != nil || result.MarkdownContent != "small PDF" {
		t.Fatalf("small PDF required a new tool: %v", err)
	}
}
