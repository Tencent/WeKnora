package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvironmentUsesExplicitFileWithoutOverwritingProcessEnv(t *testing.T) {
	t.Chdir(t.TempDir())
	writeEnvFixture(t, "custom.env", "WEKNORA_ENV_FILE_TEST=from-file\nWEKNORA_ENV_KEEP_TEST=from-file\n")
	t.Setenv("ENV_FILE", "custom.env")
	t.Setenv("WEKNORA_ENV_KEEP_TEST", "from-process")
	unsetEnvForTest(t, "WEKNORA_ENV_FILE_TEST")

	if err := LoadEnvironment(); err != nil {
		t.Fatalf("LoadEnvironment() error = %v", err)
	}
	if got := os.Getenv("WEKNORA_ENV_FILE_TEST"); got != "from-file" {
		t.Fatalf("WEKNORA_ENV_FILE_TEST = %q, want %q", got, "from-file")
	}
	if got := os.Getenv("WEKNORA_ENV_KEEP_TEST"); got != "from-process" {
		t.Fatalf("WEKNORA_ENV_KEEP_TEST = %q, want existing process value", got)
	}
}

func TestLoadEnvironmentDefaultPrecedence(t *testing.T) {
	t.Chdir(t.TempDir())
	writeEnvFixture(t, ".env", "WEKNORA_ENV_DEFAULT_TEST=dotenv\n")
	writeEnvFixture(t, ".env.lite", "WEKNORA_ENV_DEFAULT_TEST=lite\n")
	t.Setenv("ENV_FILE", "")
	unsetEnvForTest(t, "WEKNORA_ENV_DEFAULT_TEST")

	if err := LoadEnvironment(); err != nil {
		t.Fatalf("LoadEnvironment() error = %v", err)
	}
	if got := os.Getenv("WEKNORA_ENV_DEFAULT_TEST"); got != "dotenv" {
		t.Fatalf("WEKNORA_ENV_DEFAULT_TEST = %q, want .env to take precedence", got)
	}
}

func TestLoadEnvironmentFallsBackToLiteFile(t *testing.T) {
	t.Chdir(t.TempDir())
	writeEnvFixture(t, ".env.lite", "WEKNORA_ENV_LITE_TEST=lite\n")
	t.Setenv("ENV_FILE", "")
	unsetEnvForTest(t, "WEKNORA_ENV_LITE_TEST")

	if err := LoadEnvironment(); err != nil {
		t.Fatalf("LoadEnvironment() error = %v", err)
	}
	if got := os.Getenv("WEKNORA_ENV_LITE_TEST"); got != "lite" {
		t.Fatalf("WEKNORA_ENV_LITE_TEST = %q, want %q", got, "lite")
	}
}

func TestLoadEnvironmentRejectsMissingExplicitFile(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("ENV_FILE", "missing.env")

	if err := LoadEnvironment(); err == nil {
		t.Fatal("LoadEnvironment() error = nil, want missing explicit file error")
	}
}

func TestLoadEnvironmentAllowsMissingDefaultFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("ENV_FILE", "")

	if err := LoadEnvironment(); err != nil {
		t.Fatalf("LoadEnvironment() error = %v, want nil when default files are absent", err)
	}
}

func writeEnvFixture(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(".", name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func unsetEnvForTest(t *testing.T, name string) {
	t.Helper()
	value, existed := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("unset %s: %v", name, err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(name, value)
			return
		}
		_ = os.Unsetenv(name)
	})
}
