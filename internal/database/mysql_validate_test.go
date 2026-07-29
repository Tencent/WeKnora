package database

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateMySQLVersionString(t *testing.T) {
	require.NoError(t, ValidateMySQLVersionString("8.0.16"))
	require.NoError(t, ValidateMySQLVersionString("8.4.2-commercial"))
	require.NoError(t, ValidateMySQLVersionString("8.0.36-google"))

	require.Error(t, ValidateMySQLVersionString("8.0.15"))
	require.Error(t, ValidateMySQLVersionString("5.7.44"))
	require.Error(t, ValidateMySQLVersionString("10.11.8-MariaDB"))
}
