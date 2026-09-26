//go:build !linux

package host

// applyLimits is Linux only: elsewhere plugins run uncapped (desktop, dev).
func applyLimits(int, string, limits) func() { return func() {} }
