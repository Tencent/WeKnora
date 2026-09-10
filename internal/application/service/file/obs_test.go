package file

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
)

func newTestOBSClient(endpoint string) *s3.Client {
	return s3.New(s3.Options{
		Region:           "test-region",
		EndpointResolver: &obsEndpointResolver{url: endpoint},
		Credentials:      credentials.NewStaticCredentialsProvider("test-access-key", "test-secret-key", ""),
		UsePathStyle:     true,
	})
}

func TestOBSSaveBytesWithProxyReturnsProviderPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := &obsFileService{
		client:      newTestOBSClient(server.URL),
		bucketName:  "documents",
		pathPrefix:  "weknora",
		proxyDomain: "https://vnia.ctyun.cn:8081",
	}

	path, err := svc.SaveBytes(context.Background(), []byte("content"), 10000, "report.pdf", false)
	require.NoError(t, err)
	require.Regexp(t, `^obs://documents/weknora/10000/[0-9a-f-]+\.pdf$`, path)
}

func TestOBSPathParsingSupportsProviderAndLegacyProxyPaths(t *testing.T) {
	svc := &obsFileService{
		bucketName:  "documents",
		proxyDomain: "https://vnia.ctyun.cn:8081",
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "provider path", path: "obs://documents/weknora/10000/report.pdf", want: "weknora/10000/report.pdf"},
		{name: "legacy proxy URL", path: "https://vnia.ctyun.cn:8081/weknora/10000/report.pdf", want: "weknora/10000/report.pdf"},
		{name: "bare object key", path: "weknora/10000/report.pdf", want: "weknora/10000/report.pdf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.parseObsFilePath(tt.path)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}

	_, err := svc.parseObsFilePath("obs://another-bucket/weknora/10000/report.pdf")
	require.ErrorContains(t, err, "invalid OBS file path")
}

func TestOBSGetFileURLUsesProxyForProviderPath(t *testing.T) {
	svc := &obsFileService{
		bucketName:  "documents",
		proxyDomain: "https://vnia.ctyun.cn:8081",
	}

	got, err := svc.GetFileURL(context.Background(), "obs://documents/weknora/10000/report.pdf")
	require.NoError(t, err)
	require.Equal(t, "https://vnia.ctyun.cn:8081/weknora/10000/report.pdf", got)

	legacy := "https://vnia.ctyun.cn:8081/weknora/10000/legacy.pdf"
	got, err = svc.GetFileURL(context.Background(), legacy)
	require.NoError(t, err)
	require.Equal(t, legacy, got)
}

func TestOBSPathOwnershipSupportsProviderAndLegacyProxyPaths(t *testing.T) {
	svc := &obsFileService{
		bucketName:  "documents",
		proxyDomain: "https://vnia.ctyun.cn:8081",
	}

	require.True(t, svc.ownsObsFilePath("obs://documents/weknora/report.pdf"))
	require.True(t, svc.ownsObsFilePath("https://vnia.ctyun.cn:8081/weknora/report.pdf"))
	require.False(t, svc.ownsObsFilePath("s3://documents/weknora/report.pdf"))
	require.False(t, svc.ownsObsFilePath("https://example.com/weknora/report.pdf"))
}

func TestOBSCopyFileReturnsProviderPathWithProxyConfigured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Amz-Copy-Source") == "" {
			http.Error(w, "missing copy source", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<CopyObjectResult><ETag>"test-etag"</ETag><LastModified>2026-08-20T00:00:00Z</LastModified></CopyObjectResult>`))
	}))
	defer server.Close()

	svc := &obsFileService{
		client:      newTestOBSClient(server.URL),
		bucketName:  "documents",
		pathPrefix:  "weknora",
		proxyDomain: "https://vnia.ctyun.cn:8081",
	}

	for _, source := range []string{
		"obs://documents/weknora/10000/source.pdf",
		"https://vnia.ctyun.cn:8081/weknora/10000/source.pdf",
	} {
		copied, err := svc.CopyFile(context.Background(), source, 10000, "knowledge-1")
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(copied, "obs://documents/weknora/10000/knowledge-1/"))
		require.True(t, strings.HasSuffix(copied, ".pdf"))
	}

	_, err := svc.CopyFile(context.Background(), "s3://documents/weknora/source.pdf", 10000, "knowledge-1")
	require.ErrorIs(t, err, ErrCrossBackendCopy)
}
