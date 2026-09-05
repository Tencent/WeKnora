package file

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
)

type doFunc func(*http.Request) (*http.Response, error)

func (f doFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestOBSFileService(t *testing.T, acl *string) *obsFileService {
	t.Helper()
	client := s3.New(s3.Options{
		Region: "test-region",
		EndpointResolver: &obsEndpointResolver{
			url: "https://obs.example.com",
		},
		Credentials:  credentials.NewStaticCredentialsProvider("ak", "sk", ""),
		UsePathStyle: true,
		HTTPClient: doFunc(func(req *http.Request) (*http.Response, error) {
			*acl = req.Header.Get("X-Amz-Acl")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		}),
	})
	return &obsFileService{
		client:     client,
		bucketName: "preview-bucket",
		pathPrefix: "weknora",
	}
}

func TestOBSSaveBytesWriteIntentKeepsObjectPrivate(t *testing.T) {
	var acl string
	svc := newTestOBSFileService(t, &acl)
	var intentPath string
	ctx := interfaces.WithFileWriteIntent(context.Background(), func(_ context.Context, path string) error {
		intentPath = path
		return nil
	})

	got, err := svc.SaveBytes(ctx, []byte("preview"), 7, "preview.docx", false)
	require.NoError(t, err)
	require.Equal(t, got, intentPath)
	require.Empty(t, acl)
}

func TestOBSSaveBytesWithoutWriteIntentPreservesPublicACL(t *testing.T) {
	var acl string
	svc := newTestOBSFileService(t, &acl)

	_, err := svc.SaveBytes(context.Background(), []byte("ordinary export"), 7, "export.docx", false)
	require.NoError(t, err)
	require.Equal(t, "public-read", acl)
}
