//go:build !linux

package sandbox

import (
	"fmt"
	"os"
)

// Main is only available on Linux.
func Main([]string) int {
	fmt.Fprintln(os.Stderr, "plugin-sandbox needs Linux")
	return 2
}

// Supported reports whether this system can run plugins in a sandbox.
func Supported() bool { return false }
