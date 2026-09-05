package file

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	ks3aws "github.com/ks3sdklib/aws-sdk-go/aws"
	"github.com/ks3sdklib/aws-sdk-go/aws/credentials"
	ks3s3 "github.com/ks3sdklib/aws-sdk-go/service/s3"
	"github.com/stretchr/testify/require"
)

func TestKS3OperationsHonorContextCancellation(t *testing.T) {
	requestStarted := make(chan struct{}, 3)
	releaseHandlers := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		requestStarted <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-releaseHandlers:
		}
	}))
	defer func() {
		close(releaseHandlers)
		server.Close()
	}()

	client := ks3s3.New(&ks3aws.Config{
		Credentials:      credentials.NewStaticCredentials("test-access-key", "test-secret-key", ""),
		Endpoint:         server.URL,
		Region:           "test-region",
		DisableSSL:       true,
		S3ForcePathStyle: true,
		SignerVersion:    "V2",
		MaxRetries:       0,
		HTTPClient:       server.Client(),
	})
	svc := &ks3FileService{client: client, bucketName: "test-bucket"}

	tests := map[string]func(context.Context) error{
		"save": func(ctx context.Context) error {
			_, err := svc.SaveBytes(ctx, []byte("preview"), 7, "preview.docx", false)
			return err
		},
		"get": func(ctx context.Context) error {
			_, err := svc.GetFile(ctx, "ks3://test-bucket/7/exports/missing.docx")
			return err
		},
		"delete": func(ctx context.Context) error {
			return svc.DeleteFile(ctx, "ks3://test-bucket/7/exports/missing.docx")
		},
	}

	for name, operation := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			if name == "save" {
				var intentMu sync.Mutex
				var intentPath string
				ctx = interfaces.WithFileWriteIntent(ctx, func(_ context.Context, path string) error {
					intentMu.Lock()
					intentPath = path
					intentMu.Unlock()
					return nil
				})
				defer func() {
					intentMu.Lock()
					defer intentMu.Unlock()
					require.True(t, strings.HasPrefix(intentPath, "ks3://test-bucket/7/exports/"))
					require.True(t, strings.HasSuffix(intentPath, ".docx"))
				}()
			}

			done := make(chan error, 1)
			go func() { done <- operation(ctx) }()

			select {
			case <-requestStarted:
			case <-time.After(2 * time.Second):
				cancel()
				t.Fatal("KS3 request did not reach test server")
			}
			cancel()

			select {
			case err := <-done:
				require.Error(t, err)
				canceled := errors.Is(err, context.Canceled) ||
					strings.Contains(err.Error(), context.Canceled.Error())
				require.True(t, canceled)
			case <-time.After(2 * time.Second):
				t.Fatal("KS3 operation did not stop after context cancellation")
			}
		})
	}
}
