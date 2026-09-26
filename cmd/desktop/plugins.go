package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
)

// configureDesktopPlugins adapts the plugin runtime to a desktop install.
//
// Extracted packages go under ~/.weknora/data/plugins rather than the temp
// directory, which macOS purges while the app runs. And a Finder-launched
// app only has launchd's PATH, where python3 is at best Apple's stub that
// offers to install the developer tools; python plugins get an interpreter
// found in the usual places, or are left off.
func configureDesktopPlugins(home string) {
	if strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_CACHE_DIR")) == "" {
		dir := filepath.Join(home, ".weknora", "data", "plugins")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logger.Warnf(context.Background(), "Failed to create plugin cache %s: %v", dir, err)
		} else {
			_ = os.Setenv("WEKNORA_PLUGIN_CACHE_DIR", dir)
		}
	}
	if strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_PYTHON")) != "" ||
		strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_EMBEDDED_KINDS")) != "" {
		return
	}
	if python := desktopPython(home, fileIsExecutable, appleCLTInstalled); python != "" {
		_ = os.Setenv("WEKNORA_PLUGIN_PYTHON", python)
		return
	}
	logger.Infof(context.Background(), "No python3 found for python plugins; only binary plugins will run")
	_ = os.Setenv("WEKNORA_PLUGIN_EMBEDDED_KINDS", "binary")
}

// desktopPython finds a python3 a desktop app can run: on PATH, then where
// Homebrew, python.org and pyenv put it. On macOS /usr/bin/python3 counts
// only when the command line tools behind it are installed.
func desktopPython(home string, executable func(string) bool, cltInstalled func() bool) string {
	var candidates []string
	if p, err := exec.LookPath("python3"); err == nil {
		candidates = append(candidates, p)
	}
	if goruntime.GOOS == "darwin" {
		candidates = append(candidates,
			"/opt/homebrew/bin/python3",
			"/usr/local/bin/python3",
			"/Library/Frameworks/Python.framework/Versions/Current/bin/python3",
			filepath.Join(home, ".pyenv", "shims", "python3"),
			"/usr/bin/python3",
		)
	}
	for _, p := range candidates {
		if !executable(p) {
			continue
		}
		if goruntime.GOOS == "darwin" && p == "/usr/bin/python3" && !cltInstalled() {
			continue
		}
		return p
	}
	return ""
}

func fileIsExecutable(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// appleCLTInstalled reports whether /usr/bin/python3 is backed by the
// command line tools or Xcode rather than being the install stub.
func appleCLTInstalled() bool {
	return exec.Command("/usr/bin/xcode-select", "-p").Run() == nil
}
