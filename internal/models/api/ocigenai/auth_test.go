package ocigenai

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAuth(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	full := Credentials{
		Tenancy: "ocid1.tenancy.oc1..t", User: "ocid1.user.oc1..u", Fingerprint: "aa:bb", PrivateKey: pemKey,
	}

	sign, err := NewAuth(full)
	require.NoError(t, err)
	body := []byte(`{"inputs":["x"]}`)
	url := "https://inference.generativeai.us-chicago-1.oci.oraclecloud.com" + embedPath
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	sign(req, body)
	assert.NotEmpty(t, req.Header.Get("Date"), "the signer covers date, so it must be set")
	assert.Contains(t, req.Header.Get("Authorization"), `keyId="ocid1.tenancy.oc1..t/ocid1.user.oc1..u/aa:bb"`)
	assert.Contains(t, req.Header.Get("Authorization"),
		`headers="date (request-target) host content-length content-type x-content-sha256"`)

	missing := full
	missing.User, missing.PrivateKey = "", ""
	_, err = NewAuth(missing)
	assert.ErrorContains(t, err, "user OCID, private key")

	bad := full
	bad.PrivateKey = "-----BEGIN RSA PRIVATE KEY-----\nnot a key\n-----END RSA PRIVATE KEY-----"
	_, err = NewAuth(bad)
	assert.ErrorContains(t, err, "OCI private key")

	_, err = NewAuth(Credentials{Mode: "session_token"})
	assert.ErrorContains(t, err, "unknown OCI auth mode")
}

func TestNewConfigRequiresCompartment(t *testing.T) {
	_, err := NewConfig(api.Endpoint{}, "", "", map[string]string{})
	assert.ErrorContains(t, err, "compartment")
}
