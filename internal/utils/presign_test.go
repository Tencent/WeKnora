package utils

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignFileURL_RoundTrip(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "weknora-test-aes-key-32bytes!!!")

	baseURL := "https://weknora.example.com"
	filePath := "local://1/abc/img.png"
	var tenantID uint64 = 1

	signed, err := SignFileURL(baseURL, filePath, tenantID, 1*time.Hour)
	require.NoError(t, err)
	assert.Contains(t, signed, "https://weknora.example.com/api/v1/files/presigned")
	assert.Contains(t, signed, "file_path=")
	assert.Contains(t, signed, "tenant_id=1")
	assert.Contains(t, signed, "sig=")
}

func TestSignFileURL_NoKey(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "")

	_, err := SignFileURL("https://example.com", "local://1/img.png", 1, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SYSTEM_AES_KEY")
}

func TestVerifyFileURLSig_Valid(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "weknora-test-aes-key-32bytes!!!")

	filePath := "local://42/knowledge/img.jpg"
	var tenantID uint64 = 42
	key := getPresignKey()
	require.NotNil(t, key)

	expires := time.Now().Add(1 * time.Hour).Unix()
	sig := signPayload(key, filePath, tenantID, expires)

	assert.True(t, VerifyFileURLSig(filePath, tenantID, strconv.FormatInt(expires, 10), sig))
}

func TestVerifyFileURLSig_Expired(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "weknora-test-aes-key-32bytes!!!")

	filePath := "local://1/img.png"
	var tenantID uint64 = 1
	key := getPresignKey()
	require.NotNil(t, key)

	expires := time.Now().Add(-1 * time.Hour).Unix() // already expired
	sig := signPayload(key, filePath, tenantID, expires)

	assert.False(t, VerifyFileURLSig(filePath, tenantID, strconv.FormatInt(expires, 10), sig))
}

func TestVerifyFileURLSig_Tampered(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "weknora-test-aes-key-32bytes!!!")

	filePath := "local://1/img.png"
	var tenantID uint64 = 1
	key := getPresignKey()
	require.NotNil(t, key)

	expires := time.Now().Add(1 * time.Hour).Unix()
	sig := signPayload(key, filePath, tenantID, expires)
	expiresStr := strconv.FormatInt(expires, 10)

	// Tamper with file path
	assert.False(t, VerifyFileURLSig("local://1/other.png", tenantID, expiresStr, sig))
	// Tamper with tenant ID
	assert.False(t, VerifyFileURLSig(filePath, 999, expiresStr, sig))
	// Tamper with signature
	assert.False(t, VerifyFileURLSig(filePath, tenantID, expiresStr, "deadbeef"))
}

func TestVerifyFileURLSig_NoKey(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "")

	assert.False(t, VerifyFileURLSig("local://1/img.png", 1, "99999999999", "abc"))
}

func TestParseTenantIDFromStoragePath(t *testing.T) {
	tests := []struct {
		path string
		want uint64
	}{
		{"local://1/abc/img.png", 1},
		{"local://42/knowledge/file.pdf", 42},
		{"minio://bucket/1/abc/img.png", 1},
		{"s3://bucket/weknora/1/abc/img.png", 1},
		{"cos://bucket/region/prefix/1/abc/img.png", 1},
		{"tos://bucket/1/abc/img.png", 1},
		{"oss://bucket/weknora/1/abc/img.png", 1},
		{"https://example.com/img.png", 0},
		{"invalid", 0},
		{"local://exports/file.csv", 0},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := ParseTenantIDFromStoragePath(tt.path)
			assert.Equal(t, tt.want, got, "ParseTenantIDFromStoragePath(%q)", tt.path)
		})
	}
}
