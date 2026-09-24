package docparser

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

func TestSelfHostedParserReadTimeout(t *testing.T) {
	// Earlier parser tests may have initialized the runtime whitelist.
	utils.SetSSRFWhitelistFromRaw("127.0.0.1,localhost")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	for _, engine := range []string{PaddleOCRVLEngineName, MinerUEngineName} {
		t.Run(engine, func(t *testing.T) {
			for _, mode := range []string{"success", "http timeout", "parent deadline", "parent cancellation"} {
				t.Run(mode, func(t *testing.T) {
					t.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
					timeout := 10 * time.Second
					if mode == "http timeout" {
						timeout = 100 * time.Millisecond
					}
					arrived := make(chan struct{})
					release := make(chan struct{})
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						_, _ = io.Copy(io.Discard, r.Body)
						close(arrived)
						if mode == "success" {
							w.Header().Set("Content-Type", "application/json")
							if engine == MinerUEngineName {
								_, _ = io.WriteString(w, `{"results":{"test":{
 "md_content":"parsed document","images":{}}}}`)
							} else {
								_, _ = io.WriteString(w, `{"errorCode":0,"result":{"layoutParsingResults":[
 {"markdown":{"text":"parsed document","images":{}}}]}}`)
							}
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
					reader, err := NewReader(ctx, engine, "pdf", false, ReaderDeps{
						Config: &config.Config{KnowledgeBase: &config.KnowledgeBaseConfig{
							PaddleOCRVLTimeout: timeout, MinerUTimeout: timeout,
						}},
						Overrides: map[string]string{
							"paddleocr_vl_endpoint": server.URL, "mineru_endpoint": server.URL,
						},
					})
					if err != nil {
						t.Fatal(err)
					}
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
		})
	}
}

func TestSelfHostedParserReaderConfig(t *testing.T) {
	// Environment changes after startup must not affect reader construction.
	t.Setenv("WEKNORA_PADDLEOCR_VL_TIMEOUT", "1ms")
	t.Setenv("WEKNORA_MINERU_TIMEOUT", "1ms")
	for _, tc := range []struct {
		name          string
		cfg           *config.Config
		paddle, miner time.Duration
	}{
		{"nil config", nil, 1000 * time.Second, 1000 * time.Second},
		{"nil knowledge base", &config.Config{}, 1000 * time.Second, 1000 * time.Second},
		{"nonpositive", &config.Config{KnowledgeBase: &config.KnowledgeBaseConfig{
			PaddleOCRVLTimeout: -time.Second,
		}}, 1000 * time.Second, 1000 * time.Second},
		{"independent values", &config.Config{KnowledgeBase: &config.KnowledgeBaseConfig{
			PaddleOCRVLTimeout: 90 * time.Minute, MinerUTimeout: 40 * time.Minute,
		}}, 90 * time.Minute, 40 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, engine := range []string{PaddleOCRVLEngineName, MinerUEngineName} {
				reader, err := NewReader(context.Background(), engine, "pdf", false, ReaderDeps{Config: tc.cfg})
				if err != nil {
					t.Fatal(err)
				}
				var got, want time.Duration
				switch r := reader.(type) {
				case *PaddleOCRVLReader:
					got, want = r.timeout, tc.paddle
				case *MinerUReader:
					got, want = r.timeout, tc.miner
				default:
					t.Fatalf("unexpected reader %T", reader)
				}
				if got != want {
					t.Fatalf("%s timeout = %s, want %s", engine, got, want)
				}
			}
		})
	}
}
