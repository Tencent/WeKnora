package host

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// pythonEcho is the echo plugin written with the Python SDK.
const pythonEcho = `import os
from weknora_plugin import Plugin, SearchResult

plugin = Plugin(os.environ["WEKNORA_PLUGIN_ID"], os.environ["WEKNORA_PLUGIN_VERSION"])


@plugin.web_search("echo")
def search(call, q):
    if q.query == "env":
        return [SearchResult(title=",".join(sorted(os.environ)), url="env")]
    if q.query == "crash":
        os._exit(3)
    if q.query == "pid":
        return [SearchResult(title=str(os.getpid()), url="pid")]
    return [SearchResult(title=q.query, url="echo")]


if __name__ == "__main__":
    plugin.serve()
`

// installPython lays out an extracted python package: main.py and the SDK
// vendored under vendor/, as a plugin author ships it.
func installPython(t *testing.T) *reconcile.Loaded {
	t.Helper()
	if _, err := exec.LookPath(PythonCommand()); err != nil {
		t.Skipf("%s is not installed", PythonCommand())
	}
	dir := t.TempDir()
	sdk := filepath.Join("..", "..", "..", "pluginsdk", "python", "src", "weknora_plugin")
	err := filepath.WalkDir(sdk, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".py" {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		target := filepath.Join(dir, "vendor", "weknora_plugin", filepath.Base(p))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte(pythonEcho), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, ID: "acme.echo", Version: "1.0.0", APIVersion: pluginapi.APIVersion,
		Name: manifest.Text("Echo", nil), Publisher: manifest.Publisher{ID: "acme"},
		Runtime: manifest.Runtime{Type: manifest.RuntimeHost, Kind: KindPython, Entry: "main.py"},
		Contributes: manifest.Contributions{
			manifest.PointWebSearch: {{ID: "echo", Name: manifest.Text("Echo", nil)}},
		},
	}
	return &reconcile.Loaded{Manifest: m, Dir: dir}
}

func TestHostRunsAPythonPlugin(t *testing.T) {
	fastTimings(t)
	t.Setenv("DB_PASSWORD", "must-not-leak")
	ctx := context.Background()
	m := NewManager()
	rep := &reports{}
	m.SetReporter(rep)
	defer m.Close()

	l := installPython(t)
	if err := m.Activate(ctx, l); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if got, err := search(t, m, "hello"); err != nil || got != "hello" {
		t.Fatalf("search = %q, %v", got, err)
	}
	env, err := search(t, m, "env")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(env, "DB_PASSWORD") || !strings.Contains(env, "PYTHONPATH") ||
		!strings.Contains(env, pluginapi.EnvToken) {
		t.Fatalf("plugin environment = %s", env)
	}

	pid1, _ := search(t, m, "pid")
	_, _ = search(t, m, "crash")
	waitFor(t, "restart", func() bool {
		pid, err := search(t, m, "pid")
		return err == nil && pid != pid1
	})
	if err := m.Deactivate(ctx, "acme.echo"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(l.Dir); len(entries) != 2 {
		t.Fatalf("the plugin wrote into its package: %v", entries)
	}
}

func TestEntryName(t *testing.T) {
	py := manifest.Runtime{Kind: KindPython, Entry: "main.py"}
	if got := EntryName(py); got != "main.py" {
		t.Fatalf("python entry = %s", got)
	}
	bin := manifest.Runtime{Kind: KindBinary, Entry: "bin/{os}-{arch}/x"}
	if got := EntryName(bin); strings.Contains(got, "{") {
		t.Fatalf("binary entry = %s", got)
	}
	if !Supported(KindPython) || Supported("node") {
		t.Fatal("this host runs binaries and python")
	}
}

func TestKindsDecideWhereAPluginRuns(t *testing.T) {
	ctx := context.Background()
	l := install(t, "1.0.0", "")

	embedded := NewManager()
	embedded.SetKinds([]string{KindPython})
	defer embedded.Close()
	if err := embedded.Activate(ctx, l); err != nil || embedded.Local("acme.echo") {
		t.Fatalf("an app node hands other kinds on: %v", err)
	}

	standalone := NewStandaloneManager([]string{KindPython})
	defer standalone.Close()
	if err := standalone.Activate(ctx, l); err == nil || !strings.Contains(err.Error(), "does not run binary") {
		t.Fatalf("a standalone host refuses other kinds, got %v", err)
	}

	t.Setenv("WEKNORA_PLUGIN_EMBEDDED_KINDS", "none")
	if got := KindsFromEnv("WEKNORA_PLUGIN_EMBEDDED_KINDS"); len(got) != 0 {
		t.Fatalf("none = %v", got)
	}
	t.Setenv("WEKNORA_PLUGIN_EMBEDDED_KINDS", "binary, node")
	if got := KindsFromEnv("WEKNORA_PLUGIN_EMBEDDED_KINDS"); len(got) != 1 || got[0] != KindBinary {
		t.Fatalf("binary, node = %v", got)
	}
}
