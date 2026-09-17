package file

import (
	"net/http"
	"testing"
	"time"

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
	require.Equal(t, 60*time.Second, transport.ResponseHeaderTimeout)
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
	require.Equal(t, 60*time.Second, base.ResponseHeaderTimeout)
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
