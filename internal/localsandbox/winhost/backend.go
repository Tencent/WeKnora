//go:build windows

// Package winhost is the Windows sandbox backend. The intended isolation is a
// dedicated hidden account, a restricted token, per-root capability SIDs,
// firewall rules, and a job object for the process tree. This build reports
// Windows as unavailable rather than running unsandboxed.
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
