package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type imageReaderKBRepo struct {
	interfaces.KnowledgeBaseRepository
	kb *types.KnowledgeBase
}

func (r *imageReaderKBRepo) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return r.kb, nil
}

type imageReaderFiles struct {
	interfaces.FileService
	body  []byte
	reads []string
}

func (f *imageReaderFiles) GetFile(_ context.Context, path string) (io.ReadCloser, error) {
	f.reads = append(f.reads, path)
	return io.NopCloser(bytes.NewReader(f.body)), nil
}

func imageResult(urls ...string) *types.SearchResult {
	infos := make([]types.ImageInfo, len(urls))
	for i, u := range urls {
		infos[i] = types.ImageInfo{URL: u}
	}
	raw, _ := json.Marshal(infos)
	return &types.SearchResult{ID: "c1", KnowledgeBaseID: "kb-1", ImageInfo: string(raw)}
}

func TestReadChunkImageReadsTheFirstImageFromItsKnowledgeBaseStorage(t *testing.T) {
	files := &imageReaderFiles{body: []byte("png bytes")}
	s := &knowledgeBaseService{
		repo:       &imageReaderKBRepo{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 7}},
		tenantRepo: &attrsTenantRepo{}, // no tenant row: the default FileService serves
		fileSvc:    files,
	}
	data, err := s.ReadChunkImage(context.Background(), imageResult("local://a.png", "local://b.png"))
	require.NoError(t, err)
	assert.Equal(t, []byte("png bytes"), data)
	assert.Equal(t, []string{"local://a.png"}, files.reads)
}

func TestReadChunkImageRefusesWhatItCannotOrShouldNotRead(t *testing.T) {
	files := &imageReaderFiles{body: bytes.Repeat([]byte{1}, maxRerankImageBytes+1)}
	s := &knowledgeBaseService{
		repo:       &imageReaderKBRepo{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 7}},
		tenantRepo: &attrsTenantRepo{},
		fileSvc:    files,
	}
	_, err := s.ReadChunkImage(context.Background(), imageResult("local://huge.png"))
	assert.ErrorContains(t, err, "larger than")

	_, err = s.ReadChunkImage(context.Background(), &types.SearchResult{ID: "c2", ImageInfo: ""})
	assert.ErrorContains(t, err, "carries no image")

	_, err = s.ReadChunkImage(context.Background(), imageResult(""))
	assert.ErrorContains(t, err, "no image URL")
}

func TestRemoteImageDownloadHonorsCallerCancellation(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	kb := &knowledgeBaseService{}
	multimodal := &ImageMultimodalService{}
	tests := []struct {
		name      string
		read      func(context.Context, string) ([]byte, error)
		flushBody bool
	}{
		{
			name: "reranker waiting for headers",
			read: func(ctx context.Context, url string) ([]byte, error) {
				return kb.ReadChunkImage(ctx, imageResult(url))
			},
		},
		{
			name: "ingestion waiting for remaining body",
			read: func(ctx context.Context, url string) ([]byte, error) {
				return multimodal.readImageBytes(ctx, types.ImageMultimodalPayload{ImageURL: url})
			},
			flushBody: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			started := make(chan struct{})
			requestCanceled := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.flushBody {
					_, _ = io.WriteString(w, "partial image")
					w.(http.Flusher).Flush()
				}
				close(started)
				select {
				case <-r.Context().Done():
					close(requestCanceled)
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				data []byte
				err  error
			}
			done := make(chan result, 1)
			go func() {
				data, err := tt.read(ctx, server.URL)
				done <- result{data: data, err: err}
			}()
			select {
			case <-started:
			case got := <-done:
				t.Fatalf("download ended before reaching server: %v", got.err)
			case <-time.After(5 * time.Second):
				t.Fatal("server did not receive the image request")
			}

			cancel()
			select {
			case got := <-done:
				require.ErrorIs(t, got.err, context.Canceled)
				require.Nil(t, got.data)
			case <-time.After(2 * time.Second):
				t.Fatal("image download kept waiting after cancellation")
			}
			select {
			case <-requestCanceled:
			case <-time.After(2 * time.Second):
				t.Fatal("server request remained open after cancellation")
			}
		})
	}
}
