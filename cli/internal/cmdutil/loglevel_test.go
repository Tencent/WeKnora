package cmdutil

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

func TestResolveLogLevel(t *testing.T) {
	cases := []struct {
		name     string
		flagVal  string // "" means flag not set
		envVal   string // WEKNORA_LOG_LEVEL
		wantLvl  string
		wantWarn bool
	}{
		{"default", "", "", "error", false},
		{"flag wins over env", "debug", "info", "debug", false},
		{"env when no flag", "", "info", "info", false},
		{"invalid env falls through to default", "", "trace", "error", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEKNORA_LOG_LEVEL", tc.envVal)

			cmd := &cobra.Command{}
			AddLogLevelFlag(cmd)
			if tc.flagVal != "" {
				if err := cmd.ParseFlags([]string{"--log-level", tc.flagVal}); err != nil {
					t.Fatalf("ParseFlags: %v", err)
				}
			}

			var stderr bytes.Buffer
			got, warn := ResolveLogLevel(cmd, &stderr)
			if got != tc.wantLvl {
				t.Errorf("level=%q want %q", got, tc.wantLvl)
			}
			if warn != tc.wantWarn {
				t.Errorf("warn=%v want %v", warn, tc.wantWarn)
			}
		})
	}
}

func TestAddLogLevelFlag_RegistersPersistent(t *testing.T) {
	cmd := &cobra.Command{}
	AddLogLevelFlag(cmd)
	if f := cmd.PersistentFlags().Lookup("log-level"); f == nil {
		t.Error("--log-level must be registered as a persistent flag (global across all subcommands)")
	}
}
