package docparser

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
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
			t.Fatal("splitting changed PDF content")
		}
	}
	if len(parts) != 3 || parts[0].first != 1 || parts[0].last != 100 ||
		parts[1].first != 101 || parts[1].last != 200 || parts[2].first != 201 || parts[2].last != 205 {
		t.Fatalf("incorrect page coverage: %v", parts)
	}
}

func TestSplitCloudPDFMergesPartsWithoutImageCollision(t *testing.T) {
	if _, err := exec.LookPath("qpdf"); err != nil {
		t.Skip("qpdf required")
	}
	allowLoopbackSSRF(t)
	req := &types.ReadRequest{FileName: "book.pdf", FileType: "pdf", FileContent: testPDF(105)}
	c, uploads := mockSplitCloud(t)
	result, err := c.readSplitPDF(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if uploads.Load() != 2 || result.Metadata["pages"] != "105" || len(result.ImageRefs) != 2 {
		t.Fatalf("missing merged pages/images: %v", result.Metadata)
	}
	md := result.MarkdownContent
	if len(result.SourceBlocks) != 2 || result.SourceBlocks[1].Locator.Page != 101 {
		t.Fatalf("original page locators lost: %v", result.SourceBlocks)
	}
	for i, block := range result.SourceBlocks {
		text := string([]rune(md)[block.Start:block.End])
		if !strings.HasPrefix(text, "pages") ||
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
	allowLoopbackSSRF(t)
	req := &types.ReadRequest{FileName: "small.pdf", FileType: "pdf", FileContent: testPDF(2)}
	c, uploads := mockSplitCloud(t)
	result, err := c.readSplitPDF(context.Background(), req)
	if err != nil || uploads.Load() != 1 || !strings.Contains(result.MarkdownContent, "pages 1-100") {
		t.Fatalf("small PDF required a new tool: %v", err)
	}
}

// Exercise real Cloud requests and ZIP merging without a persistence fixture.
func mockSplitCloud(t *testing.T) (*MinerUCloudReader, *atomic.Int32) {
	t.Helper()
	var uploads atomic.Int32
	var batches atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			id := batches.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"batch_id": fmt.Sprint(id), "file_urls": []string{server.URL + "/upload"}}})
		case r.Method == http.MethodPut:
			data, err := io.ReadAll(r.Body)
			if err != nil || !bytes.HasPrefix(data, []byte("%PDF")) {
				t.Error("missing PDF upload")
			}
			uploads.Add(1)
		case strings.HasPrefix(r.URL.Path, "/extract-results/batch/"):
			id := strings.TrimPrefix(r.URL.Path, "/extract-results/batch/")
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"extract_result": []map[string]string{{"state": "done", "full_zip_url": server.URL + "/zip/" + id}}}})
		case strings.HasPrefix(r.URL.Path, "/zip/"):
			first, last := 1, 100
			if strings.HasSuffix(r.URL.Path, "/2") {
				first, last = 101, 105
			}
			text := fmt.Sprintf("pages %d-%d 庄子齐物论研究", first, last)
			md := "![image](images/0.jpg) <img src=\"images/0.jpg\">\n\n" + text
			list, _ := json.Marshal([]map[string]any{{"type": "text", "text": text, "page_idx": 0, "bbox": []int{0, 0, 100, 100}}})
			z := zip.NewWriter(w)
			for name, data := range map[string][]byte{"full.md": []byte(md), "images/0.jpg": []byte("image"), "book_content_list.json": list} {
				f, err := z.Create(name)
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = f.Write(data)
			}
			if err := z.Close(); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return &MinerUCloudReader{baseURL: server.URL, timeout: time.Second, pollInterval: time.Millisecond}, &uploads
}
