// Package wechat implements the WeChat personal account IM adapter for WeKnora
// via the Tencent iLink Bot API (ilinkai.weixin.qq.com).
package wechat

import (
	"crypto/aes"
	"fmt"
)

// decryptAES128ECB decrypts data encrypted with AES-128-ECB (no padding removal).
// iLink media files are encrypted with a 16-byte AES key using ECB mode.
func decryptAES128ECB(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new aes cipher: %w", err)
	}

	bs := block.BlockSize()
	if len(ciphertext) == 0 || len(ciphertext)%bs != 0 {
		return nil, fmt.Errorf("ciphertext length %d is not a multiple of block size %d", len(ciphertext), bs)
	}

	plaintext := make([]byte, len(ciphertext))
	for i := 0; i < len(ciphertext); i += bs {
		block.Decrypt(plaintext[i:i+bs], ciphertext[i:i+bs])
	}

	// Remove PKCS#7 padding if present
	if len(plaintext) > 0 {
		padLen := int(plaintext[len(plaintext)-1])
		if padLen > 0 && padLen <= bs && padLen <= len(plaintext) {
			valid := true
			for i := 0; i < padLen; i++ {
				if plaintext[len(plaintext)-1-i] != byte(padLen) {
					valid = false
					break
				}
			}
			if valid {
				plaintext = plaintext[:len(plaintext)-padLen]
			}
		}
	}

	return plaintext, nil
}
