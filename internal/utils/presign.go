package utils

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// presignPath is the URL path for presigned file access.
	presignPath = "/api/v1/files/presigned"
	// presignDefaultTTL is the default validity period for presigned URLs.
	// Kept short because the HMAC key alone authorizes cross-tenant access —
	// a leaked URL should expire before it can be widely abused. IM clients
	// typically fetch and cache images within seconds of receipt.
	presignDefaultTTL = 2 * time.Hour
)

// getPresignKey returns the HMAC key derived from SYSTEM_AES_KEY.
// Returns nil if the key is not configured or invalid.
func getPresignKey() []byte {
	key := os.Getenv("SYSTEM_AES_KEY")
	if len(key) < 16 {
		return nil
	}
	return []byte(key)
}

// signPayload computes HMAC-SHA256 over the canonical payload string.
func signPayload(key []byte, filePath string, tenantID uint64, expires int64) string {
	payload := fmt.Sprintf("file_path=%s&tenant_id=%d&expires=%d", filePath, tenantID, expires)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// SignFileURL generates a presigned HTTP URL for accessing a storage file.
// baseURL is the external URL of the WeKnora instance (e.g. "https://weknora.example.com").
// filePath is the provider:// storage path (e.g. "local://1/abc/img.png").
// tenantID identifies the tenant that owns the file.
// ttl is how long the URL remains valid (0 uses the default presignDefaultTTL).
//
// Returns ("", error) if the signing key is not configured.
func SignFileURL(baseURL, filePath string, tenantID uint64, ttl time.Duration) (string, error) {
	key := getPresignKey()
	if key == nil {
		return "", fmt.Errorf("presign: SYSTEM_AES_KEY not configured")
	}
	if ttl <= 0 {
		ttl = presignDefaultTTL
	}
	expires := time.Now().Add(ttl).Unix()
	sig := signPayload(key, filePath, tenantID, expires)

	u, err := url.Parse(strings.TrimRight(baseURL, "/") + presignPath)
	if err != nil {
		return "", fmt.Errorf("presign: invalid base URL: %w", err)
	}
	q := u.Query()
	q.Set("file_path", filePath)
	q.Set("tenant_id", strconv.FormatUint(tenantID, 10))
	q.Set("expires", strconv.FormatInt(expires, 10))
	q.Set("sig", sig)
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// VerifyFileURLSig checks the HMAC signature and expiry of a presigned URL.
// Returns true only if the signature is valid and the URL has not expired.
func VerifyFileURLSig(filePath string, tenantID uint64, expiresStr, sig string) bool {
	key := getPresignKey()
	if key == nil {
		return false
	}

	expires, err := strconv.ParseInt(expiresStr, 10, 64)
	if err != nil {
		return false
	}

	// Check expiry.
	if time.Now().Unix() > expires {
		return false
	}

	// Verify signature.
	expected := signPayload(key, filePath, tenantID, expires)
	return hmac.Equal([]byte(expected), []byte(sig))
}

// ParseTenantIDFromStoragePath extracts the tenant ID from a provider:// storage path.
// Storage paths follow the convention: {scheme}://{tenantID}/...
// Returns 0 if the path does not contain a valid tenant ID.
//
// NOTE: This is a best-effort fallback. Prefer passing the tenant ID from
// request context when available — for cloud providers with numeric bucket
// or region names, the first numeric segment may not be the tenant ID.
func ParseTenantIDFromStoragePath(filePath string) uint64 {
	// Strip scheme: "local://1/abc/img.png" → "1/abc/img.png"
	_, rest, ok := strings.Cut(filePath, "://")
	if !ok {
		return 0
	}

	// Storage path layouts vary by provider:
	//   local://TENANT_ID/...
	//   minio://bucket/TENANT_ID/...
	//   s3://bucket/prefix/TENANT_ID/...
	//   cos://bucket/region/prefix/TENANT_ID/...
	//   tos://bucket/TENANT_ID/...
	//   oss://bucket/prefix/TENANT_ID/...
	// We try each slash-separated segment until we find a numeric tenant ID.
	parts := strings.Split(rest, "/")
	for _, part := range parts {
		if id, err := strconv.ParseUint(part, 10, 64); err == nil {
			return id
		}
	}

	return 0
}
