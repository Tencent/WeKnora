package main

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
)

func TestDesktopPythonSkipsTheAppleStub(t *testing.T) {
	if goruntime.GOOS != "darwin" {
		t.Skip("the stub is a macOS matter")
	}
	t.Setenv("PATH", "/nonexistent")
	only := func(paths ...string) func(string) bool {
		return func(p string) bool {
			for _, want := range paths {
				if p == want {
					return true
				}
			}
			return false
		}
	}
	if got := desktopPython("/Users/x", only("/usr/bin/python3"), func() bool { return false }); got != "" {
		t.Fatalf("picked the install stub: %q", got)
	}
	got := desktopPython("/Users/x", only("/usr/bin/python3"), func() bool { return true })
	if got != "/usr/bin/python3" {
		t.Fatalf("with the command line tools = %q", got)
	}
	got = desktopPython("/Users/x", only("/usr/bin/python3", "/opt/homebrew/bin/python3"), func() bool { return true })
	if got != "/opt/homebrew/bin/python3" {
		t.Fatalf("Homebrew should win over the system python: %q", got)
	}
}

func TestConfigureDesktopPluginsKeepsExplicitSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WEKNORA_PLUGIN_CACHE_DIR", "")
	t.Setenv("WEKNORA_PLUGIN_PYTHON", "/custom/python3")
	t.Setenv("WEKNORA_PLUGIN_EMBEDDED_KINDS", "")
	configureDesktopPlugins(home)
	if got := os.Getenv("WEKNORA_PLUGIN_CACHE_DIR"); got != filepath.Join(home, ".weknora", "data", "plugins") {
		t.Fatalf("cache dir = %q", got)
	}
	if got := os.Getenv("WEKNORA_PLUGIN_PYTHON"); got != "/custom/python3" {
		t.Fatalf("an explicit interpreter was replaced: %q", got)
	}
	if got := os.Getenv("WEKNORA_PLUGIN_EMBEDDED_KINDS"); got != "" {
		t.Fatalf("kinds changed despite an explicit interpreter: %q", got)
	}
}
