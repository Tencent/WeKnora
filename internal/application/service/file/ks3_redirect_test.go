package file

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/utils"
)

// The KS3 SDK replaces the redirect policy on the client it is given with
// one that follows any target and carries the signature along; the client
// must come out of the constructor with the SSRF-safe policy it was built
// with.
func TestKS3ClientKeepsTheSSRFRedirectPolicy(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "ks3-cn-beijing.ksyuncs.com")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	client, err := newKS3Client("https://ks3-cn-beijing.ksyuncs.com", "cn-beijing", "ak", "secret")
	require.NoError(t, err)
	policy := client.Config.HTTPClient.CheckRedirect
	require.NotNil(t, policy)

	signed := httptest.NewRequest(http.MethodGet, "https://bucket.ks3-cn-beijing.ksyuncs.com/object", nil)
	signed.Header.Set("Authorization", "KSS ak:signature")

	// A redirect to another host must not take the signature with it.
	elsewhere := httptest.NewRequest(http.MethodGet, "https://93.184.216.34/object", nil)
	_ = policy(elsewhere, []*http.Request{signed})
	require.Empty(t, elsewhere.Header.Get("Authorization"))

	// A redirect into the private network is refused at redirect time, as
	// for every other client built on the SSRF-safe policy.
	inside := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/admin", nil)
	require.Error(t, policy(inside, []*http.Request{signed}))
}
