package docparser

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

func TestPaddleOCRVLTimeoutConfig(t *testing.T) {
	for _, tt := range []struct {
		name, value string
		want        time.Duration
	}{
		{"unset", "", 1000 * time.Second},
		{"empty", "", 1000 * time.Second},
		{"whitespace", "  ", 1000 * time.Second},
		{"seconds", "5400s", 90 * time.Minute},
		{"minutes", "90m", 90 * time.Minute},
		{"trimmed", " 90m ", 90 * time.Minute},
		{"invalid", "invalid", 1000 * time.Second},
		{"unitless", "5400", 1000 * time.Second},
		{"zero", "0s", 1000 * time.Second},
		{"negative", "-1s", 1000 * time.Second},
		{"overflow", "999999999999999999999h", 1000 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WEKNORA_PADDLEOCR_VL_TIMEOUT", tt.value)
			if tt.name == "unset" {
				if err := os.Unsetenv("WEKNORA_PADDLEOCR_VL_TIMEOUT"); err != nil {
					t.Fatal(err)
				}
			}
			reader := NewPaddleOCRVLReader(nil)
			if reader.timeout != tt.want {
				t.Fatalf("timeout = %s, want %s", reader.timeout, tt.want)
			}
		})
	}
}

func TestPaddleOCRVLReadTimeout(t *testing.T) {
	// Earlier parser tests may have initialized the runtime whitelist.
	utils.SetSSRFWhitelistFromRaw("127.0.0.1,localhost")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	for _, mode := range []string{"success", "http timeout", "parent deadline", "parent cancellation"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
			t.Setenv("WEKNORA_PADDLEOCR_VL_TIMEOUT", "10s")
			if mode == "http timeout" {
				t.Setenv("WEKNORA_PADDLEOCR_VL_TIMEOUT", "100ms")
			}
			arrived := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				close(arrived)
				if mode == "success" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"errorCode":0,"result":{"layoutParsingResults":[
 {"markdown":{"text":"parsed document","images":{}}}]}}`)
					return
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if mode == "parent deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 100*time.Millisecond)
				defer stop()
			}
			if mode == "parent cancellation" {
				go func() {
					select {
					case <-arrived:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			reader := NewPaddleOCRVLReader(map[string]string{"paddleocr_vl_endpoint": server.URL})
			result, err := reader.Read(ctx, &types.ReadRequest{
				FileName: "test.pdf", FileType: "pdf", FileContent: []byte("test"),
			})
			if mode == "success" {
				if err != nil {
					t.Fatal(err)
				}
				if result.MarkdownContent != "parsed document" {
					t.Fatalf("unexpected result: %+v", result)
				}
				return
			}
			want := context.DeadlineExceeded
			if mode == "parent cancellation" {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
			if mode == "http timeout" && ctx.Err() != nil {
				t.Fatalf("request only stopped at parent deadline: %v", ctx.Err())
			}
		})
	}
}
