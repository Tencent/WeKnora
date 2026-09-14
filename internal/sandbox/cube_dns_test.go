package sandbox

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeCubeDNSServers(t *testing.T) {
	got, err := NormalizeCubeDNSServers(nil)
	require.NoError(t, err)
	require.Nil(t, got)

	got, err = NormalizeCubeDNSServers([]string{"", "  "})
	require.NoError(t, err)
	require.Nil(t, got)

	got, err = NormalizeCubeDNSServers([]string{
		" 8.8.8.8 ", "8.8.8.8", "1.1.1.1",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"8.8.8.8", "1.1.1.1"}, got)

	_, err = NormalizeCubeDNSServers([]string{"dns.google"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "dns.google")
}
