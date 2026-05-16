// Package client provides a docreader gRPC client and the shared TLS / token
// authentication helpers used by both the standalone Go SDK in this package
// and the internal docparser wrapper. Keep all auth/TLS construction here so
// the two call sites cannot drift on security defaults.
package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// AuthConfig holds the docreader gRPC client TLS / token configuration.
type AuthConfig struct {
	TLSEnabled bool
	CertFile   string
	KeyFile    string
	CAFile     string
	// ServerName overrides the SNI / certificate-host check on the client.
	// When empty, the address passed to Dial is used by Go's TLS stack.
	ServerName string

	AuthToken string
}

// LoadAuthConfigFromEnv reads docreader gRPC auth knobs from the process
// environment. The caller must pass the result to BuildDialOptions to apply
// them to a gRPC connection.
func LoadAuthConfigFromEnv() *AuthConfig {
	return &AuthConfig{
		TLSEnabled: os.Getenv("GRPC_TLS_ENABLED") == "true",
		CertFile:   os.Getenv("GRPC_TLS_CERT"),
		KeyFile:    os.Getenv("GRPC_TLS_KEY"),
		CAFile:     os.Getenv("GRPC_TLS_CA"),
		ServerName: os.Getenv("GRPC_TLS_SERVER_NAME"),
		AuthToken:  os.Getenv("GRPC_AUTH_TOKEN"),
	}
}

// BuildDialOptions returns the gRPC DialOptions that apply the configured
// transport credentials and per-RPC token. Callers should append their own
// per-call options (load balancer, message size, etc.).
func (c *AuthConfig) BuildDialOptions(maxMsgSize int) ([]grpc.DialOption, error) {
	opts := []grpc.DialOption{
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxMsgSize),
			grpc.MaxCallSendMsgSize(maxMsgSize),
		),
	}

	if c.TLSEnabled {
		creds, err := c.buildTLSCredentials()
		if err != nil {
			return nil, fmt.Errorf("failed to build TLS credentials: %w", err)
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
		Logger.Printf("INFO: TLS enabled for gRPC client")
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	if c.AuthToken != "" {
		// Only allow per-RPC tokens to ride a secured channel. This mirrors
		// gRPC's own oauth2 credentials behaviour and prevents the bearer
		// token from leaking on plaintext connections.
		opts = append(opts, grpc.WithPerRPCCredentials(&tokenAuth{
			token:           c.AuthToken,
			requireTLSGuard: c.TLSEnabled,
		}))
		Logger.Printf("INFO: Token authentication enabled for gRPC client (TLS=%v)", c.TLSEnabled)
	}

	return opts, nil
}

func (c *AuthConfig) buildTLSCredentials() (credentials.TransportCredentials, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: c.ServerName,
	}

	if c.CAFile != "" {
		caCert, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}
		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = certPool
	}

	switch {
	case c.CertFile != "" && c.KeyFile != "":
		cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
		Logger.Printf("INFO: mTLS enabled (client certificate loaded)")
	case c.CertFile != "" || c.KeyFile != "":
		return nil, fmt.Errorf(
			"GRPC_TLS_CERT and GRPC_TLS_KEY must be set together for mTLS",
		)
	}

	return credentials.NewTLS(tlsConfig), nil
}

type tokenAuth struct {
	token string
	// requireTLSGuard mirrors AuthConfig.TLSEnabled; we expose it via
	// RequireTransportSecurity so gRPC will refuse to send the bearer token
	// over an insecure connection when the operator has enabled TLS.
	requireTLSGuard bool
}

func (t *tokenAuth) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"authorization": "Bearer " + t.token,
	}, nil
}

func (t *tokenAuth) RequireTransportSecurity() bool {
	return t.requireTLSGuard
}
