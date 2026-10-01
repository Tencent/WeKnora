//go:build darwin

package localsandbox

import (
	"context"

	"github.com/Tencent/WeKnora/internal/localsandbox/core"
	"github.com/Tencent/WeKnora/internal/localsandbox/seatbelt"
	"github.com/Tencent/WeKnora/internal/logger"
)

// NewBackend returns the platform backend. The build tag on this file is the
// only place the OS is selected; no other code may branch on runtime.GOOS.
func NewBackend() (core.Backend, error) {
	logger.Infof(context.Background(), "[LocalSandbox] using seatbelt backend")
	return seatbelt.New()
}
