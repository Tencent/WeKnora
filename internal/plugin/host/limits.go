package host

import (
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
)

// envMemoryDefault is the memory limit of plugins that declare none
// (runtime.resources.memory), e.g. "1Gi". Unset: no limit. The cgroup
// setting (envCgroup) is Linux-only.
const envMemoryDefault = "WEKNORA_PLUGIN_MEMORY_DEFAULT"

// limits is what a plugin process may use; zero means no limit.
type limits struct {
	cpuMilli    int64
	memoryBytes int64
}

// limitsFor reads a plugin's declared resources, with the platform default
// memory for plugins that declare none. The manifest was validated, so
// parse errors only mean "no limit".
func limitsFor(m *manifest.Manifest) limits {
	var l limits
	if r := m.Runtime.Resources; r != nil {
		if r.CPU != "" {
			l.cpuMilli, _ = manifest.ParseCPU(r.CPU)
		}
		if r.Memory != "" {
			l.memoryBytes, _ = manifest.ParseMemory(r.Memory)
		}
	}
	if l.memoryBytes == 0 {
		if def := strings.TrimSpace(os.Getenv(envMemoryDefault)); def != "" {
			l.memoryBytes, _ = manifest.ParseMemory(def)
		}
	}
	return l
}
