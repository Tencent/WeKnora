// Package-level note:
//
// SetRisk attaches risk metadata to a destructive cobra command via cobra
// annotations. The SetAgentHelp wrapper in agenthelp.go reads these
// annotations and prepends a "Risk: <action> (<level>)" line at the top
// of human help output.
//
// envelope.error.risk.action is emitted separately by the
// ConfirmDestructive callsite argument and does NOT read these annotations.
package cmdutil

import "github.com/spf13/cobra"

// RiskDestructive is the only level value currently emitted. The annotation
// key reserves "read" / "write" for future use.
const RiskDestructive = "destructive"

// SetRisk writes risk metadata to a cobra command's annotations.
// Idempotent: re-calling with the same action overwrites cleanly.
//
// nil-map guard: cobra.Command.Annotations is `map[string]string` and
// defaults to nil. Writing to a nil map panics, so we allocate first.
func SetRisk(cmd *cobra.Command, action string) {
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations["risk.level"] = RiskDestructive
	cmd.Annotations["risk.action"] = action
}

// GetRisk reads risk metadata. Returns (level, action, ok). ok=false when
// either the annotations map is nil or risk.action key is missing.
//
// Reading from a nil map is safe in Go (returns zero value); we still
// check map-existence explicitly for clarity.
func GetRisk(cmd *cobra.Command) (level, action string, ok bool) {
	if cmd.Annotations == nil {
		return "", "", false
	}
	action, ok = cmd.Annotations["risk.action"]
	if !ok {
		return "", "", false
	}
	level = cmd.Annotations["risk.level"]
	return level, action, true
}
