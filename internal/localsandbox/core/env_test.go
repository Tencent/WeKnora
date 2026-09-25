package core

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilterInheritedEnvDropsSecrets(t *testing.T) {
	got := FilterInheritedEnv([]string{
		"PATH=/bin",
		"HOME=/Users/dev",
		"AWS_SECRET_ACCESS_KEY=super-secret",
		"GITHUB_TOKEN=gho_secret",
		"LC_CTYPE=UTF-8",
	})
	joined := strings.Join(got, "\n")
	require.Contains(t, joined, "PATH=/bin")
	require.Contains(t, joined, "HOME=/Users/dev")
	require.Contains(t, joined, "LC_CTYPE=UTF-8")
	require.NotContains(t, joined, "super-secret")
	require.NotContains(t, joined, "GITHUB_TOKEN")
}

func TestBuildCommandEnvEmptyMapKeepsFilteredPATH(t *testing.T) {
	t.Setenv("PATH", "/bin:/usr/bin")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "super-secret")

	got := BuildCommandEnv(map[string]string{}, nil)
	require.Equal(t, "/bin:/usr/bin", got["PATH"])
	_, hasSecret := got["AWS_SECRET_ACCESS_KEY"]
	require.False(t, hasSecret)

	gotNil := BuildCommandEnv(nil, nil)
	require.Equal(t, got["PATH"], gotNil["PATH"])
}

func TestBuildCommandEnvOverlaysExplicitAndPrependsPATH(t *testing.T) {
	t.Setenv("PATH", "/bin")
	got := BuildCommandEnv(map[string]string{
		"FOO": "bar",
	}, []string{"/opt/tools/bin"})
	require.Equal(t, "bar", got["FOO"])
	require.True(t, strings.HasPrefix(got["PATH"], "/opt/tools/bin"+string(os.PathListSeparator)))
	require.Contains(t, got["PATH"], "/bin")
}

func TestBuildCommandEnvDropsLoaderInjectionFromExplicit(t *testing.T) {
	got := BuildCommandEnv(map[string]string{
		"AWS_SECRET_ACCESS_KEY": "skill-key",
		"DYLD_INSERT_LIBRARIES": "/tmp/evil.dylib",
		"LD_PRELOAD":            "/tmp/evil.so",
	}, nil)
	require.Equal(t, "skill-key", got["AWS_SECRET_ACCESS_KEY"])
	_, hasDYLD := got["DYLD_INSERT_LIBRARIES"]
	require.False(t, hasDYLD)
	_, hasPreload := got["LD_PRELOAD"]
	require.False(t, hasPreload)
}
