//go:build !linux

package host

import (
	"errors"
	"os/exec"
)

type sandboxed struct {
	cmd     *exec.Cmd
	start   func() error
	release func()
}

func (p *process) sandbox(*exec.Cmd) (*sandboxed, error) {
	return nil, errors.New(envNetns + " needs Linux")
}
