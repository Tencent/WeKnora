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

// TestConfigFromModel_MaxOutputTokens pins how the model row's declared output
// limit reaches the VLM config: a pinned spec override states the provider
// fact more specifically than the row field, and anything unknown stays 0 so
// the OCR budget keeps its built-in fallback.
func TestConfigFromModel_MaxOutputTokens(t *testing.T) {
	cases := []struct {
		name string
		m    *types.Model
		want int
	}{
		{
			name: "row field wins when no spec override",
			m: &types.Model{
				Parameters: types.ModelParameters{MaxOutputTokens: 384000},
			},
			want: 384000,
		},
		{
			name: "spec override wins over the row field",
			m: &types.Model{
				Parameters: types.ModelParameters{
					MaxOutputTokens: 384000,
					Spec:            &types.ModelSpecOverride{MaxOutputTokens: 8192},
				},
			},
			want: 8192,
		},
		{
			name: "zero when neither declares one",
			m:    &types.Model{},
			want: 0,
		},
		{
			name: "non-positive spec override is ignored",
			m: &types.Model{
				Parameters: types.ModelParameters{
					MaxOutputTokens: 8192,
					Spec:            &types.ModelSpecOverride{MaxOutputTokens: -1},
				},
			},
			want: 8192,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ConfigFromModel(tc.m, "", "")
			if cfg.MaxOutputTokens != tc.want {
				t.Errorf("MaxOutputTokens = %d, want %d", cfg.MaxOutputTokens, tc.want)
			}
		})
	}
}
