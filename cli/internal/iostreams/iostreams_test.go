package iostreams

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetForTest(t *testing.T) {
	out, errBuf := SetForTest(t)
	IO.Out.Write([]byte("stdout"))
	IO.Err.Write([]byte("stderr"))
	assert.Equal(t, "stdout", out.String())
	assert.Equal(t, "stderr", errBuf.String())
}

func TestSetForTest_RestoresSingleton(t *testing.T) {
	original := IO
	t.Run("nested", func(t *testing.T) {
		SetForTest(t)
		assert.NotSame(t, original, IO)
	})
	// After the subtest's t.Cleanup runs, IO must be restored.
	assert.Same(t, original, IO)
}

func TestProductionGetters(t *testing.T) {
	// We can't reliably assert the *value* of these (depends on whether tests
	// run in a TTY) but we can ensure the methods don't panic and return
	// consistent values across calls.
	p := newProduction()
	a, b := p.IsStdoutTTY(), p.IsStderrTTY()
	assert.Equal(t, a, p.IsStdoutTTY())
	assert.Equal(t, b, p.IsStderrTTY())
	_ = p.ColorEnabled() // smoke
}
