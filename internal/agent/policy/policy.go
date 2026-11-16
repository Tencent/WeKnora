// Package policy decides whether an agent may run an action without asking
// the user. It is the only place that maps an action's risk and the run's
// access level to allow, ask or deny; tools ask it instead of comparing
// levels themselves. What the agent can reach at all (registered tools,
// workspace roles, writable knowledge bases) is decided elsewhere and is not
// widened by anything here.
//
// See docs/design/agent-permission-model.md.
package policy

import "github.com/Tencent/WeKnora/internal/types"

// Risk classifies an action by its effect on user data.
type Risk string

const (
	// RiskRead has no side effects.
	RiskRead Risk = "read"
	// RiskProbe changes no data but does real work to answer: it connects to
	// an external service or runs a retrieval.
	RiskProbe Risk = "probe"
	// RiskWrite changes state in a recoverable way (reparse, status change).
	RiskWrite Risk = "write"
	// RiskDestructive removes data or cannot be undone.
	RiskDestructive Risk = "destructive"
)

// Valid reports whether r is a known risk.
func (r Risk) Valid() bool {
	return r == RiskRead || r == RiskProbe || r == RiskWrite || r == RiskDestructive
}

// Mutates reports whether the action changes data.
func (r Risk) Mutates() bool { return r == RiskWrite || r == RiskDestructive }

var riskRank = map[Risk]int{RiskRead: 1, RiskProbe: 2, RiskWrite: 3, RiskDestructive: 4}

// Above reports whether r is a higher risk than other. Every known risk is
// above an unknown or empty one.
func (r Risk) Above(other Risk) bool { return riskRank[r] > riskRank[other] }

// Action is one call the agent wants to make.
type Action struct {
	Risk Risk
	// Always asks every time, whatever the level: a skill installation, or an
	// MCP tool the workspace marked as requiring approval.
	Always bool
}

// Rules are what the run allows.
type Rules struct {
	// Level is the run's effective access level.
	Level types.AssistantAccessLevel
}

// Verdict is the outcome of Decide.
type Verdict int

const (
	// Allow runs the action without asking.
	Allow Verdict = iota
	// Ask runs the action only after the user approves it.
	Ask
	// Deny refuses the action: the run may not make it even with approval.
	Deny
)

// Decide returns whether a may run under r. Mutations a read-only run must
// not make are kept from the model by Offered; one that still arrives is
// denied.
func Decide(a Action, r Rules) Verdict {
	if !Offered(r.Level, a.Risk) {
		return Deny
	}
	if a.Always {
		return Ask
	}
	switch a.Risk {
	case RiskRead, RiskProbe:
		return Allow
	case RiskWrite:
		if r.Level == types.AssistantAccessAutoWrite {
			return Allow
		}
		return Ask
	default:
		return Ask
	}
}

// Grantable reports whether approving a during a run may be remembered for
// later calls of the same kind: only recoverable changes the workspace does
// not require approving every time.
func Grantable(a Action) bool { return a.Risk == RiskWrite && !a.Always }

// Pregrantable reports whether a standing grant, approved ahead of time
// for a specific resource, may cover a. Unlike Grantable this includes
// destructive actions, which only an automation's settings may pre-approve;
// Always still asks every time.
func Pregrantable(a Action) bool {
	return (a.Risk == RiskWrite || a.Risk == RiskDestructive) && !a.Always
}

// Covers reports whether a grant approved for risk granted applies to a call
// of risk r: a grant never covers a riskier call than it was approved for.
func Covers(granted, r Risk) bool { return r.Valid() && !r.Above(granted) }

// Offered reports whether an action of risk should be offered to the model
// at all under level. A read-only run is not shown changes it cannot make,
// nor anything whose risk is unknown.
func Offered(level types.AssistantAccessLevel, risk Risk) bool {
	return level != types.AssistantAccessReadOnly || risk == RiskRead || risk == RiskProbe
}
