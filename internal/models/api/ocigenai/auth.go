// Package ocigenai speaks the native OCI Generative AI inference API
// (https://docs.oracle.com/en-us/iaas/api/#/en/generative-ai-inference/20231130/):
// the embedText and rerankText actions, signed with OCI IAM-Based
// Authentication rather than a bearer key.
//
// Signing uses the official SDK's request signer
// (github.com/oracle/oci-go-sdk/v65/common), so the HTTP Signature scheme,
// key handling and instance-principal token refresh are Oracle's code, not a
// re-implementation.
package ocigenai

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/common/auth"
)

// Auth modes, stored in a model row's extra_config.
const (
	// AuthAPIKey signs with a user's API signing key (tenancy, user,
	// fingerprint and the uploaded private key).
	AuthAPIKey = "api_key"
	// AuthInstancePrincipal signs as the OCI compute instance WeKnora runs
	// on; nothing is stored.
	AuthInstancePrincipal = "instance_principal"
)

// Credentials is what one model row stores for IAM authentication.
type Credentials struct {
	Mode        string
	Tenancy     string
	User        string
	Fingerprint string
	// PrivateKey is the PEM file content; Passphrase decrypts it when the
	// key is encrypted.
	PrivateKey string
	Passphrase string
}

// NewAuth builds the request signer for the credentials. Configuration
// mistakes (a missing OCID, a key that does not parse, no instance
// principal available) fail here, when the client is built, instead of as
// a 401 on the first request.
func NewAuth(c Credentials) (api.AuthFunc, error) {
	var provider common.ConfigurationProvider
	switch strings.TrimSpace(c.Mode) {
	case "", AuthAPIKey:
		var missing []string
		for _, f := range []struct{ name, value string }{
			{"tenancy OCID", c.Tenancy},
			{"user OCID", c.User},
			{"fingerprint", c.Fingerprint},
			{"private key", c.PrivateKey},
		} {
			if strings.TrimSpace(f.value) == "" {
				missing = append(missing, f.name)
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("OCI API key authentication requires %s", strings.Join(missing, ", "))
		}
		var passphrase *string
		if c.Passphrase != "" {
			passphrase = &c.Passphrase
		}
		provider = common.NewRawConfigurationProvider(strings.TrimSpace(c.Tenancy), strings.TrimSpace(c.User),
			"", strings.TrimSpace(c.Fingerprint), strings.TrimSpace(c.PrivateKey), passphrase)
		if _, err := provider.PrivateRSAKey(); err != nil {
			return nil, fmt.Errorf("OCI private key: %w", err)
		}
	case AuthInstancePrincipal:
		p, err := instancePrincipal()
		if err != nil {
			return nil, fmt.Errorf("OCI instance principal: %w", err)
		}
		provider = p
	default:
		return nil, fmt.Errorf("unknown OCI auth mode %q", c.Mode)
	}
	signer := common.DefaultRequestSigner(provider)
	return func(req *http.Request, _ []byte) {
		// The signer covers the date header but, unlike the SDK's own
		// clients, does not set it; an unset one is signed as empty and OCI
		// answers 401 INVALID_AUTHENTICATION_INFO.
		if req.Header.Get("Date") == "" {
			req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
		}
		// ponytail: api.AuthFunc cannot fail. An unsigned request is
		// refused by OCI with 401 NotAuthenticated, and the cause is logged.
		if err := signer.Sign(req); err != nil {
			logger.Errorf(req.Context(), "OCI request signing failed: %v", err)
		}
	}, nil
}

var (
	instanceMu       sync.Mutex
	instanceProvider common.ConfigurationProvider
)

// instancePrincipal returns the process-wide instance principal. It is the
// VM's identity, so one provider (which refreshes its own token) serves
// every model row. A failure is not cached: the metadata service may simply
// not have been reachable yet.
func instancePrincipal() (common.ConfigurationProvider, error) {
	instanceMu.Lock()
	defer instanceMu.Unlock()
	if instanceProvider != nil {
		return instanceProvider, nil
	}
	p, err := auth.InstancePrincipalConfigurationProvider()
	if err != nil {
		return nil, err
	}
	instanceProvider = p
	return p, nil
}

// Extra-config keys of a model row (the provider's extra fields).
const (
	KeyAuthMode    = "auth_mode"
	KeyTenancy     = "tenancy_ocid"
	KeyUser        = "user_ocid"
	KeyFingerprint = "fingerprint"
	KeyCompartment = "compartment_id"
)

// NewConfig builds the client configuration of one model row. The uploaded
// private key is the row's secret credential (app_secret); the API key field
// holds its passphrase, if any. endpoint.Auth is replaced by the OCI signer.
func NewConfig(endpoint api.Endpoint, passphrase, privateKey string, extra map[string]string) (Config, error) {
	compartment := strings.TrimSpace(extra[KeyCompartment])
	if compartment == "" {
		return Config{}, fmt.Errorf("OCI Generative AI requires a compartment OCID (%s)", KeyCompartment)
	}
	signer, err := NewAuth(Credentials{
		Mode:        extra[KeyAuthMode],
		Tenancy:     extra[KeyTenancy],
		User:        extra[KeyUser],
		Fingerprint: extra[KeyFingerprint],
		PrivateKey:  privateKey,
		Passphrase:  passphrase,
	})
	if err != nil {
		return Config{}, err
	}
	endpoint.Auth = signer
	return Config{Endpoint: endpoint, CompartmentID: compartment}, nil
}
