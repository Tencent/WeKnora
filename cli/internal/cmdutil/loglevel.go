package cmdutil

import (
	"io"
	"os"

	"github.com/spf13/cobra"
)

// validLogLevels is the canonical set of accepted log levels
// (debug|info|warn|error). Map gives O(1) validation.
var validLogLevels = map[string]bool{
	"error": true,
	"warn":  true,
	"info":  true,
	"debug": true,
}

// IsValidLogLevel reports whether v is one of the accepted log levels.
// Exported so factory-level validation can give a strict error on an
// explicit --log-level invalid value (env values stay silent-fallthrough).
func IsValidLogLevel(v string) bool { return validLogLevels[v] }

// LogLevelDefault is the resolved level when nothing is configured: only
// emit error-level logs (minimal noise for non-debug usage).
const LogLevelDefault = "error"

// AddLogLevelFlag registers --log-level as a persistent flag on cmd
// (typically the root command). Unlike --format (per-command, Method D),
// --log-level is uniformly applied across all SDK calls so it lives on root.
func AddLogLevelFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().String("log-level", "", "Log verbosity: error | warn | info | debug (env: WEKNORA_LOG_LEVEL)")
}

// ResolveLogLevel resolves the effective log level using priority (high → low):
//  1. --log-level flag (if explicitly set, even from a parent command's persistent flagset)
//  2. WEKNORA_LOG_LEVEL env (if set to a valid value)
//  3. Default "error"
//
// Invalid env values fall through to the next priority (no error — env is
// best-effort). The stderr writer is retained in the signature for symmetry
// with ApplyLogLevel callers but is unused at the OS level.
func ResolveLogLevel(cmd *cobra.Command, _ io.Writer) (string, bool) {
	// Priority 1: explicit --log-level flag.
	if cmd != nil {
		if f := cmd.Flags().Lookup("log-level"); f != nil && f.Changed {
			v := f.Value.String()
			if validLogLevels[v] {
				return v, false
			}
		}
	}

	// Priority 2: WEKNORA_LOG_LEVEL env (silently skip invalid values).
	if v := os.Getenv("WEKNORA_LOG_LEVEL"); v != "" && validLogLevels[v] {
		return v, false
	}

	// Priority 3: default.
	return LogLevelDefault, false
}
