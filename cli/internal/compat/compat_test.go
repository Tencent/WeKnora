package compat_test

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/compat"
)

func TestCompat(t *testing.T) {
	tests := []struct {
		name        string
		serverVer   string
		cliVer      string
		wantLevel   compat.Level
		wantHintHas string
	}{
		{"equal", "1.2.3", "1.2.3", compat.OK, ""},
		{"client minor lower", "1.5.0", "1.2.0", compat.OK, ""},
		{"client minor higher", "1.2.0", "1.5.0", compat.SoftWarn, "server is older"},
		{"different major", "1.9.9", "2.0.0", compat.HardError, "incompatible"},
		{"client unset", "1.2.3", "(unknown)", compat.OK, ""}, // dev build; no warning
		{"server unset", "", "1.2.3", compat.OK, ""},
		{"malformed server", "garbage", "1.2.3", compat.OK, ""}, // fail-open

		// "v" prefix tolerance (git describe + Tencent tag convention)
		{"v-prefix both", "v1.2.0", "v1.5.0", compat.SoftWarn, "server is older"},
		{"v-prefix server only", "v1.9.9", "2.0.0", compat.HardError, "incompatible"},
		{"v-prefix cli only", "1.9.9", "v2.0.0", compat.HardError, "incompatible"},

		// Hint must be empty when level == OK (no false positives)
		{"OK hint empty", "1.2.3", "1.2.3", compat.OK, ""},
	}
	for _, tt := range tests {
		gotLevel, gotHint := compat.Compat(tt.serverVer, tt.cliVer)
		if gotLevel != tt.wantLevel {
			t.Errorf("%s: Level = %v, want %v", tt.name, gotLevel, tt.wantLevel)
		}
		if tt.wantHintHas != "" && !strings.Contains(gotHint, tt.wantHintHas) {
			t.Errorf("%s: hint = %q, should contain %q", tt.name, gotHint, tt.wantHintHas)
		}
		if tt.wantLevel == compat.OK && gotHint != "" {
			t.Errorf("%s: OK level should have empty hint, got %q", tt.name, gotHint)
		}
	}
}
