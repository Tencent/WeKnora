package session

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMatchApprovedDirAcceptsOnlyApprovedAbsolutePaths(t *testing.T) {
	approved := []string{"/Users/dev/My Project", " /Users/dev/Other/ ", ""}

	got, ok := MatchApprovedDir("/Users/dev/My Project", approved)
	require.True(t, ok)
	require.Equal(t, "/Users/dev/My Project", got)

	got, ok = MatchApprovedDir("  /Users/dev/My Project/  ", approved)
	require.True(t, ok, "trailing separator and padding must still match")
	require.Equal(t, "/Users/dev/My Project", got)

	got, ok = MatchApprovedDir("/Users/dev/Other", approved)
	require.True(t, ok, "an approved entry is cleaned before comparison")
	require.Equal(t, "/Users/dev/Other", got)

	for _, bad := range []string{
		"",
		"relative/dir",
		"/Users/dev/My Project/nested",
		"/Users/dev/Unapproved",
	} {
		_, ok := MatchApprovedDir(bad, approved)
		require.False(t, ok, bad)
	}
}

func TestMatchApprovedDirRejectsEverythingWithoutApprovals(t *testing.T) {
	_, ok := MatchApprovedDir("/Users/dev/My Project", nil)
	require.False(t, ok)
}
