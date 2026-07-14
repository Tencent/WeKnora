package runtime

import (
	"os"
	"strings"
)

// AppRole is the deployment role this process plays. It only decides which
// in-process background components start; it never affects HTTP API wiring —
// the HTTP server listens in every role.
//
// The role comes from the APP_ROLE environment variable. Empty or unrecognized
// values normalize to RoleAll, so existing single-process deployments, local
// development, and the desktop build are unaffected.
type AppRole string

const (
	// RoleAll runs everything in one process — the historical default behavior.
	RoleAll AppRole = "all"
	// RoleAPI is the runtime side: HTTP API + enqueue client + DataSourceScheduler,
	// with no asynq worker.
	RoleAPI AppRole = "api"
	// RoleWorker is the offline side: asynq worker + AuditLogRetention, with no
	// scheduler.
	RoleWorker AppRole = "worker"
)

// ResolveAppRole reads and normalizes the APP_ROLE environment variable.
// It is case-insensitive and trims surrounding whitespace; empty or
// unrecognized values fall back to RoleAll.
func ResolveAppRole() AppRole {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APP_ROLE"))) {
	case string(RoleAPI):
		return RoleAPI
	case string(RoleWorker):
		return RoleWorker
	default:
		return RoleAll
	}
}

// RunsWorker reports whether this role starts the asynq worker and offline
// maintenance tasks (audit retention).
func (r AppRole) RunsWorker() bool {
	return r == RoleAll || r == RoleWorker
}

// RunsScheduler reports whether this role starts the DataSourceScheduler
// (periodic enqueue — producer semantics).
func (r AppRole) RunsScheduler() bool {
	return r == RoleAll || r == RoleAPI
}
