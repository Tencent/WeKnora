package policy

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// String names v for logs.
func (v Verdict) String() string {
	switch v {
	case Allow:
		return "allow"
	case Ask:
		return "ask"
	case Deny:
		return "deny"
	default:
		return "unknown"
	}
}

// Record logs a decision on anything but a read, so what an agent changed
// without asking, and on whose behalf, can be traced later. key names the
// action, e.g. an operation ID or "mcp:<service>/<tool>".
func Record(ctx context.Context, key string, a Action, r Rules, v Verdict) {
	if a.Risk == RiskRead {
		return
	}
	fields := logger.Fields{
		"agent_action":  key,
		"agent_risk":    string(a.Risk),
		"agent_always":  a.Always,
		"agent_level":   string(r.Level),
		"agent_mode":    string(types.RunModeFromContext(ctx)),
		"agent_verdict": v.String(),
	}
	if via := types.ActingViaFromContext(ctx); via != "" {
		fields["agent_acting_via"] = via
	}
	logger.GetLogger(ctx).WithFields(fields).Info("agent policy decision")
}
