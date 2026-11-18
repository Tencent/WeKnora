package docparser

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

func TestPaddleOCRVLHeadersFromOverrides(t *testing.T) {
	t.Run("decodes and canonicalizes", func(t *testing.T) {
		headers := paddleOCRVLHeadersFromOverrides(map[string]string{
			paddleOCRVLHeadersKey: `{"x-gateway-token":" abc ","Authorization":"Bearer t"}`,
		})
		if headers["X-Gateway-Token"] != "abc" || headers["Authorization"] != "Bearer t" {
			t.Fatalf("unexpected headers: %v", headers)
		}
	})

	t.Run("drops reserved headers", func(t *testing.T) {
		headers := paddleOCRVLHeadersFromOverrides(map[string]string{
			paddleOCRVLHeadersKey: `{"Content-Type":"text/plain","X-Keep":"1"}`,
		})
		if _, ok := headers["Content-Type"]; ok {
			t.Fatalf("Content-Type must not be overridable: %v", headers)
		}
		if headers["X-Keep"] != "1" {
			t.Fatalf("kept header lost: %v", headers)
		}
	})

	t.Run("invalid payload is ignored", func(t *testing.T) {
		if headers := paddleOCRVLHeadersFromOverrides(map[string]string{
			paddleOCRVLHeadersKey: "not-json",
		}); headers != nil {
			t.Fatalf("expected nil headers, got %v", headers)
		}
	})

	t.Run("absent key", func(t *testing.T) {
		headers := paddleOCRVLHeadersFromOverrides(map[string]string{
			"paddleocr_vl_endpoint": "http://x",
		})
		if headers != nil {
			t.Fatalf("expected nil headers, got %v", headers)
		}
	})
}

func TestPaddleOCRVLReadSendsConfiguredHeaders(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1,localhost")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")

	const token = "gateway-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Gateway-Token") != token {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error": "missing gateway token"}`)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"errorCode":0,"result":{"layoutParsingResults":[
 {"markdown":{"text":"parsed through gateway","images":{}}}]}}`)
	}))
	defer server.Close()

	overrides := map[string]string{
		"paddleocr_vl_endpoint": server.URL,
		paddleOCRVLHeadersKey:   `{"X-Gateway-Token":"` + token + `"}`,
	}
	result, err := NewPaddleOCRVLReader(overrides).Read(context.Background(), &types.ReadRequest{
		FileName:    "doc.pdf",
		FileType:    "pdf",
		FileContent: []byte("%PDF-1.4"),
	})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected parse error: %s", result.Error)
	}
	if result.MarkdownContent != "parsed through gateway" {
		t.Fatalf("unexpected markdown: %q", result.MarkdownContent)
	}

	// Without the gateway header the service rejects the request, which proves
	// the header above is what made the call succeed.
	_, err = NewPaddleOCRVLReader(map[string]string{
		"paddleocr_vl_endpoint": server.URL,
	}).Read(context.Background(), &types.ReadRequest{
		FileName:    "doc.pdf",
		FileType:    "pdf",
		FileContent: []byte("%PDF-1.4"),
	})
	if err == nil {
		t.Fatal("expected an error without the gateway header")
	}
}

func TestPingPaddleOCRVLUsesHeaders(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1,localhost")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Gateway-Token") != "ok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer server.Close()

	if ok, reason := PingPaddleOCRVL(server.URL, map[string]string{"X-Gateway-Token": "ok"}); !ok {
		t.Fatalf("ping with header failed: %s", reason)
	}
	if ok, _ := PingPaddleOCRVL(server.URL, nil); ok {
		t.Fatal("ping without header should be reported unavailable")
	}
}
