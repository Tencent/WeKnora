package crypto

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
)

// Config 加密服务配置结构
type Config struct {
	// MasterKey 主密钥，用于派生加密密钥
	// 可以从环境变量或配置文件中读取
	MasterKey string `json:"master_key" yaml:"master_key" env:"CRYPTO_MASTER_KEY"`

	// Salt 盐值，用于密钥派生
	// 如果为空，将自动生成随机盐值
	Salt string `json:"salt" yaml:"salt" env:"CRYPTO_SALT"`

	// SaltLength 盐值长度（当Salt为空时使用）
	SaltLength int `json:"salt_length" yaml:"salt_length" env:"CRYPTO_SALT_LENGTH" default:"16"`

	// KeyDerivationIterations PBKDF2迭代次数
	KeyDerivationIterations int `json:"key_derivation_iterations" yaml:"key_derivation_iterations" env:"CRYPTO_ITERATIONS" default:"10000"`

	// KeyLength 派生密钥长度
	KeyLength int `json:"key_length" yaml:"key_length" env:"CRYPTO_KEY_LENGTH" default:"32"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		MasterKey:               "",
		Salt:                    "",
		SaltLength:              16,
		KeyDerivationIterations: 10000,
		KeyLength:               32,
	}
}

// LoadConfigFromEnv 从环境变量加载配置
func LoadConfigFromEnv() *Config {
	config := DefaultConfig()

	// 从环境变量读取主密钥
	if masterKey := os.Getenv("CRYPTO_MASTER_KEY"); masterKey != "" {
		config.MasterKey = masterKey
	}

	// 从环境变量读取盐值
	if salt := os.Getenv("CRYPTO_SALT"); salt != "" {
		config.Salt = salt
	}

	// 从环境变量读取盐值长度
	if saltLengthStr := os.Getenv("CRYPTO_SALT_LENGTH"); saltLengthStr != "" {
		if saltLength, err := strconv.Atoi(saltLengthStr); err == nil && saltLength >= 8 {
			config.SaltLength = saltLength
		}
	}

	// 从环境变量读取迭代次数
	if iterationsStr := os.Getenv("CRYPTO_ITERATIONS"); iterationsStr != "" {
		if iterations, err := strconv.Atoi(iterationsStr); err == nil && iterations > 0 {
			config.KeyDerivationIterations = iterations
		}
	}

	// 从环境变量读取密钥长度
	if keyLengthStr := os.Getenv("CRYPTO_KEY_LENGTH"); keyLengthStr != "" {
		if keyLength, err := strconv.Atoi(keyLengthStr); err == nil && keyLength >= 16 {
			config.KeyLength = keyLength
		}
	}

	return config
}

// Validate 验证配置是否有效
func (c *Config) Validate() error {
	if c.MasterKey == "" {
		return fmt.Errorf("master key cannot be empty")
	}

	if c.SaltLength < 8 {
		return fmt.Errorf("salt length must be at least 8 bytes")
	}

	if c.KeyDerivationIterations < 1000 {
		return fmt.Errorf("key derivation iterations must be at least 1000")
	}

	if c.KeyLength < 16 {
		return fmt.Errorf("key length must be at least 16 bytes")
	}

	return nil
}

// GetSaltBytes 获取盐值的字节数组形式
func (c *Config) GetSaltBytes() ([]byte, error) {
	if c.Salt != "" {
		// 如果配置了盐值，解码Base64格式
		saltBytes, err := base64.StdEncoding.DecodeString(c.Salt)
		if err != nil {
			return nil, fmt.Errorf("failed to decode salt from base64: %w", err)
		}
		return saltBytes, nil
	}

	// 如果没有配置盐值，生成随机盐值
	salt, err := GenerateRandomSalt(c.SaltLength)
	if err != nil {
		return nil, fmt.Errorf("failed to generate random salt: %w", err)
	}

	return salt, nil
}

// NewCryptoServiceFromConfig 从配置创建加密服务
func NewCryptoServiceFromConfig(config *Config) (*CryptoService, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	saltBytes, err := config.GetSaltBytes()
	if err != nil {
		return nil, fmt.Errorf("failed to get salt bytes: %w", err)
	}

	return NewCryptoService(config.MasterKey, saltBytes)
}

// ExampleConfigUsage 展示配置使用示例
func ExampleConfigUsage() {
	fmt.Println("=== 配置使用示例 ===")

	// 方法1: 手动创建配置
	manualConfig := &Config{
		MasterKey:               "my-secret-master-key",
		Salt:                    "", // 自动生成盐值
		SaltLength:              16,
		KeyDerivationIterations: 10000,
		KeyLength:               32,
	}

	cryptoService1, err := NewCryptoServiceFromConfig(manualConfig)
	if err != nil {
		fmt.Printf("手动配置创建失败: %v\n", err)
	} else {
		fmt.Println("手动配置创建成功")
		_ = cryptoService1
	}

	// 方法2: 从环境变量加载配置
	// 首先设置环境变量
	os.Setenv("CRYPTO_MASTER_KEY", "env-master-key")
	os.Setenv("CRYPTO_SALT", base64.StdEncoding.EncodeToString([]byte("env-salt-123456")))
	os.Setenv("CRYPTO_SALT_LENGTH", "16")
	os.Setenv("CRYPTO_ITERATIONS", "10000")
	os.Setenv("CRYPTO_KEY_LENGTH", "32")

	envConfig := LoadConfigFromEnv()
	cryptoService2, err := NewCryptoServiceFromConfig(envConfig)
	if err != nil {
		fmt.Printf("环境变量配置创建失败: %v\n", err)
	} else {
		fmt.Println("环境变量配置创建成功")
		_ = cryptoService2
	}

	// 清理环境变量
	os.Unsetenv("CRYPTO_MASTER_KEY")
	os.Unsetenv("CRYPTO_SALT")
	os.Unsetenv("CRYPTO_SALT_LENGTH")
	os.Unsetenv("CRYPTO_ITERATIONS")
	os.Unsetenv("CRYPTO_KEY_LENGTH")
}

// GenerateConfigExample 生成配置示例
func GenerateConfigExample() {
	fmt.Println("\n=== 配置生成示例 ===")

	// 生成随机主密钥
	masterKey, err := GenerateRandomKey(32)
	if err != nil {
		fmt.Printf("生成主密钥失败: %v\n", err)
		return
	}

	// 生成随机盐值
	salt, err := GenerateRandomSalt(16)
	if err != nil {
		fmt.Printf("生成盐值失败: %v\n", err)
		return
	}

	saltBase64 := base64.StdEncoding.EncodeToString(salt)

	fmt.Println("生成的配置示例:")
	fmt.Printf("CRYPTO_MASTER_KEY=%s\n", masterKey)
	fmt.Printf("CRYPTO_SALT=%s\n", saltBase64)
	fmt.Printf("CRYPTO_SALT_LENGTH=16\n")
	fmt.Printf("CRYPTO_ITERATIONS=10000\n")
	fmt.Printf("CRYPTO_KEY_LENGTH=32\n")

	fmt.Println("\nYAML格式配置示例:")
	fmt.Printf(`master_key: %s
salt: %s
salt_length: 16
key_derivation_iterations: 10000
key_length: 32
`, masterKey, saltBase64)

	fmt.Println("\nJSON格式配置示例:")
	fmt.Printf(`{
  "master_key": "%s",
  "salt": "%s",
  "salt_length": 16,
  "key_derivation_iterations": 10000,
  "key_length": 32
}
`, masterKey, saltBase64)
}
