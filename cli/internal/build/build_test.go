package build

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInfo_DefaultDevValues(t *testing.T) {
	v, c, d := Info()
	// In a non-ldflags-injected build, these are the package defaults; tests
	// that vary them need to assign at run time, not via -ldflags (testing
	// here only that Info returns whatever is current).
	assert.NotEmpty(t, v)
	assert.NotEmpty(t, c)
	assert.NotEmpty(t, d)
}

func TestInfo_ReflectsInjectedValues(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = "v0.0.0-test"
	v, _, _ := Info()
	assert.Equal(t, "v0.0.0-test", v)
}
