//go:build windows

package localsandbox

import (
	"github.com/Tencent/WeKnora/internal/localsandbox/core"
	"github.com/Tencent/WeKnora/internal/localsandbox/winhost"
)

func NewBackend() (core.Backend, error) { return winhost.New() }
