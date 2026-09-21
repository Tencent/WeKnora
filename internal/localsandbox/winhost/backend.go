//go:build windows

// Package winhost will implement the sandbox backend for Windows using the
// Route B design: a dedicated hidden account, a restricted token, per-root
// capability SIDs, firewall rules and a job object for the process tree.
// See spec section 6.2. Phase 3 splits this package into account.go, token.go,
// acl.go, firewall.go, install.go, spawn.go and teardown.go plus an elevated
// helper executable.
package winhost

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/localsandbox/core"
	"github.com/Tencent/WeKnora/internal/logger"
)

// New reports Windows as unavailable rather than running unsandboxed.
func New() (core.Backend, error) {
	err := fmt.Errorf("%w: windows backend is not implemented yet", core.ErrUnsupportedPlatform)
	logger.Errorf(context.Background(), "[LocalSandbox] %v", err)
	return nil, err
}
