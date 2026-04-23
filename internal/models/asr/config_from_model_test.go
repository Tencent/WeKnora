package asr

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestConfigFromModel(t *testing.T) {
	m := &types.Model{
		ID:     "asr-1",
		Name:   "whisper-1",
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL:       "https://api.example.com/v1",
			APIKey:        "sk",
			CustomHeaders: map[string]string{"X": "y"},
		},
	}
	cfg := ConfigFromModel(m)
	if cfg == nil || cfg.ModelID != "asr-1" || cfg.ModelName != "whisper-1" {
		t.Fatalf("identity mismatch: %+v", cfg)
	}
	if cfg.BaseURL != "https://api.example.com/v1" || cfg.APIKey != "sk" {
		t.Errorf("connection fields mismatch: %+v", cfg)
	}
	if cfg.CustomHeaders["X"] != "y" {
		t.Errorf("CustomHeaders not propagated: %+v", cfg.CustomHeaders)
	}
}

func TestConfigFromModel_Nil(t *testing.T) {
	if got := ConfigFromModel(nil); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}
