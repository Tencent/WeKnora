package envfile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadPrefersLiteFileAndKeepsProcessEnvironment(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env.lite"), []byte("DB_DRIVER=sqlite\nJWT_SECRET=from-lite\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("DB_DRIVER=postgres\nDB_HOST=from-dotenv\n"), 0o600))
	t.Chdir(dir)

	unsetEnvForTest(t, "ENV_FILE", "DB_DRIVER", "DB_HOST", "JWT_SECRET")
	t.Setenv("JWT_SECRET", "from-process")

	require.NoError(t, Load())
	assert.Equal(t, "sqlite", os.Getenv("DB_DRIVER"), ".env.lite should be the default for Lite packages")
	assert.Equal(t, "from-dotenv", os.Getenv("DB_HOST"), "the fallback .env should fill missing values")
	assert.Equal(t, "from-process", os.Getenv("JWT_SECRET"), "process environment must take precedence")
}

func TestLoadUsesExplicitEnvFile(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "custom.env")
	require.NoError(t, os.WriteFile(custom, []byte("DB_DRIVER=custom\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env.lite"), []byte("DB_DRIVER=sqlite\n"), 0o600))
	t.Chdir(dir)

	unsetEnvForTest(t, "DB_DRIVER")
	t.Setenv("ENV_FILE", custom)

	require.NoError(t, Load())
	assert.Equal(t, "custom", os.Getenv("DB_DRIVER"))
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
