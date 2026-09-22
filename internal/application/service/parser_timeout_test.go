package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

type parserTimeoutFileService struct{ interfaces.FileService }

func (*parserTimeoutFileService) GetFile(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("test PDF")), nil
}

type parserTimeoutTenantService struct{ interfaces.TenantService }

func TestDocumentPathsUseConfiguredParserTimeout(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	for _, engine := range []string{docparser.PaddleOCRVLEngineName, docparser.MinerUEngineName} {
		for _, path := range []string{"knowledge", "temporary document"} {
			t.Run(engine+"/"+path, func(t *testing.T) {
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
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				var err error
				if path == "knowledge" {
					svc := &knowledgeService{config: cfg, tenantService: &parserTimeoutTenantService{}}
					reader := svc.resolveDocReader(ctx, engine, "pdf", false, map[string]string{
						"paddleocr_vl_endpoint": server.URL, "mineru_endpoint": server.URL,
					})
					if reader == nil {
						t.Fatal("reader not resolved")
					}
					_, err = reader.Read(ctx, &types.ReadRequest{
						FileContent: []byte("test"), FileName: "test.pdf", FileType: "pdf",
					})
				} else {
					svc := NewTemporaryDocumentService(
						cfg, nil, &parserTimeoutFileService{}, nil, nil, nil, nil, nil, nil, nil,
					)
					ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{
						ParserEngineConfig: &types.ParserEngineConfig{
							PaddleOCRVLEndpoint: server.URL, MinerUEndpoint: server.URL,
						},
					})
					_, _, _, err = svc.(*temporaryDocumentService).parse(ctx, &types.TemporaryDocument{
						FileName: "test.pdf", FileType: ".pdf",
						ProcessingOptions: types.JSON(`{"parser_engine":"` + engine + `"}`),
					})
				}
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("error = %v, want parser timeout", err)
				}
				if ctx.Err() != nil {
					t.Fatalf("only parent deadline stopped the request: %v", ctx.Err())
				}
			})
		}
	}
}
