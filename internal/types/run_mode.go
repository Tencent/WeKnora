package types

import "context"

// RunMode says whether anyone can answer the prompts an agent run raises.
type RunMode string

const (
	// RunModeAttended is a run whose client renders prompt cards and answers
	// them: the web chat and the assistant panel.
	RunModeAttended RunMode = "attended"
	// RunModeDeferred is a run nobody watches now whose owner can answer
	// later: a prompt suspends the run until it is answered.
	RunModeDeferred RunMode = "deferred"
	// RunModeUnattended is a run nobody can answer, such as an API, embed or
	// IM request: a prompt is refused at once.
	RunModeUnattended RunMode = "unattended"
)

// Normalize returns m, treating anything unknown, including empty, as
// unattended: a run is only answerable when its caller says so.
func (m RunMode) Normalize() RunMode {
	switch m {
	case RunModeAttended, RunModeDeferred:
		return m
	default:
		return RunModeUnattended
	}
}

// Attended reports whether a client can answer prompts now.
func (m RunMode) Attended() bool { return m.Normalize() == RunModeAttended }

// WithRunMode attaches the run's mode to ctx.
func WithRunMode(ctx context.Context, m RunMode) context.Context {
	return context.WithValue(ctx, RunModeContextKey, m.Normalize())
}

// RunModeFromContext returns the mode attached to ctx, unattended when none.
func RunModeFromContext(ctx context.Context) RunMode {
	if ctx == nil {
		return RunModeUnattended
	}
	m, _ := ctx.Value(RunModeContextKey).(RunMode)
	return m.Normalize()
}

// WithAccessLevel records the run's effective access level.
func WithAccessLevel(ctx context.Context, level AssistantAccessLevel) context.Context {
	return context.WithValue(ctx, AccessLevelContextKey, level)
}

// AccessLevelFromContext is the run's access level. A context that never
// set one gets AssistantAccessAutoWrite, the level of an agent with no
// configured ceiling.
func AccessLevelFromContext(ctx context.Context) AssistantAccessLevel {
	if level, ok := ctx.Value(AccessLevelContextKey).(AssistantAccessLevel); ok && level.Valid() {
		return level
	}
	return AssistantAccessAutoWrite
}
