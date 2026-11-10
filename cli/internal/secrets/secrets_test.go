package secrets

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/testutil"
)

func TestFileStore_RoundTrip(t *testing.T) {
	testutil.XDGTempDir(t)
	s, err := NewFileStore()
	require.NoError(t, err)

	_, err = s.Get("prod", "access")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))

	require.NoError(t, s.Set("prod", "access", "secret-value"))
	got, err := s.Get("prod", "access")
	require.NoError(t, err)
	assert.Equal(t, "secret-value", got)

	require.NoError(t, s.Delete("prod", "access"))
	_, err = s.Get("prod", "access")
	require.True(t, errors.Is(err, ErrNotFound))

	require.NoError(t, s.Delete("prod", "access"), "Delete on missing must be a no-op")
}

func TestFileStore_FileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits don't map to Windows ACLs; 0600 invariant is Unix-only")
	}
	testutil.XDGTempDir(t)
	s, err := NewFileStore()
	require.NoError(t, err)
	require.NoError(t, s.Set("prod", "access", "v"))

	st, err := os.Stat(s.path("prod", "access"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm(), "secrets must be 0600")
}

func TestFileStore_NamespaceIsolation(t *testing.T) {
	testutil.XDGTempDir(t)
	s, err := NewFileStore()
	require.NoError(t, err)
	require.NoError(t, s.Set("prod", "access", "P"))
	require.NoError(t, s.Set("staging", "access", "S"))

	got, _ := s.Get("prod", "access")
	assert.Equal(t, "P", got)
	got, _ = s.Get("staging", "access")
	assert.Equal(t, "S", got)
}

func TestFileStore_Ref(t *testing.T) {
	testutil.XDGTempDir(t)
	s, err := NewFileStore()
	require.NoError(t, err)
	ref := s.Ref("prod", "access")
	// Ref is RFC 8089: scheme + slash-normalized path. On Windows the path
	// starts with a drive letter (file:///C:/...); on Unix file:///home/...
	assert.True(t, strings.HasPrefix(ref, "file:///"), "expected file:/// scheme, got %q", ref)
	assert.Contains(t, ref, "/prod/access")
}

func TestMemStore_RoundTripAndRef(t *testing.T) {
	s := NewMemStore()
	_, err := s.Get("p", "k")
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, s.Set("p", "k", "v"))
	got, err := s.Get("p", "k")
	require.NoError(t, err)
	assert.Equal(t, "v", got)
	assert.Equal(t, "mem://p/k", s.Ref("p", "k"))
	require.NoError(t, s.Delete("p", "k"))
	_, err = s.Get("p", "k")
	require.ErrorIs(t, err, ErrNotFound)
}
