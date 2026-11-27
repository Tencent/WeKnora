package docparser

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

func TestPaddleOCRVLReadersPreservePageBounds(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1,localhost")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	for _, tt := range []struct {
		name, fileType string
		pages          []paddleOCRVLPage
		check          func(*testing.T, *types.ReadResult)
	}{
		{
			name: "page bounds", fileType: "pdf",
			pages: []paddleOCRVLPage{
				paddleOCRVLTestPage(t, "# Repeated heading\n\nfirst body\n\n"+
					"<table><tr><th>项目</th><th>值</th></tr><tr><td>甲</td><td>42</td></tr></table>",
					"first body", "unmatched layout"),
				paddleOCRVLTestPage(t, "\n "),
				paddleOCRVLTestPage(t, "# Repeated heading\n\nthird body", "Repeated heading", "third body"),
			},
			check: func(t *testing.T, result *types.ReadResult) {
				require.Len(t, result.SourceBlocks, 5)
				paddleOCRVLTestLocator(t, result.MarkdownContent, result.SourceBlocks, "first body", 1, true)
				paddleOCRVLTestLocator(t, result.MarkdownContent, result.SourceBlocks, "42", 1, false)
				paddleOCRVLTestLocator(t, result.MarkdownContent, result.SourceBlocks, "third body", 3, true)
				require.Equal(t, 3, result.SourceBlocks[3].Locator.Page)
				require.Equal(t, []float64{0.1, 0.1, 0.9, 0.2}, result.SourceBlocks[3].Locator.BBox)
			},
		},
		{
			name: "numbered headings stay page local", fileType: "pdf",
			pages: []paddleOCRVLPage{
				paddleOCRVLTestPage(t, "1. Summary\n\n<table><tr><td>other cell</td></tr></table>",
					"Summary", "other cell"),
				paddleOCRVLTestPage(t, "# Summary", "Summary"),
			},
			check: func(t *testing.T, result *types.ReadResult) {
				paddleOCRVLTestLocator(t, result.MarkdownContent, result.SourceBlocks, "1. Summary", 1, true)
				paddleOCRVLTestLocator(t, result.MarkdownContent, result.SourceBlocks, "other cell", 1, true)
				paddleOCRVLTestLocator(t, result.MarkdownContent, result.SourceBlocks, "# Summary", 2, true)
			},
		},
		{
			name: "image table aligns after normalization", fileType: "pdf",
			pages: []paddleOCRVLPage{
				paddleOCRVLTestPage(t, `<table><tr><td><img src="x.png" alt="profile"/></td></tr></table>`,
					"profile"),
			},
			check: func(t *testing.T, result *types.ReadResult) {
				require.Contains(t, result.MarkdownContent, "![profile](x.png)")
				paddleOCRVLTestLocator(t, result.MarkdownContent, result.SourceBlocks, "profile", 1, true)
				for _, block := range result.SourceBlocks {
					if len(block.Locator.BBox) > 0 {
						require.Equal(t, []float64{0.1, 0.1, 0.9, 0.2}, block.Locator.BBox)
					}
				}
			},
		},
		{
			name: "original image prefix keeps offsets", fileType: "png",
			pages: []paddleOCRVLPage{paddleOCRVLTestPage(t, "图像正文", "图像正文")},
			check: func(t *testing.T, result *types.ReadResult) {
				require.Len(t, result.ImageRefs, 1)
				paddleOCRVLTestLocator(t, result.MarkdownContent, result.SourceBlocks, "图像正文", 1, true)
			},
		},
	} {
		for _, mode := range []string{"local", "cloud"} {
			t.Run(tt.name+"/"+mode, func(t *testing.T) {
				var server *httptest.Server
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/layout-parsing":
						var response paddleOCRVLResponse
						response.Result.LayoutParsingResults = tt.pages
						_ = json.NewEncoder(w).Encode(response)
					case "/jobs":
						_, _ = io.WriteString(w, `{"data":{"jobId":"test"}}`)
					case "/jobs/test":
						_ = json.NewEncoder(w).Encode(map[string]interface{}{
							"data": map[string]interface{}{
								"state": "done", "resultUrl": map[string]string{"jsonUrl": server.URL + "/results"},
							},
						})
					case "/results":
						for _, page := range tt.pages {
							var line paddleOCRVLCloudResultLine
							line.Result.LayoutParsingResults = []paddleOCRVLPage{page}
							_ = json.NewEncoder(w).Encode(line)
						}
					default:
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				req := &types.ReadRequest{
					FileName: "test." + tt.fileType, FileType: tt.fileType, FileContent: []byte("test"),
				}
				var result *types.ReadResult
				var err error
				if mode == "local" {
					reader := NewPaddleOCRVLReader(map[string]string{"paddleocr_vl_endpoint": server.URL})
					result, err = reader.Read(ctx, req)
				} else {
					reader := NewPaddleOCRVLCloudReader(map[string]string{
						"paddleocr_vl_cloud_base_url": server.URL + "/jobs", "paddleocr_vl_cloud_token": "test-token",
					})
					result, err = reader.Read(ctx, req)
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Empty(t, result.Error)
				require.NotContains(t, result.MarkdownContent, "<table>")
				tt.check(t, result)
			})
		}
	}
}

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
