package host

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
)

func TestLimitsFor(t *testing.T) {
	m := &manifest.Manifest{Runtime: manifest.Runtime{Resources: &manifest.Resources{CPU: "250m", Memory: "256Mi"}}}
	if l := limitsFor(m); l.cpuMilli != 250 || l.memoryBytes != 256<<20 {
		t.Fatalf("declared = %+v", l)
	}
	bare := &manifest.Manifest{}
	if l := limitsFor(bare); l != (limits{}) {
		t.Fatalf("no resources, no default = %+v", l)
	}
	t.Setenv(envMemoryDefault, "1Gi")
	if l := limitsFor(bare); l.memoryBytes != 1<<30 || l.cpuMilli != 0 {
		t.Fatalf("platform default = %+v", l)
	}
	if l := limitsFor(m); l.memoryBytes != 256<<20 {
		t.Fatalf("a declared limit wins over the default: %+v", l)
	}
}
