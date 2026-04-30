package utils

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"
)

// EncPrefix marks a string as AES-256-GCM encrypted
const EncPrefix = "enc:v1:"

// GetAESKey reads the 32-byte AES key from SYSTEM_AES_KEY env.
// Returns nil if not set or not exactly 32 bytes.
func GetAESKey() []byte {
	key := []byte(os.Getenv("SYSTEM_AES_KEY"))
	if len(key) == 32 {
		return key
	}
	return nil
}

// EncryptAESGCM encrypts plaintext with AES-256-GCM.
// Returns the original string if empty, already encrypted, or key is nil.
func EncryptAESGCM(plaintext string, key []byte) (string, error) {
	if plaintext == "" || key == nil {
		return plaintext, nil
	}
	if strings.HasPrefix(plaintext, EncPrefix) {
		return plaintext, nil
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, aesgcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := aesgcm.Seal(nil, nonce, []byte(plaintext), nil)
	combined := append(nonce, ciphertext...)
	return EncPrefix + base64.RawURLEncoding.EncodeToString(combined), nil
}

// ErrEncryptedDataMissingKey is returned by DecryptStoredSecret when the value
// carries the enc:v1: prefix but no AES key is available to decrypt it. This
// signals an operator misconfiguration (typically a rotated or unset
// SYSTEM_AES_KEY) and must propagate so the system fails loudly instead of
// silently using ciphertext as a credential.
var ErrEncryptedDataMissingKey = errors.New("encrypted data found but SYSTEM_AES_KEY is not set or has wrong length")

// DecryptAESGCM decrypts an AES-256-GCM encrypted string.
// If the string lacks the enc:v1: prefix, it's treated as legacy plaintext and returned as-is.
func DecryptAESGCM(encrypted string, key []byte) (string, error) {
	if encrypted == "" || key == nil {
		return encrypted, nil
	}
	if !strings.HasPrefix(encrypted, EncPrefix) {
		return encrypted, nil
	}

	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encrypted, EncPrefix))
	if err != nil {
		return "", err
	}
	if len(data) < 12 {
		return "", errors.New("invalid encrypted data: too short")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce, ciphertext := data[:aesgcm.NonceSize()], data[aesgcm.NonceSize():]
	plaintext, err := aesgcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// DecryptStoredSecret decrypts a value loaded from the database with strict
// error propagation. Use this from GORM Scan/AfterFind hooks for any field that
// stores AES-encrypted secrets (API keys, passwords, app secrets).
//
// Behaviour:
//   - empty input -> empty output, no error.
//   - no enc:v1: prefix -> returned as-is (legacy plaintext column), no error.
//   - has enc:v1: prefix and SYSTEM_AES_KEY is missing or wrong length ->
//     returns ErrEncryptedDataMissingKey. Callers MUST propagate this so the
//     load fails loudly instead of silently surfacing ciphertext as a credential.
//   - has enc:v1: prefix and key is set -> decrypts, returns any decryption
//     error verbatim (e.g. base64 decode failure, GCM auth tag mismatch from a
//     rotated key).
//
// The previous lenient pattern (`if decrypted, err := ...; err == nil { ... }`)
// hid the rotated-key case and caused the encrypted ciphertext to be sent
// upstream as the actual API key, surfacing as 401/403 from third-party
// vendors. Always prefer this helper over calling DecryptAESGCM directly when
// loading from the database.
func DecryptStoredSecret(encrypted string) (string, error) {
	if encrypted == "" {
		return "", nil
	}
	if !strings.HasPrefix(encrypted, EncPrefix) {
		return encrypted, nil
	}
	key := GetAESKey()
	if key == nil {
		return "", ErrEncryptedDataMissingKey
	}
	return DecryptAESGCM(encrypted, key)
}
