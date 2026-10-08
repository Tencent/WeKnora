package file

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ks3sdklib/aws-sdk-go/aws/awserr"
	"github.com/stretchr/testify/require"
	"github.com/tencentyun/cos-go-sdk-v5"

	"github.com/Tencent/WeKnora/internal/utils"
)

// The object storage clients must not carry Go's whole-request timeout: it
// bounds the body transfer too, so it capped every upload and download at
// what a link can move in 30 seconds (#3306).
func TestObjectStorageHTTPClientConfigDropsOnlyTheWholeRequestTimeout(t *testing.T) {
	want := utils.DefaultSSRFSafeHTTPClientConfig()
	want.Timeout = 0

	require.Equal(t, want, objectStorageHTTPClientConfig())
}

// Without the whole-request timeout, connection setup is bounded on the
// transport instead; a silent peer must still fail in bounded time.
func TestObjectStorageTransportBoundsConnectionSetup(t *testing.T) {
	transport := objectStorageTransport()

	require.Equal(t, 10*time.Second, transport.TLSHandshakeTimeout)
	require.Equal(t, objectStorageResponseHeaderTimeout, transport.ResponseHeaderTimeout)
	require.Equal(t, time.Second, transport.ExpectContinueTimeout)
	require.Equal(t, 90*time.Second, transport.IdleConnTimeout)
	require.NotNil(t, transport.DialContext, "the SSRF-safe dialer must stay in place")
}

func TestObjectStorageHTTPClientKeepsTheSSRFGuard(t *testing.T) {
	client := objectStorageHTTPClient()

	require.Zero(t, client.Timeout)
	_, ok := client.Transport.(*utils.SSRFValidatingRoundTripper)
	require.True(t, ok)
	require.NotNil(t, client.CheckRedirect)
}

func TestCOSHTTPClientHasNoWholeRequestTimeout(t *testing.T) {
	client := newCOSHTTPClient("id", "key")

	require.Zero(t, client.Timeout)
	auth, ok := client.Transport.(*cos.AuthorizationTransport)
	require.True(t, ok, "COS signing must stay the outermost transport")
	guard, ok := auth.Transport.(*utils.SSRFValidatingRoundTripper)
	require.True(t, ok, "the SSRF guard must stay underneath the signer")
	base, ok := guard.Base.(*http.Transport)
	require.True(t, ok)
	require.Equal(t, objectStorageResponseHeaderTimeout, base.ResponseHeaderTimeout)
}

func TestOBSClientHasNoWholeRequestTimeout(t *testing.T) {
	options := obsS3Options("obs.cn-north-4.myhuaweicloud.com", "cn-north-4", "ak", "sk")

	client, ok := options.HTTPClient.(*http.Client)
	require.True(t, ok)
	require.Zero(t, client.Timeout)
}

func TestS3ClientHasNoWholeRequestTimeout(t *testing.T) {
	svc, err := newS3Client("", "ak", "sk", "bucket", "us-east-1", "", false)
	require.NoError(t, err)

	client, ok := svc.client.Options().HTTPClient.(*http.Client)
	require.True(t, ok)
	require.Zero(t, client.Timeout)
}

func TestKS3ClientHasNoWholeRequestTimeout(t *testing.T) {
	// The endpoint check resolves the host unless it is whitelisted; keep the
	// test off the network.
	t.Setenv("SSRF_WHITELIST", "ks3-cn-beijing.ksyuncs.com")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	client, err := newKS3Client("https://ks3-cn-beijing.ksyuncs.com", "cn-beijing", "ak", "sk")
	require.NoError(t, err)

	require.Zero(t, client.Config.HTTPClient.Timeout)
}

func TestOSSClientHasNoWholeRequestTimeout(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "oss-cn-hangzhou.aliyuncs.com")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	client, err := newOSSClient("https://oss-cn-hangzhou.aliyuncs.com", "cn-hangzhou", "ak", "sk")
	require.NoError(t, err)
	require.NotNil(t, client)
	require.Zero(t, objectStorageHTTPClient().Timeout)
}

func TestObjectStorageTransferContextAddsFallbackDeadline(t *testing.T) {
	ctx, cancel := objectStorageTransferContext(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	remain := time.Until(deadline)
	require.Greater(t, remain, 29*time.Minute)
	require.Less(t, remain, 31*time.Minute)
}

func TestObjectStorageTransferContextKeepsCallerDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ctx, stop := objectStorageTransferContext(parent)
	defer stop()
	require.Equal(t, parent, ctx)
}

func TestObjectStorageBoundReaderCancelsOnClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	body := objectStorageBoundReader(io.NopCloser(strings.NewReader("x")), cancel)
	require.NoError(t, body.Close())
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestKS3BucketMissingDetection(t *testing.T) {
	require.True(t, isKS3BucketMissing(awserr.NewRequestFailure(
		awserr.New("NoSuchBucket", "missing", nil), http.StatusNotFound, "req")))
	require.True(t, isKS3BucketMissing(awserr.NewRequestFailure(
		awserr.New("NotFound", "missing", nil), http.StatusNotFound, "req")))
	require.False(t, isKS3BucketMissing(awserr.NewRequestFailure(
		awserr.New("AccessDenied", "denied", nil), http.StatusForbidden, "req")))
	require.False(t, isKS3BucketMissing(context.DeadlineExceeded))
}
