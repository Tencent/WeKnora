package utils

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAESKey = "01234567890123456789012345678901" // 32 bytes

func TestEncryptAESGCM(t *testing.T) {
	key := []byte(testAESKey)

	t.Run("encrypts plaintext with enc:v1: prefix", func(t *testing.T) {
		encrypted, err := EncryptAESGCM("sk-secret-key", key)
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(encrypted, EncPrefix))
		assert.NotEqual(t, "sk-secret-key", encrypted)
	})

	t.Run("returns empty string as-is", func(t *testing.T) {
		encrypted, err := EncryptAESGCM("", key)
		require.NoError(t, err)
		assert.Equal(t, "", encrypted)
	})

	t.Run("returns already encrypted string as-is (idempotent)", func(t *testing.T) {
		first, err := EncryptAESGCM("sk-secret-key", key)
		require.NoError(t, err)

		second, err := EncryptAESGCM(first, key)
		require.NoError(t, err)
		assert.Equal(t, first, second, "re-encrypting should be a no-op")
	})

	t.Run("returns plaintext when key is nil", func(t *testing.T) {
		encrypted, err := EncryptAESGCM("sk-secret-key", nil)
		require.NoError(t, err)
		assert.Equal(t, "sk-secret-key", encrypted)
	})
}

func TestDecryptAESGCM(t *testing.T) {
	key := []byte(testAESKey)

	t.Run("round-trip encrypt then decrypt", func(t *testing.T) {
		original := "sk-my-secret-api-key"
		encrypted, err := EncryptAESGCM(original, key)
		require.NoError(t, err)

		decrypted, err := DecryptAESGCM(encrypted, key)
		require.NoError(t, err)
		assert.Equal(t, original, decrypted)
	})

	t.Run("returns legacy plaintext as-is (no enc:v1: prefix)", func(t *testing.T) {
		decrypted, err := DecryptAESGCM("sk-legacy-plaintext", key)
		require.NoError(t, err)
		assert.Equal(t, "sk-legacy-plaintext", decrypted)
	})

	t.Run("returns empty string as-is", func(t *testing.T) {
		decrypted, err := DecryptAESGCM("", key)
		require.NoError(t, err)
		assert.Equal(t, "", decrypted)
	})

	t.Run("returns as-is when key is nil", func(t *testing.T) {
		decrypted, err := DecryptAESGCM("enc:v1:something", nil)
		require.NoError(t, err)
		assert.Equal(t, "enc:v1:something", decrypted)
	})
}

func TestGetAESKey(t *testing.T) {
	t.Run("returns key when SYSTEM_AES_KEY is 32 bytes", func(t *testing.T) {
		t.Setenv("SYSTEM_AES_KEY", testAESKey)
		key := GetAESKey()
		assert.Equal(t, []byte(testAESKey), key)
	})

	t.Run("returns nil when SYSTEM_AES_KEY is not set", func(t *testing.T) {
		t.Setenv("SYSTEM_AES_KEY", "")
		key := GetAESKey()
		assert.Nil(t, key)
	})

	t.Run("returns nil when SYSTEM_AES_KEY is wrong length", func(t *testing.T) {
		t.Setenv("SYSTEM_AES_KEY", "too-short")
		key := GetAESKey()
		assert.Nil(t, key)
	})
}
