package handler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseOptionalRFC3339(t *testing.T) {
	value, err := parseOptionalRFC3339("")
	require.NoError(t, err)
	require.Nil(t, value)

	value, err = parseOptionalRFC3339("2026-08-25T20:30:00+08:00")
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 8, 25, 12, 30, 0, 0, time.UTC), *value)

	value, err = parseOptionalRFC3339("2026-08-25")
	require.Error(t, err)
	require.Nil(t, value)
}
