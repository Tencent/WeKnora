package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsValidSessionKind(t *testing.T) {
	for _, kind := range []string{"", SessionKindAssistant} {
		require.True(t, IsValidSessionKind(kind))
	}
	for _, kind := range []string{"normal", "Assistant", " assistant", "assistant ", "web"} {
		require.False(t, IsValidSessionKind(kind))
	}
}
