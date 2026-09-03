package sandbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetectLocalDockerHostPrefersEnv(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///tmp/from-env.sock")
	t.Setenv("DOCKER_CONTEXT", "colima")
	require.Equal(t, "unix:///tmp/from-env.sock", DetectLocalDockerHost())
}

func TestDetectLocalDockerHostUsesCurrentContext(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "contexts", "meta", "abc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(
		`{"currentContext":"colima"}`,
	), 0o644))
	meta, err := json.Marshal(map[string]any{
		"Name": "colima",
		"Endpoints": map[string]any{
			"docker": map[string]any{"Host": "unix:///tmp/colima.sock"},
		},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "contexts", "meta", "abc", "meta.json"), meta, 0o644,
	))

	require.Equal(t, "unix:///tmp/colima.sock", DetectLocalDockerHost())
}

func TestDetectLocalDockerHostFallsBackToDefault(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	require.Equal(t, DefaultDockerHost, DetectLocalDockerHost())
}
