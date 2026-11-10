package runtime

import "time"

// serverStartedAt is set once at process boot (see MarkServerStarted).
var serverStartedAt time.Time

// MarkServerStarted records the instant the WeKnora server process started.
// Call once from main() before heavy init so uptime reflects the full process
// lifetime, not merely when the HTTP listener binds.
func MarkServerStarted() {
	serverStartedAt = time.Now().UTC()
}

// ServerStartedAt returns the boot instant in UTC. Zero when MarkServerStarted
// was not called (e.g. some tests); callers should treat zero as unknown.
func ServerStartedAt() time.Time {
	return serverStartedAt
}

// ServerUptime returns elapsed time since MarkServerStarted. Zero when unset.
func ServerUptime() time.Duration {
	if serverStartedAt.IsZero() {
		return 0
	}
	return time.Since(serverStartedAt)
}
