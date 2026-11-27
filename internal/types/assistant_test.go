package types

import "testing"

func TestAssistantEffectiveAccessLevel(t *testing.T) {
	cases := []struct {
		name      string
		cfg       *AssistantConfig
		requested AssistantAccessLevel
		want      AssistantAccessLevel
	}{
		{"unset asks before changes", nil, "", AssistantAccessConfirm},
		{"unknown falls back to the default", nil, "root", AssistantAccessConfirm},
		{"no config caps auto_write", nil, AssistantAccessAutoWrite, AssistantAccessConfirm},
		{"lower than the ceiling is kept", nil, AssistantAccessReadOnly, AssistantAccessReadOnly},
		{
			"a raised ceiling allows auto_write",
			&AssistantConfig{MaxAccessLevel: AssistantAccessAutoWrite},
			AssistantAccessAutoWrite, AssistantAccessAutoWrite,
		},
		{
			"a read-only ceiling caps the default",
			&AssistantConfig{MaxAccessLevel: AssistantAccessReadOnly}, "", AssistantAccessReadOnly,
		},
		{
			"an invalid ceiling counts as the default",
			&AssistantConfig{MaxAccessLevel: "everything"}, AssistantAccessAutoWrite, AssistantAccessConfirm,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.EffectiveAccessLevel(tc.requested); got != tc.want {
				t.Fatalf("EffectiveAccessLevel(%q) = %q, want %q", tc.requested, got, tc.want)
			}
		})
	}
}

func TestAssistantMaxIterationsBounds(t *testing.T) {
	cases := []struct {
		name string
		cfg  *AssistantConfig
		want int
	}{
		{name: "nil uses the default", cfg: nil, want: DefaultAssistantMaxIterations},
		{name: "zero uses the default", cfg: &AssistantConfig{}, want: DefaultAssistantMaxIterations},
		{
			name: "negative uses the default", cfg: &AssistantConfig{MaxIterations: -1},
			want: DefaultAssistantMaxIterations,
		},
		{name: "a chosen value is kept", cfg: &AssistantConfig{MaxIterations: 12}, want: 12},
		{
			name: "an oversized value is bounded", cfg: &AssistantConfig{MaxIterations: 1000},
			want: MaxAssistantMaxIterations,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.EffectiveMaxIterations(); got != tc.want {
				t.Fatalf("EffectiveMaxIterations() = %d, want %d", got, tc.want)
			}
		})
	}

	cfg := &AssistantConfig{MaxIterations: 1000}
	cfg.Normalize()
	if cfg.MaxIterations != MaxAssistantMaxIterations {
		t.Fatalf("Normalize() left max_iterations=%d, want %d", cfg.MaxIterations, MaxAssistantMaxIterations)
	}
}
