package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/pbkdf2"
)

// CryptoService 提供密钥加密解密服务
type CryptoService struct {
	masterKey []byte
	salt      []byte
}

// NewCryptoService 创建新的加密服务实例
// masterKey: 主密钥，用于派生加密密钥
// salt: 盐值，用于密钥派生，如果为空则生成随机盐
func NewCryptoService(masterKey string, salt []byte) (*CryptoService, error) {
	if masterKey == "" {
		return nil, errors.New("master key cannot be empty")
	}

	// 如果未提供盐值，生成随机盐
	if salt == nil || len(salt) == 0 {
		salt = make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return nil, fmt.Errorf("failed to generate salt: %w", err)
		}
	}

	return &CryptoService{
		masterKey: []byte(masterKey),
		salt:      salt,
	}, nil
}

// deriveKey 使用PBKDF2派生加密密钥
func (cs *CryptoService) deriveKey() []byte {
	return pbkdf2.Key(cs.masterKey, cs.salt, 10000, 32, sha256.New)
}

// Encrypt 加密数据
func (cs *CryptoService) Encrypt(plaintext []byte) (string, error) {
	if len(plaintext) == 0 {
		return "", errors.New("plaintext cannot be empty")
	}

	key := cs.deriveKey()

	// 创建AES块密码
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// 生成随机IV
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return "", fmt.Errorf("failed to generate IV: %w", err)
	}

	// 使用CTR模式加密
	stream := cipher.NewCTR(block, iv)
	ciphertext := make([]byte, len(plaintext))
	stream.XORKeyStream(ciphertext, plaintext)

	// 组合IV和密文
	result := make([]byte, len(iv)+len(ciphertext))
	copy(result[:aes.BlockSize], iv)
	copy(result[aes.BlockSize:], ciphertext)

	// 返回Base64编码的结果
	return base64.StdEncoding.EncodeToString(result), nil
}

// Decrypt 解密数据
func (cs *CryptoService) Decrypt(encryptedData string) ([]byte, error) {
	if encryptedData == "" {
		return nil, errors.New("encrypted data cannot be empty")
	}

	// 解码Base64数据
	data, err := base64.StdEncoding.DecodeString(encryptedData)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	if len(data) < aes.BlockSize {
		return nil, errors.New("encrypted data too short")
	}

	key := cs.deriveKey()

	// 创建AES块密码
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// 提取IV和密文
	iv := data[:aes.BlockSize]
	ciphertext := data[aes.BlockSize:]

	// 使用CTR模式解密
	stream := cipher.NewCTR(block, iv)
	plaintext := make([]byte, len(ciphertext))
	stream.XORKeyStream(plaintext, ciphertext)

	return plaintext, nil
}

// EncryptString 加密字符串
func (cs *CryptoService) EncryptString(plaintext string) (string, error) {
	return cs.Encrypt([]byte(plaintext))
}

// DecryptString 解密字符串
func (cs *CryptoService) DecryptString(encryptedData string) (string, error) {
	plaintext, err := cs.Decrypt(encryptedData)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// GetSalt 获取当前使用的盐值
func (cs *CryptoService) GetSalt() []byte {
	return cs.salt
}

// GenerateRandomKey 生成随机密钥
func GenerateRandomKey(length int) (string, error) {
	if length < 16 {
		return "", errors.New("key length must be at least 16 bytes")
	}

	key := make([]byte, length)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("failed to generate random key: %w", err)
	}

	return base64.StdEncoding.EncodeToString(key), nil
}

// GenerateRandomSalt 生成随机盐值
func GenerateRandomSalt(length int) ([]byte, error) {
	if length < 8 {
		return nil, errors.New("salt length must be at least 8 bytes")
	}

	salt := make([]byte, length)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("failed to generate random salt: %w", err)
	}

	return salt, nil
}
