package session

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

type timeoutUnusedDocumentReader struct{ interfaces.DocumentReader }

func TestChatAttachmentUsesConfiguredParserTimeout(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	for _, engine := range []string{docparser.PaddleOCRVLEngineName, docparser.MinerUEngineName} {
		t.Run(engine, func(t *testing.T) {
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			cfg := &config.Config{KnowledgeBase: &config.KnowledgeBaseConfig{
				PaddleOCRVLTimeout: 100 * time.Millisecond, MinerUTimeout: 100 * time.Millisecond,
			}}
			processor := NewAttachmentProcessor(cfg, nil, &timeoutUnusedDocumentReader{}, nil, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			ctx = context.WithValue(ctx, types.ChatParserEngineContextKey, engine)
			ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{
				ParserEngineConfig: &types.ParserEngineConfig{
					PaddleOCRVLEndpoint: server.URL, MinerUEndpoint: server.URL,
				},
			})
			err := processor.processWithDocumentReader(
				ctx, []byte("test"), "test.pdf", ".pdf", &types.MessageAttachment{}, 1,
			)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v, want parser timeout", err)
			}
			if ctx.Err() != nil {
				t.Fatalf("only parent deadline stopped the request: %v", ctx.Err())
			}
		})
	}
}
