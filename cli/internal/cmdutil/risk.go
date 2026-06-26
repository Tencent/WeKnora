// Package-level note:
//
// SetRisk / SetWriteRisk attach risk metadata to a cobra command via cobra
// annotations. The SetAgentHelp wrapper in agenthelp.go reads these
// annotations and prepends a "Risk: <action> (<level>)" line at the top
// of human help output. Destructive ops use SetRisk ("destructive");
// reversible update commands use SetWriteRisk ("write"). "read" is reserved.
//
// envelope.error.risk.action is emitted separately by the
// ConfirmDestructive / ConfirmWrite callsite argument and does NOT read
// these annotations.
package cmdutil

import "github.com/spf13/cobra"

// Risk levels emitted in the annotation / envelope:
//   - RiskDestructive: irreversible ops (delete, kb init clobber).
//   - RiskWrite: reversible metadata edits (kb / agent / doc update).
//
// "read" remains reserved (read-only commands carry no risk annotation).
const (
	RiskDestructive = "destructive"
	RiskWrite       = "write"
)

// SetRisk writes destructive risk metadata to a cobra command's annotations.
// Idempotent: re-calling with the same action overwrites cleanly.
//
// nil-map guard: cobra.Command.Annotations is `map[string]string` and
// defaults to nil. Writing to a nil map panics, so we allocate first.
func SetRisk(cmd *cobra.Command, action string) {
	setRisk(cmd, RiskDestructive, action)
}

// SetWriteRisk mirrors SetRisk but tags the command at the "write" level —
// used by update commands (kb / agent / doc update), which are reversible
// metadata edits rather than irreversible destructive ops. They remain
// confirmation-gated; only the level label differs.
func SetWriteRisk(cmd *cobra.Command, action string) {
	setRisk(cmd, RiskWrite, action)
}

// setRisk is the shared writer behind SetRisk / SetWriteRisk.
func setRisk(cmd *cobra.Command, level, action string) {
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations["risk.level"] = level
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
