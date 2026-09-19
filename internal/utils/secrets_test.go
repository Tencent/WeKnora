package utils

import (
	"os"
	"strings"
	"testing"
)

const secretsTestAESKey = "0123456789abcdef0123456789abcdef" // 32 bytes

type secretsTestValue struct {
	APIKey    string `json:"api_key"`
	AppSecret string `json:"app_secret"`
	BaseURL   string `json:"base_url"`
	BigID     int64  `json:"big_id"`
	Count     int    `json:"count"`
	Enabled   bool   `json:"enabled"`
}

func setSecretsTestAESKey(t *testing.T) {
	t.Helper()
	if err := os.Setenv("SYSTEM_AES_KEY", secretsTestAESKey); err != nil {
		t.Fatalf("set SYSTEM_AES_KEY: %v", err)
	}
}

func TestMarshalWithSecretsRoundTrip(t *testing.T) {
	setSecretsTestAESKey(t)
	v := secretsTestValue{
		APIKey:    "sk-secret-1",
		AppSecret: "app-secret-1",
		BaseURL:   "https://api.example.com",
		BigID:     9007199254740993, // > 2^53, would be corrupted by float64
		Count:     42,
		Enabled:   true,
	}
	val, err := MarshalWithSecrets(v, "api_key", "app_secret")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	raw := string(val.([]byte))
	if strings.Contains(raw, "sk-secret-1") || strings.Contains(raw, "app-secret-1") {
		t.Fatalf("plaintext secret leaked into persisted value: %s", raw)
	}

	var out secretsTestValue
	if err := UnmarshalWithSecrets(val.([]byte), &out, "api_key", "app_secret"); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.APIKey != "sk-secret-1" || out.AppSecret != "app-secret-1" {
		t.Fatalf("secret round-trip mismatch: %+v", out)
	}
	if out.BaseURL != "https://api.example.com" {
		t.Fatalf("non-secret field corrupted: %+v", out)
	}
	if out.BigID != 9007199254740993 {
		t.Fatalf("int64 precision lost: got %d", out.BigID)
	}
	if out.Count != 42 || !out.Enabled {
		t.Fatalf("scalar field corrupted: %+v", out)
	}
}

func TestUnmarshalWithSecretsAcceptsLegacyPlaintext(t *testing.T) {
	setSecretsTestAESKey(t)
	// 存量明文（无 enc:v1: 前缀）应原样返回，不做解密
	data := []byte(`{"api_key":"legacy-plain","base_url":"https://x"}`)
	var out secretsTestValue
	if err := UnmarshalWithSecrets(data, &out, "api_key"); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.APIKey != "legacy-plain" {
		t.Fatalf("legacy plaintext not preserved: %q", out.APIKey)
	}
}

func TestUnmarshalWithSecretsBlanksBrokenCiphertext(t *testing.T) {
	setSecretsTestAESKey(t)
	// 带密文前缀但解不开 → 置空，而不是报错阻断加载
	data := []byte(`{"api_key":"enc:v1:garbage","base_url":"https://x"}`)
	var out secretsTestValue
	if err := UnmarshalWithSecrets(data, &out, "api_key"); err != nil {
		t.Fatalf("unmarshal should not error on broken ciphertext: %v", err)
	}
	if out.APIKey != "" {
		t.Fatalf("broken ciphertext should be blanked, got %q", out.APIKey)
	}
}

func TestMarshalWithSecretsRejectsWhenNoKey(t *testing.T) {
	// 不设置 key：凭证非空必须拒绝保存，绝不静默落明文
	if err := os.Setenv("SYSTEM_AES_KEY", ""); err != nil {
		t.Fatal(err)
	}
	v := secretsTestValue{APIKey: "sk-should-not-persist"}
	if _, err := MarshalWithSecrets(v, "api_key"); err == nil {
		t.Fatal("persisting plaintext credential without SYSTEM_AES_KEY was accepted")
	}
	// 空凭证时无 key 不应报错（没有要保护的东西）
	empty := secretsTestValue{BaseURL: "https://x"}
	if _, err := MarshalWithSecrets(empty, "api_key"); err != nil {
		t.Fatalf("empty credentials rejected without key: %v", err)
	}
}
