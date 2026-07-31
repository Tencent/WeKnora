package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Checksum returns a hex SHA-256 checksum for payload.
func Checksum(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// ValidatePayload verifies persisted payload metadata before a cache hit is used.
func ValidatePayload(payload []byte, size int64, checksum string) error {
	if int64(len(payload)) != size {
		return fmt.Errorf("artifact payload size mismatch: got %d want %d", len(payload), size)
	}
	if Checksum(payload) != checksum {
		return fmt.Errorf("artifact payload checksum mismatch")
	}
	return nil
}
