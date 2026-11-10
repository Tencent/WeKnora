//go:build windows

package main

import (
	"os"
	"syscall"
)

var shutdownSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM}
