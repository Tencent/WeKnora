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
