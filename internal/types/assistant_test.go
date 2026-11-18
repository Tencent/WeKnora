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
