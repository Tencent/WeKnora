package feishu

import (
	"crypto/aes"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

// Webhook bodies reach decrypt() before the verification token is checked, and
// CBC decryption panics when the ciphertext is not a whole number of blocks
// ("input not full blocks"). Such a body must be rejected as an error instead.
func TestDecryptRejectsCiphertextWithPartialBlock(t *testing.T) {
	a := &Adapter{encryptKey: "test-encrypt-key"}

	for _, size := range []int{aes.BlockSize + 1, aes.BlockSize*2 + 5, 1} {
		payload := base64.StdEncoding.EncodeToString(make([]byte, size))
		_, err := a.decrypt(payload)
		require.Error(t, err, "ciphertext of %d bytes must be rejected", size)
	}
}
