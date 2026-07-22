package types

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSessionLastRequestStateIgnoresLegacyRecursiveFlag(t *testing.T) {
	var state SessionLastRequestState
	require.NoError(t, state.Scan([]byte(`{"folder_ids":["folder-1"],"include_`+`subfolders":false}`)))
	require.Equal(t, []string{"folder-1"}, state.FolderIDs)
	value, err := state.Value()
	require.NoError(t, err)
	require.NotContains(t, string(value.([]byte)), "include_"+"subfolders")
}
