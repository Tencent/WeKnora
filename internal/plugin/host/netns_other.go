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

// sandboxOS: network namespaces are Linux's.
const sandboxOS = false

func probeSandbox() error { return errors.New("the network sandbox needs Linux") }

func (p *process) sandbox(*exec.Cmd) (*sandboxed, error) {
	return nil, errors.New(envNetns + " needs Linux")
}
