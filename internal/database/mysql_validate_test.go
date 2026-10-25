package database

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMySQLVersionSupport(t *testing.T) {
	for _, version := range []string{"8.0.17", "8.0.18", "8.0.45-commercial", "8.4.8"} {
		require.NoError(t, ValidateMySQLVersionString(version))
	}
	for _, version := range []string{"5.7.44", "8.0.15", "8.0.16", "8.1.0", "9.0.0", "10.11.6-MariaDB", "unknown"} {
		require.Error(t, ValidateMySQLVersionString(version), version)
	}
}
