package rerank

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestConfigFromModel(t *testing.T) {
	m := &types.Model{
		ID:     "rr-1",
		Name:   "bge-reranker-v2-m3",
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL:       "https://api.example.com/v1",
			APIKey:        "sk-xxx",
			Provider:      "siliconflow",
			ExtraConfig:   map[string]string{"flag": "on"},
			CustomHeaders: map[string]string{"X-Gateway": "g"},
		},
	}
	cfg := ConfigFromModel(m, "app", "secret")
	if cfg == nil || cfg.ModelID != "rr-1" || cfg.ModelName != "bge-reranker-v2-m3" {
		t.Fatalf("identity mismatch: %+v", cfg)
	}
	if cfg.Provider != "siliconflow" || cfg.CustomHeaders["X-Gateway"] != "g" {
		t.Errorf("provider/headers not propagated: %+v", cfg)
	}
	if cfg.AppID != "app" || cfg.AppSecret != "secret" {
		t.Errorf("cloud creds mismatch: %+v", cfg)
	}
}
