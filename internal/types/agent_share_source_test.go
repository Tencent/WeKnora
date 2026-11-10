package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAgentSourceTenantIDEmpty(t *testing.T) {
	value, err := ParseAgentSourceTenantID("")
	require.NoError(t, err)
	require.Equal(t, uint64(0), value)

	value, err = ParseAgentSourceTenantID("   ")
	require.NoError(t, err)
	require.Equal(t, uint64(0), value)
}

func TestParseAgentSourceTenantIDValid(t *testing.T) {
	value, err := ParseAgentSourceTenantID("42")
	require.NoError(t, err)
	require.Equal(t, uint64(42), value)
}

func TestParseAgentSourceTenantIDInvalid(t *testing.T) {
	_, err := ParseAgentSourceTenantID("abc")
	require.Error(t, err)

	_, err = ParseAgentSourceTenantID("42abc")
	require.Error(t, err)
}
