package common

import (
	"crypto/tls"
	"os"
	"strings"
)

// RedisTLSConfig builds a *tls.Config for Redis connections from environment
// variables. It returns nil when TLS is disabled, so the result can be assigned
// directly to redis.Options.TLSConfig or asynq.RedisClientOpt.TLSConfig (a nil
// value keeps the existing plaintext behavior).
//
// Environment variables:
//
//	REDIS_USE_TLS                   Enable TLS when "true" (default: disabled).
//	REDIS_TLS_SERVER_NAME          Optional server name for certificate
//	                               verification and SNI (useful when the address
//	                               is an IP rather than a hostname).
//	REDIS_TLS_INSECURE_SKIP_VERIFY Skip server certificate verification when
//	                               "true". INSECURE — intended for development or
//	                               self-signed setups only; do not use in production.
//
// The server certificate is verified by default; verification is only relaxed
// when REDIS_TLS_INSECURE_SKIP_VERIFY is explicitly set to "true".
func RedisTLSConfig() *tls.Config {
	if !envTrue("REDIS_USE_TLS") {
		return nil
	}

	cfg := &tls.Config{MinVersion: tls.VersionTLS12}

	if serverName := strings.TrimSpace(os.Getenv("REDIS_TLS_SERVER_NAME")); serverName != "" {
		cfg.ServerName = serverName
	}
	if envTrue("REDIS_TLS_INSECURE_SKIP_VERIFY") {
		cfg.InsecureSkipVerify = true
	}

	return cfg
}

// envTrue reports whether the named environment variable is set to "true"
// (case-insensitive, surrounding whitespace ignored).
func envTrue(name string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(name)), "true")
}
