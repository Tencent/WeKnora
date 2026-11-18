package localsandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDescribeTargetsReportsMetadataOnly(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(file, []byte("hello"), 0o644))
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "x"), nil, 0o644))

	facts, complete := describeTargets([]string{file, sub, filepath.Join(dir, "missing")})
	require.True(t, complete)
	require.Equal(t, []TargetFact{
		{Path: file, Type: "file", Size: 5},
		{Path: sub, Type: "directory", Entries: 1},
		{Path: filepath.Join(dir, "missing"), Type: "missing"},
	}, facts)
}

func TestDescribeTargetsStopsPastTheLimit(t *testing.T) {
	paths := make([]string, maxReviewTargets+1)
	for i := range paths {
		paths[i] = filepath.Join(t.TempDir(), "x")
	}
	facts, complete := describeTargets(paths)
	require.False(t, complete)
	require.Len(t, facts, maxReviewTargets)
}
