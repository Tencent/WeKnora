package envfile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadLiteBuildLoadsEnvLite(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env.lite"), []byte("DB_DRIVER=sqlite\nJWT_SECRET=from-lite\n"), 0o600))
	t.Chdir(dir)

	unsetEnvForTest(t, "ENV_FILE", "DB_DRIVER", "JWT_SECRET")
	t.Setenv("JWT_SECRET", "from-process")

	require.NoError(t, Load(true))
	assert.Equal(t, "sqlite", os.Getenv("DB_DRIVER"))
	assert.Equal(t, "from-process", os.Getenv("JWT_SECRET"), "process environment must take precedence")
}

func TestLoadLiteBuildIgnoresDotEnv(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env.lite"), []byte("DB_DRIVER=sqlite\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("DB_DRIVER=postgres\nREDIS_ADDR=redis:6379\n"), 0o600))
	t.Chdir(dir)

	unsetEnvForTest(t, "ENV_FILE", "DB_DRIVER", "REDIS_ADDR")

	require.NoError(t, Load(true))
	assert.Equal(t, "sqlite", os.Getenv("DB_DRIVER"), ".env.lite wins; .env must not override it")
	assert.Empty(t, os.Getenv("REDIS_ADDR"), ".env must not be consulted on Lite builds")
}

func TestLoadStandardBuildLoadsNothing(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env.lite"), []byte("DB_DRIVER=sqlite\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("DB_DRIVER=postgres\n"), 0o600))
	t.Chdir(dir)

	unsetEnvForTest(t, "ENV_FILE", "DB_DRIVER")

	require.NoError(t, Load(false))
	assert.Empty(t, os.Getenv("DB_DRIVER"), "standard builds must not auto-load dotenv files")
}

func TestLoadExplicitEnvFileOnStandardBuild(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "custom.env")
	require.NoError(t, os.WriteFile(custom, []byte("DB_DRIVER=custom\n"), 0o600))
	t.Chdir(dir)

	unsetEnvForTest(t, "DB_DRIVER")
	t.Setenv("ENV_FILE", custom)

	require.NoError(t, Load(false))
	assert.Equal(t, "custom", os.Getenv("DB_DRIVER"), "explicit ENV_FILE is honored on every build")
}

func TestLoadMissingFilesAreIgnored(t *testing.T) {
	t.Chdir(t.TempDir())
	unsetEnvForTest(t, "ENV_FILE")
	require.NoError(t, Load(true))
}

func TestLoadInvalidSyntaxIsReported(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env.lite"), []byte("DB_DRIVER=\"unterminated\n"), 0o600))
	t.Chdir(dir)

	unsetEnvForTest(t, "ENV_FILE", "DB_DRIVER")

	err := Load(true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".env.lite")
}

func unsetEnvForTest(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		value, exists := os.LookupEnv(key)
		require.NoError(t, os.Unsetenv(key))
		t.Cleanup(func() {
			if exists {
				_ = os.Setenv(key, value)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
}
