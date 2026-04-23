package vlm

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestConfigFromModel_RemoteDefaultsToOpenAI(t *testing.T) {
	m := &types.Model{
		ID:     "v1",
		Name:   "gpt-4o",
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL:       "https://api.example.com/v1",
			APIKey:        "sk",
			Provider:      "openai",
			ExtraConfig:   map[string]string{"x": "y"},
			CustomHeaders: map[string]string{"H": "v"},
		},
	}
	cfg := ConfigFromModel(m, "app", "secret")
	if cfg.InterfaceType != "openai" {
		t.Errorf("expected openai default for remote, got %q", cfg.InterfaceType)
	}
	if cfg.CustomHeaders["H"] != "v" {
		t.Errorf("CustomHeaders not propagated: %+v", cfg.CustomHeaders)
	}
	if cfg.Extra["x"] != "y" {
		t.Errorf("ExtraConfig not propagated as Extra: %+v", cfg.Extra)
	}
	if cfg.AppID != "app" || cfg.AppSecret != "secret" {
		t.Errorf("cloud creds mismatch: %+v", cfg)
	}
}

func TestConfigFromModel_LocalDefaultsToOllama(t *testing.T) {
	m := &types.Model{
		Name:   "qwen2-vl",
		Source: types.ModelSourceLocal,
	}
	cfg := ConfigFromModel(m, "", "")
	if cfg.InterfaceType != "ollama" {
		t.Errorf("expected ollama default for local, got %q", cfg.InterfaceType)
	}
}

func TestConfigFromModel_RespectsExplicitInterface(t *testing.T) {
	m := &types.Model{
		Name:   "qwen2-vl",
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			InterfaceType: "ollama",
		},
	}
	if got := ConfigFromModel(m, "", "").InterfaceType; got != "ollama" {
		t.Errorf("expected explicit interface to win, got %q", got)
	}
}
