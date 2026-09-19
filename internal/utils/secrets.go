package utils

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"log"
)

// MarshalWithSecrets 把 v 序列化成 JSON，对 keys 指定的凭证字段统一加密后返回。
// 用于替代各配置类型 Value() 里"逐字段 if + EncryptAESGCM"的重复代码。
//
// 实现用 map[string]json.RawMessage 中转，保留非 string 字段（int64、嵌套对象）
// 的原始字节，避免 map[string]any 把 JSON number 转成 float64 造成精度丢失。
// 加密幂等：已带密文前缀（enc:v1:）的值不会被二次加密；空凭证跳过。
//
// 安全语义（严格模式）：凭证非空时，
//   - SYSTEM_AES_KEY 未配置 → 返回错误，拒绝把明文写入数据库；
//   - 加密失败 → 返回错误，绝不静默落明文。
//
// 只处理 v 的顶层 string 字段，嵌套 map 凭证（如 DataSourceConfig.Credentials、
// ParserEngineConfig.ExternalPluginConfigs）由各自已有的"整体 map 加密"负责，
// 调用方必须同样遵守"加密失败不落明文"。
func MarshalWithSecrets(v any, keys ...string) (driver.Value, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	key := GetAESKey()
	for _, k := range keys {
		rm, ok := m[k]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(rm, &s); err != nil || s == "" {
			continue
		}
		if key == nil {
			return nil, fmt.Errorf("refusing to persist plaintext credential %q: SYSTEM_AES_KEY is not configured", k)
		}
		enc, err := EncryptAESGCM(s, key)
		if err != nil {
			return nil, fmt.Errorf("encrypt credential %q: %w", k, err)
		}
		m[k], _ = json.Marshal(enc)
	}
	return json.Marshal(m)
}

// UnmarshalWithSecrets 反序列化并解密 keys 指定的凭证字段，是 MarshalWithSecrets
// 的对称实现。解密宽容：明文（无密文前缀）原样返回；坏密文置空并记日志，与现有
// Scan 的容错语义一致（单条坏凭证不阻断整个配置的加载）。
func UnmarshalWithSecrets(data []byte, v any, keys ...string) error {
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	for _, k := range keys {
		rm, ok := m[k]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(rm, &s); err != nil || s == "" {
			continue
		}
		if plain, ok := DecryptStoredSecretLenient(s); ok {
			m[k], _ = json.Marshal(plain)
		} else {
			log.Printf("[crypto] %s: decrypt failed (SYSTEM_AES_KEY missing/rotated?), treating as unconfigured", k)
			m[k] = json.RawMessage(`""`)
		}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}
