package rerank

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api/ocigenai"
	"github.com/Tencent/WeKnora/internal/models/providers"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// ociTestRow is the extra_config and uploaded private key of an OCI
// Generative AI row signed with a throwaway API signing key.
func ociTestRow(t *testing.T) (map[string]string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return map[string]string{
		ocigenai.KeyCompartment: "ocid1.compartment.oc1..test",
		ocigenai.KeyTenancy:     "ocid1.tenancy.oc1..test",
		ocigenai.KeyUser:        "ocid1.user.oc1..test",
		ocigenai.KeyFingerprint: "aa:bb",
	}, string(pemKey)
}

func TestOCIGenAIRerankWire(t *testing.T) {
	u := newUpstream(t)
	extra, key := ociTestRow(t)
	client, err := newReranker(&RerankerConfig{
		Source: types.ModelSourceRemote, Provider: providers.OCIGenAIID, ModelName: "cohere.rerank-v4.0-fast",
		BaseURL: u.url, ExtraConfig: extra, AppSecret: key,
	})
	require.NoError(t, err)
	results, err := client.Rerank(context.Background(), "q", []string{"a", "bbb", "cc"})
	require.NoError(t, err)

	// Ranked by score; the upstream scores longer documents higher.
	require.Equal(t, []int{1, 2, 0}, []int{results[0].Index, results[1].Index, results[2].Index})
	require.Len(t, u.requests, 1)
	req := u.requests[0]
	require.Equal(t, "/20231130/actions/rerankText", req.path)
	require.Equal(t, "q", req.body["input"])
	require.Equal(t, "ocid1.compartment.oc1..test", req.body["compartmentId"])
	require.Equal(t, map[string]any{"servingType": "ON_DEMAND", "modelId": "cohere.rerank-v4.0-fast"},
		req.body["servingMode"])
	require.EqualValues(t, 3, req.body["topN"])
	require.Contains(t, req.header.Get("Authorization"), `keyId="ocid1.tenancy.oc1..test/ocid1.user.oc1..test/aa:bb"`)
}
