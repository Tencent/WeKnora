package pluginapi

import (
	"sync"
	"testing"
)

func TestConfigStorePutGet(t *testing.T) {
	s := NewConfigStore()
	if _, ok := s.Get("m1", "model-a"); ok {
		t.Fatal("expected miss before put")
	}

	s.PutFromValidate(map[string]any{
		"model_id":   "m1",
		"model_name": "model-a",
		"api_key":    "sk-a",
		"base_url":   "https://a.example.com/v1",
	})

	// 按 model_id 命中
	if c, ok := s.Get("m1", ""); !ok || c["api_key"] != "sk-a" {
		t.Fatalf("expected hit by id, got %+v ok=%v", c, ok)
	}
	// 按 model_name 命中
	if c, ok := s.Get("", "model-a"); !ok || c["base_url"] != "https://a.example.com/v1" {
		t.Fatalf("expected hit by name, got %+v ok=%v", c, ok)
	}
}

func TestConfigStoreFallbackToLast(t *testing.T) {
	s := NewConfigStore()
	s.PutFromValidate(map[string]any{"model_id": "m1", "model_name": "model-a", "api_key": "sk-a"})
	s.PutFromValidate(map[string]any{"model_id": "m2", "model_name": "model-b", "api_key": "sk-b"})

	// 未知 model_id/name 回退到最近一次
	if c, ok := s.Get("unknown", "unknown"); !ok || c["api_key"] != "sk-b" {
		t.Fatalf("expected last fallback, got %+v ok=%v", c, ok)
	}
}

func TestConfigStoreGetString(t *testing.T) {
	s := NewConfigStore()
	s.PutFromValidate(map[string]any{"model_id": "m1", "api_key": "sk-a", "base_url": "https://x/v1"})

	if v := s.GetString("m1", "", "api_key"); v != "sk-a" {
		t.Fatalf("expected sk-a, got %q", v)
	}
	// 缺失字段返回空串
	if v := s.GetString("m1", "", "missing"); v != "" {
		t.Fatalf("expected empty, got %q", v)
	}
	// 未知 model_id/name 回退到 last（同一 provider 共享配置）
	if v := s.GetString("nope", "nope", "api_key"); v != "sk-a" {
		t.Fatalf("expected last fallback, got %q", v)
	}
}

func TestConfigStoreConcurrent(t *testing.T) {
	s := NewConfigStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			s.PutFromValidate(map[string]any{"model_id": "m", "model_name": "model", "api_key": "sk"})
		}(i)
		go func() {
			defer wg.Done()
			_, _ = s.Get("m", "model")
			_ = s.GetString("m", "model", "api_key")
		}()
	}
	wg.Wait()
}
