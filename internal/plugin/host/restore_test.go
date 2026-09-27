package host

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
)

// Every start puts the extracted package back first: a plugin woken after
// someone rewrote its entry and planted a file runs the package's entry,
// and the planted file is gone.
func TestStartsRestoreThePackage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process checks use signal 0")
	}
	fastTimings(t)
	l := install(t, "1.0.0", "")
	entry := filepath.Join("bin", runtime.GOOS+"-"+runtime.GOARCH, "echo")
	bin, err := os.ReadFile(filepath.Join(l.Dir, entry))
	if err != nil {
		t.Fatal(err)
	}
	p, err := pkg.Open(plugintest.Zip(t, map[string]string{
		"plugin.yaml": "schemaVersion: 1\nid: acme.echo\nversion: 1.0.0\napiVersion: weknora.plugin/v1\n" +
			"name: Echo\npublisher: { id: acme }\n" +
			"runtime: { type: host, kind: binary, entry: 'bin/{os}-{arch}/echo' }\n" +
			"contributes:\n  webSearch:\n    - { id: echo, name: Echo }\n",
		filepath.ToSlash(entry): string(bin),
	}))
	if err != nil {
		t.Fatal(err)
	}
	l.Package = p
	if _, err := pkg.Restore(p, l.Dir); err != nil {
		t.Fatal(err)
	}

	m := NewManager()
	m.SetIdleTimeout(300 * time.Millisecond)
	defer m.Close()
	if err := reconcile.Activate(context.Background(), m, l); err != nil {
		t.Fatal(err)
	}
	pid1, err := search(t, m, "pid")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the idle plugin to stop", func() bool { return state(m, "acme.echo") == StateIdle })
	waitFor(t, "the idle process to exit", func() bool { return exited(pid1) })

	planted := filepath.Join(l.Dir, "planted.sh")
	_ = os.WriteFile(planted, []byte("#!/bin/sh\n"), 0o755)
	_ = os.WriteFile(filepath.Join(l.Dir, entry), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	if got, err := search(t, m, "hello"); err != nil || got != "hello" {
		t.Fatalf("the woken plugin answered %q, %v", got, err)
	}
	if _, err := os.Stat(planted); !os.IsNotExist(err) {
		t.Fatalf("the planted file is still there: %v", err)
	}
}
