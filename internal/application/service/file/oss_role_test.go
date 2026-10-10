package file

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ossMetadataTransport func(*http.Request) (*http.Response, error)

func (f ossMetadataTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// The SDK uses the default transport for its fixed ECS metadata endpoint.
// Intercept it so these tests never contact real instance metadata or OSS.
func mockOSSMetadata(t *testing.T, respond func(*http.Request) (int, string)) {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = ossMetadataTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "100.100.100.200" {
			return nil, fmt.Errorf("unexpected network request: %s", req.URL.Host)
		}
		status, body := respond(req)
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
}

func ossMetadataCredentials(expiry time.Time, generation int) string {
	value, _ := json.Marshal(map[string]any{
		"Code": "Success", "AccessKeyId": fmt.Sprintf("role-ak-%d", generation),
		"AccessKeySecret": "role-secret",
		"SecurityToken":   fmt.Sprintf("role-token-%d", generation),
		"Expiration":      expiry.UTC(),
	})
	return string(value)
}

func whitelistOSSTestEndpoint(t *testing.T, host string) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", host)
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
}

func TestOSSRoleCredentialsDiscoveryCacheAndRefresh(t *testing.T) {
	whitelistOSSTestEndpoint(t, "oss.example.com")
	discoveries, fetches := 0, 0
	mockOSSMetadata(t, func(req *http.Request) (int, string) {
		if strings.HasSuffix(req.URL.Path, "security-credentials/") {
			discoveries++
			return 200, "zhongtaiOSS"
		}
		assert.Equal(t, "/latest/meta-data/ram/security-credentials/zhongtaiOSS", req.URL.Path)
		fetches++
		expiry := time.Now().Add(time.Hour)
		if fetches == 1 {
			expiry = time.Now().Add(-time.Second)
		}
		return 200, ossMetadataCredentials(expiry, fetches)
	})
	config, err := newOSSConfig(types.OSSEngineConfig{
		Endpoint: "https://oss.example.com",
		Region:   "cn-beijing",
		AuthType: types.OSSAuthECSRAMRole,
	})
	require.NoError(t, err)
	first, err := config.CredentialsProvider.GetCredentials(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "role-ak-1", first.AccessKeyID)
	second, err := config.CredentialsProvider.GetCredentials(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "role-ak-2", second.AccessKeyID)
	third, err := config.CredentialsProvider.GetCredentials(context.Background())
	require.NoError(t, err)
	assert.Equal(t, second, third)
	assert.Equal(t, 1, discoveries)
	assert.Equal(t, 2, fetches, "expired credentials must refresh; valid credentials must be cached")
}

func TestOSSRoleMainAndTempBucketRequests(t *testing.T) {
	fetches := 0
	mockOSSMetadata(t, func(req *http.Request) (int, string) {
		assert.Equal(t, "/latest/meta-data/ram/security-credentials/zhongtaiOSS", req.URL.Path)
		fetches++
		return 200, ossMetadataCredentials(time.Now().Add(time.Hour), 1)
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		assert.Equal(t, "role-token-1", req.Header.Get("x-oss-security-token"))
		assert.Contains(t, req.Header.Get("Authorization"), "role-ak-1/")
		w.Header().Set("x-oss-request-id", "test-request")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	whitelistOSSTestEndpoint(t, "127.0.0.1")
	config := types.OSSEngineConfig{
		Endpoint:       server.URL,
		Region:         "cn-beijing",
		BucketName:     "sig-zhongtai",
		AuthType:       types.OSSAuthECSRAMRole,
		RoleName:       "zhongtaiOSS",
		UseTempBucket:  true,
		TempBucketName: "temp-bucket",
		TempRegion:     "cn-shanghai",
	}
	service, _, err := NewFileServiceFromStorageConfig("oss", &types.StorageEngineConfig{OSS: &config}, "")
	require.NoError(t, err)
	svc := service.(*ossFileService)
	require.NotNil(t, svc.tempClient)
	for _, target := range []struct {
		client         *oss.Client
		bucket, region string
	}{
		{svc.client, "sig-zhongtai", "cn-beijing"},
		{svc.tempClient, "temp-bucket", "cn-shanghai"},
	} {
		_, err := target.client.PutObject(context.Background(), &oss.PutObjectRequest{
			Bucket: oss.Ptr(target.bucket),
			Key:    oss.Ptr("file.txt"),
			Body:   strings.NewReader("test"),
		})
		require.NoError(t, err)
		download, err := target.client.GetObject(context.Background(), &oss.GetObjectRequest{
			Bucket: oss.Ptr(target.bucket),
			Key:    oss.Ptr("file.txt"),
		})
		require.NoError(t, err)
		require.NoError(t, download.Body.Close())
		_, err = target.client.DeleteObject(context.Background(), &oss.DeleteObjectRequest{
			Bucket: oss.Ptr(target.bucket),
			Key:    oss.Ptr("file.txt"),
		})
		require.NoError(t, err)
		presigned, err := target.client.Presign(context.Background(), &oss.GetObjectRequest{
			Bucket: oss.Ptr(target.bucket),
			Key:    oss.Ptr("file.txt"),
		})
		require.NoError(t, err)
		u, err := url.Parse(presigned.URL)
		require.NoError(t, err)
		assert.Equal(t, "role-token-1", u.Query().Get("x-oss-security-token"))
		assert.Contains(t, u.Query().Get("x-oss-credential"), "/"+target.region+"/oss/")
		assert.Contains(t, u.Path, target.bucket)
	}
	assert.Equal(t, 1, fetches, "main and temp bucket clients must share the credentials cache")
	require.NoError(t, CheckOssConnectivityWithConfig(context.Background(), config))
}

func TestOSSStaticCredentialsStayExplicit(t *testing.T) {
	whitelistOSSTestEndpoint(t, "oss.example.com")
	client, err := newOSSClient("https://oss.example.com", "cn-beijing", "static-ak", "static-sk")
	require.NoError(t, err)
	presigned, err := client.Presign(context.Background(), &oss.GetObjectRequest{
		Bucket: oss.Ptr("bucket"), Key: oss.Ptr("file.txt"),
	})
	require.NoError(t, err)
	u, err := url.Parse(presigned.URL)
	require.NoError(t, err)
	assert.Contains(t, u.Query().Get("x-oss-credential"), "static-ak/")
	assert.Empty(t, u.Query().Get("x-oss-security-token"))
}

func TestOSSRoleMetadataFailureIsSafe(t *testing.T) {
	whitelistOSSTestEndpoint(t, "oss.example.com")
	mockOSSMetadata(t, func(*http.Request) (int, string) {
		return 200, `{"Code":"Success","AccessKeySecret":"secret-must-not-leak","SecurityToken":"token-must-not-leak"}`
	})
	client, err := newOSSClientWithConfig(types.OSSEngineConfig{
		Endpoint: "https://oss.example.com",
		Region:   "cn-beijing",
		AuthType: types.OSSAuthECSRAMRole,
		RoleName: "role",
	})
	require.NoError(t, err)
	_, err = client.Presign(context.Background(), &oss.GetObjectRequest{
		Bucket: oss.Ptr("bucket"),
		Key:    oss.Ptr("file"),
	})
	require.ErrorContains(t, err, "OSS ECS RAM role credentials unavailable")
	assert.NotContains(t, err.Error(), "secret-must-not-leak")
	assert.NotContains(t, err.Error(), "token-must-not-leak")
	assert.Contains(t, utils.SanitizeStorageConnectivityError(err), "ECS RAM")
}

func TestOSSRoleStillRejectsUnsafeEndpoint(t *testing.T) {
	_, err := newOSSClientWithConfig(types.OSSEngineConfig{
		Endpoint: "http://100.100.100.200/latest/meta-data",
		Region:   "cn-beijing",
		AuthType: types.OSSAuthECSRAMRole,
	})
	require.ErrorContains(t, err, "unsafe OSS endpoint")
}
