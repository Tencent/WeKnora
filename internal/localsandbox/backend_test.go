package localsandbox

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Every platform must answer the availability question without panicking and
// without mutating the machine.
func TestNewBackendReportsAvailability(t *testing.T) {
	backend, err := NewBackend()
	if err != nil {
		require.ErrorIs(t, err, ErrUnsupportedPlatform)
		return
	}
	require.NotEmpty(t, backend.Name())
	_ = backend.Available()
}
