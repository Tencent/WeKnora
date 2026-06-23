package im

import (
	"context"
	"io"
	"mime/multipart"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubIMFileService implements interfaces.FileService for IM resolver tests.
type stubIMFileService struct {
	getFileURL func(ctx context.Context, filePath string) (string, error)
}

func (s *stubIMFileService) CheckConnectivity(context.Context) error { return nil }

func (s *stubIMFileService) SaveFile(context.Context, *multipart.FileHeader, uint64, string) (string, error) {
	return "", nil
}

func (s *stubIMFileService) SaveBytes(context.Context, []byte, uint64, string, bool) (string, error) {
	return "", nil
}

func (s *stubIMFileService) GetFile(context.Context, string) (io.ReadCloser, error) {
	return nil, nil
}

func (s *stubIMFileService) GetFileURL(ctx context.Context, filePath string) (string, error) {
	if s.getFileURL != nil {
		return s.getFileURL(ctx, filePath)
	}
	return "https://global-storage.example/" + filePath, nil
}

func (s *stubIMFileService) DeleteFile(context.Context, string) error { return nil }

func (s *stubIMFileService) CopyFile(context.Context, string, uint64, string) (string, error) {
	return "", nil
}

func TestBuildIMFileServiceForProvider_FallbackToGlobal(t *testing.T) {
	stub := &stubIMFileService{}
	tenant := &types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: "cos",
			COS: &types.COSEngineConfig{
				SecretID:   "id",
				SecretKey:  "key",
				BucketName: "bucket",
				Region:     "ap-shanghai",
			},
		},
	}

	svc := buildIMFileServiceForProvider(tenant, "minio", stub)
	require.NotNil(t, svc)
	got, err := svc.GetFileURL(context.Background(), "minio://wizard-test/10000/exports/a.png")
	require.NoError(t, err)
	assert.Equal(t, "https://global-storage.example/minio://wizard-test/10000/exports/a.png", got)
}

func TestIMFileServiceResolver_CachesPerProvider(t *testing.T) {
	stub := &stubIMFileService{}
	tenant := &types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: "cos",
			COS: &types.COSEngineConfig{
				SecretID:   "id",
				SecretKey:  "key",
				BucketName: "bucket",
				Region:     "ap-shanghai",
			},
		},
	}
	r := newIMFileServiceResolver(tenant, stub)

	svc1 := r.resolve("minio://wizard-test/10000/a.png")
	svc2 := r.resolve("minio://wizard-test/10000/b.png")
	assert.Same(t, svc1, svc2, "same provider should reuse cached FileService")

	svc3 := r.resolve("local://10000/c.png")
	assert.NotSame(t, svc1, svc3, "different provider should use a different service")
}

func TestRewriteStorageURLs_MinIOFallbackViaGlobal(t *testing.T) {
	stub := &stubIMFileService{
		getFileURL: func(_ context.Context, filePath string) (string, error) {
			return "https://minio.example/presigned?path=" + filePath, nil
		},
	}
	tenant := &types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: "cos",
			COS: &types.COSEngineConfig{
				SecretID:   "id",
				SecretKey:  "key",
				BucketName: "bucket",
				Region:     "ap-shanghai",
			},
		},
	}
	in := `![知识助理"知识库"管理视图界面](minio://wizard-test/10000/exports/c91cf852.png)`
	resolver := newIMFileServiceResolver(tenant, stub)
	out := rewriteStorageURLs(context.Background(), in, resolver)
	assert.Contains(t, out, "https://minio.example/presigned")
	assert.NotContains(t, out, "](minio://")
}

func TestCleanIMContent_MinIOFallbackIntegration(t *testing.T) {
	stub := &stubIMFileService{
		getFileURL: func(_ context.Context, _ string) (string, error) {
			return "https://minio.example/img.png", nil
		},
	}
	tenant := &types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{DefaultProvider: "cos"},
	}
	in := "see ![x](minio://wizard-test/10000/exports/x.png) ok"
	out := cleanIMContent(context.Background(), in, tenant, stub)
	assert.Contains(t, out, "https://minio.example/img.png")
}
