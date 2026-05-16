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

type AuthConfig struct {
	TLSEnabled bool
	CertFile   string
	KeyFile    string
	CAFile     string

	AuthToken string
}

func LoadAuthConfigFromEnv() *AuthConfig {
	return &AuthConfig{
		TLSEnabled: os.Getenv("GRPC_TLS_ENABLED") == "true",
		CertFile:   os.Getenv("GRPC_TLS_CERT"),
		KeyFile:    os.Getenv("GRPC_TLS_KEY"),
		CAFile:     os.Getenv("GRPC_TLS_CA"),
		AuthToken:  os.Getenv("GRPC_AUTH_TOKEN"),
	}
}

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
		opts = append(opts, grpc.WithPerRPCCredentials(&tokenAuth{token: c.AuthToken}))
		Logger.Printf("INFO: Token authentication enabled for gRPC client")
	}

	return opts, nil
}

func (c *AuthConfig) buildTLSCredentials() (credentials.TransportCredentials, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
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

	if c.CertFile != "" && c.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
		Logger.Printf("INFO: mTLS enabled (client certificate loaded)")
	}

	return credentials.NewTLS(tlsConfig), nil
}

type tokenAuth struct {
	token string
}

func (t *tokenAuth) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"authorization": "Bearer " + t.token,
	}, nil
}

func (t *tokenAuth) RequireTransportSecurity() bool {
	return false
}
