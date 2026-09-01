package worker

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"
	cos "github.com/tencentyun/cos-go-sdk-v5"

	objstore "github.com/Tencent/WeKnora/internal/custom/client/minio"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

func TestTencentMPSInputPreparerStagesLocalSourceOnce(t *testing.T) {
	var uploaded atomic.Bool
	var putCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			if !uploaded.Load() {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		case http.MethodPut:
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.Equal(t, []byte("video-bytes"), body)
			uploaded.Store(true)
			putCount.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	localStore, err := objstore.New(config.MinIOConfig{Backend: "local", Bucket: "vidsage", LocalDir: filepath.Join(t.TempDir(), "storage")})
	require.NoError(t, err)
	_, err = localStore.PutObject(context.Background(), "videos/video-1/source.mp4", bytes.NewReader([]byte("video-bytes")), int64(len("video-bytes")), minio.PutObjectOptions{})
	require.NoError(t, err)
	bucketURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	cosClient := cos.NewClient(&cos.BaseURL{BucketURL: bucketURL}, server.Client())
	cosClient.Conf.EnableCRC = false
	preparer := &TencentMPSInputPreparer{
		MinIO: localStore, Client: cosClient,
		Prefix: "vidsage-mps-input/", SecretID: "id", SecretKey: "key",
	}
	video := &model.Video{ID: "video-1", UploadObjectKey: "videos/video-1/source.mp4", FileURL: "http://localhost/api/custom/files/videos/video-1/source.mp4"}

	first, err := preparer.Prepare(context.Background(), video, video.FileURL)
	require.NoError(t, err)
	second, err := preparer.Prepare(context.Background(), video, video.FileURL)
	require.NoError(t, err)
	require.Contains(t, first, "vidsage-mps-input/video-1/source.mp4")
	require.Contains(t, second, "vidsage-mps-input/video-1/source.mp4")
	require.Equal(t, int32(1), putCount.Load())
}

func TestTencentMPSInputPreparerKeepsPublicHTTPSURL(t *testing.T) {
	preparer := &TencentMPSInputPreparer{}
	video := &model.Video{ID: "video-public"}
	source := "https://cdn.example.com/video.mp4?token=test"
	prepared, err := preparer.Prepare(context.Background(), video, source)
	require.NoError(t, err)
	require.Equal(t, source, prepared)
}

func TestNeedsMPSInputStagingRejectsPrivateAddresses(t *testing.T) {
	for _, source := range []string{
		"http://localhost/video.mp4",
		"http://127.0.0.1/video.mp4",
		"http://10.0.0.2/video.mp4",
		"http://WeKnora-minio:9000/vidsage/video.mp4",
	} {
		require.True(t, needsMPSInputStaging(source), source)
	}
	require.False(t, needsMPSInputStaging("https://cdn.example.com/video.mp4"))
}
