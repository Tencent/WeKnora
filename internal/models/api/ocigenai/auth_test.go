package ocigenai

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

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

	// The signature verifies against the public key over exactly the signed
	// headers (draft-cavage HTTP signatures, as OCI specifies).
	verifySignature(t, req, body, &key.PublicKey)

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

func verifySignature(t *testing.T, req *http.Request, body []byte, pub *rsa.PublicKey) {
	t.Helper()
	auth := req.Header.Get("Authorization")
	field := func(name string) string {
		m := regexp.MustCompile(name + `="([^"]*)"`).FindStringSubmatch(auth)
		require.Len(t, m, 2, name)
		return m[1]
	}
	sum := sha256.Sum256(body)
	assert.Equal(t, base64.StdEncoding.EncodeToString(sum[:]), req.Header.Get("X-Content-Sha256"))
	var lines []string
	for _, h := range strings.Fields(field("headers")) {
		value := req.Header.Get(h)
		switch h {
		case "(request-target)":
			value = strings.ToLower(req.Method) + " " + req.URL.RequestURI()
		case "host":
			value = req.URL.Host
		}
		lines = append(lines, h+": "+value)
	}
	digest := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	sig, err := base64.StdEncoding.DecodeString(field("signature"))
	require.NoError(t, err)
	require.NoError(t, rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig))
}

func TestInstancePrincipalIsOptIn(t *testing.T) {
	t.Setenv(EnvAllowInstancePrincipal, "")
	_, err := NewAuth(Credentials{Mode: AuthInstancePrincipal})
	assert.ErrorContains(t, err, EnvAllowInstancePrincipal)
}

func TestInstancePrincipalFailureIsCachedBriefly(t *testing.T) {
	t.Setenv(EnvAllowInstancePrincipal, "true")
	probes := 0
	restore := probeInstanceMetadata
	probeInstanceMetadata = func() error { probes++; return errors.New("unreachable") }
	t.Cleanup(func() {
		probeInstanceMetadata = restore
		instanceMu.Lock()
		instanceProvider, instanceErr, instanceErrAt = nil, nil, time.Time{}
		instanceMu.Unlock()
	})

	_, err := NewAuth(Credentials{Mode: AuthInstancePrincipal})
	assert.ErrorContains(t, err, "unreachable")
	_, err = NewAuth(Credentials{Mode: AuthInstancePrincipal})
	assert.ErrorContains(t, err, "unreachable")
	assert.Equal(t, 1, probes, "a recent failure is not retried")

	instanceMu.Lock()
	instanceErrAt = time.Now().Add(-2 * instanceRetryAfter)
	instanceMu.Unlock()
	_, _ = NewAuth(Credentials{Mode: AuthInstancePrincipal})
	assert.Equal(t, 2, probes, "an old failure is retried")
}
