package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

const CacheFingerprintSchemaV1 = "cache-fingerprint-v1"

// CacheFingerprint returns a stable SHA-256 fingerprint for a cache scope and
// JSON-serializable payload. Encoding the scope and schema version keeps
// unrelated cache layers from accidentally sharing keys.
func CacheFingerprint(scope string, payload any) string {
	b, err := json.Marshal(payload)
	if err != nil {
		b = []byte(strings.TrimSpace(scope))
	}
	h := sha256.New()
	_, _ = h.Write([]byte(CacheFingerprintSchemaV1))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write([]byte(strings.TrimSpace(scope)))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}
