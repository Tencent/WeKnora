package chat

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/provider"
)

func stdCaps() provider.ThinkingCaps {
	return provider.ThinkingCaps{
		Supported:       true,
		CanDisable:      true,
		SupportedLevels: []provider.Level{provider.LevelLow, provider.LevelMedium, provider.LevelHigh},
		DefaultLevel:    provider.LevelMedium,
	}
}

func TestResolveThinkingLevel(t *testing.T) {
	tests := []struct {
		name           string
		callLevel      string
		modelLevel     string
		selectedLevels []string
		caps           provider.ThinkingCaps
		want           string
	}{
		{
			name:       "call level wins over model and provider",
			callLevel:  "high",
			modelLevel: "low",
			caps:       stdCaps(),
			want:       "high",
		},
		{
			name:       "model level used when call level empty",
			callLevel:  "",
			modelLevel: "low",
			caps:       stdCaps(),
			want:       "low",
		},
		{
			name:       "provider default used when call and model empty",
			callLevel:  "",
			modelLevel: "",
			caps:       stdCaps(),
			want:       "medium",
		},
		{
			name:       "no tier yields a level -> empty (emit nothing)",
			callLevel:  "",
			modelLevel: "",
			caps:       provider.ThinkingCaps{Supported: true}, // no SupportedLevels, no DefaultLevel
			want:       "",
		},
		{
			name:       "call level outside provider SupportedLevels falls through to model",
			callLevel:  "max", // not in {low,medium,high}
			modelLevel: "low",
			caps:       stdCaps(),
			want:       "low",
		},
		{
			name:           "model level outside user SelectedLevels falls through to provider default",
			callLevel:      "",
			modelLevel:     "high",
			selectedLevels: []string{"low", "medium"}, // user constrained the set; high excluded
			caps:           stdCaps(),
			want:           "medium",
		},
		{
			name:           "session switched model: stored call level unsupported -> provider default",
			callLevel:      "xhigh", // level saved on the previous model
			modelLevel:     "",
			selectedLevels: []string{"low", "medium", "high"}, // new model's set; xhigh not in it
			caps:           stdCaps(),
			want:           "medium", // falls back to the new model's provider default
		},
		{
			name:       "thinking unsupported -> always empty regardless of levels",
			callLevel:  "high",
			modelLevel: "high",
			caps:       provider.ThinkingCaps{Supported: false, DefaultLevel: provider.LevelHigh},
			want:       "",
		},
		{
			name:           "provider default itself outside SelectedLevels -> empty",
			callLevel:      "",
			modelLevel:     "",
			selectedLevels: []string{"high"},
			caps: provider.ThinkingCaps{
				Supported:       true,
				SupportedLevels: []provider.Level{provider.LevelLow, provider.LevelMedium, provider.LevelHigh},
				DefaultLevel:    provider.LevelMedium,
			},
			want: "", // medium not in selected {high}; no tier usable
		},
		{
			name:       "forced-thinking model with CanDisable=false still resolves a level",
			callLevel:  "",
			modelLevel: "high",
			caps: provider.ThinkingCaps{
				Supported: true, CanDisable: false,
				SupportedLevels: []provider.Level{provider.LevelHigh}, DefaultLevel: provider.LevelHigh,
			},
			want: "high",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveThinkingLevel(tt.callLevel, tt.modelLevel, tt.selectedLevels, tt.caps)
			if got != tt.want {
				t.Errorf("ResolveThinkingLevel(%q, %q, %v) = %q, want %q",
					tt.callLevel, tt.modelLevel, tt.selectedLevels, got, tt.want)
			}
		})
	}
}
