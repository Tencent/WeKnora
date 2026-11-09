package embedding

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

func TestOCIGenAIEmbeddingWire(t *testing.T) {
	u := newUpstream(t)
	extra, key := ociTestRow(t)
	client, err := newRemoteEmbedder(Config{
		Source: types.ModelSourceRemote, Provider: providers.OCIGenAIID, ModelName: "cohere.embed-v4.0",
		BaseURL: u.url, ExtraConfig: extra, AppSecret: key, Dimensions: 512, SupportsDimensionOverride: true,
	}, nil)
	require.NoError(t, err)
	_, err = client.BatchEmbed(types.WithEmbedQuery(context.Background()), []string{"有給休暇"})
	require.NoError(t, err)

	require.Len(t, u.requests, 1)
	req := u.requests[0]
	require.Equal(t, "/20231130/actions/embedText", req.path)
	require.Equal(t, "ocid1.compartment.oc1..test", req.body["compartmentId"])
	require.Equal(t, map[string]any{"servingType": "ON_DEMAND", "modelId": "cohere.embed-v4.0"},
		req.body["servingMode"])
	require.Equal(t, "SEARCH_QUERY", req.body["inputType"])
	require.Equal(t, "END", req.body["truncate"])
	require.EqualValues(t, 512, req.body["outputDimensions"])
	// Signed with the row's API signing key, date included.
	require.NotEmpty(t, req.header.Get("Date"))
	require.NotEmpty(t, req.header.Get("X-Content-Sha256"))
	require.Contains(t, req.header.Get("Authorization"),
		`keyId="ocid1.tenancy.oc1..test/ocid1.user.oc1..test/aa:bb"`)

	// Without a compartment the client is not built at all.
	delete(extra, ocigenai.KeyCompartment)
	_, err = newRemoteEmbedder(Config{
		Source: types.ModelSourceRemote, Provider: providers.OCIGenAIID, ModelName: "cohere.embed-v4.0",
		BaseURL: u.url, ExtraConfig: extra, AppSecret: key,
	}, nil)
	require.ErrorContains(t, err, "compartment")
}
