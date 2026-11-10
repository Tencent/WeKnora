package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStorageEndpointHost(t *testing.T) {
	tests := []struct {
		endpoint string
		want     string
	}{
		{"127.0.0.1:9000", "127.0.0.1"},
		{"http://127.0.0.1:9000", "127.0.0.1"},
		{"https://127.0.0.1:9000", "127.0.0.1"},
		{"minio.internal:9000", "minio.internal"},
		{"https://s3.amazonaws.com", "s3.amazonaws.com"},
	}
	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			assert.Equal(t, tt.want, storageEndpointHost(tt.endpoint))
		})
	}
}

func TestIsBlockedStorageEndpoint_SchemePrefixedLoopback(t *testing.T) {
	for _, endpoint := range []string{
		"http://127.0.0.1:9000",
		"https://127.0.0.1:9000",
		"127.0.0.1:9000",
	} {
		t.Run(endpoint, func(t *testing.T) {
			blocked, reason := isBlockedStorageEndpoint(endpoint)
			assert.True(t, blocked, "expected loopback endpoint to be blocked")
			assert.NotEmpty(t, reason)
		})
	}
}
