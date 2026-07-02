package docparser

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestPaddleOCRVLReaderAddsBearerToken(t *testing.T) {
	const token = "test-token"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("Authorization header = %q, want Bearer token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errorCode":0,"result":{"layoutParsingResults":[{"markdown":{"text":"ok","images":{}}}]}}`))
	}))
	defer server.Close()

	reader := NewPaddleOCRVLReader(map[string]string{
		"paddleocr_vl_endpoint":     server.URL,
		"paddleocr_vl_bearer_token": token,
	})

	result, err := reader.Read(context.Background(), &types.ReadRequest{
		FileName:    "doc.pdf",
		FileType:    "pdf",
		FileContent: []byte("pdf"),
	})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if result.MarkdownContent != "ok" {
		t.Fatalf("MarkdownContent = %q, want ok", result.MarkdownContent)
	}
}

func TestSetPaddleOCRVLAuthHeaderAddsBearerToken(t *testing.T) {
	const token = "test-token"

	req := httptest.NewRequest(http.MethodGet, "/layout-parsing", nil)
	setPaddleOCRVLAuthHeader(req, token)
	if got := req.Header.Get("Authorization"); got != "Bearer "+token {
		t.Fatalf("Authorization header = %q, want Bearer token", got)
	}
}

func TestPaddleOCRVLReaderKeepsImageRefs(t *testing.T) {
	png := createTestPNG(10, 10)
	imgB64 := base64.StdEncoding.EncodeToString(png)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errorCode":0,"result":{"layoutParsingResults":[{"markdown":{"text":"![](images/plot.png)","images":{"plot.png":"` + imgB64 + `"}}}]}}`))
	}))
	defer server.Close()

	reader := NewPaddleOCRVLReader(map[string]string{"paddleocr_vl_endpoint": server.URL})
	result, err := reader.Read(context.Background(), &types.ReadRequest{
		FileName:    "doc.pdf",
		FileType:    "pdf",
		FileContent: []byte("pdf"),
	})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if len(result.ImageRefs) != 1 {
		t.Fatalf("ImageRefs len = %d, want 1", len(result.ImageRefs))
	}
	if result.ImageRefs[0].OriginalRef != "images/plot.png" {
		t.Fatalf("OriginalRef = %q, want images/plot.png", result.ImageRefs[0].OriginalRef)
	}
}
