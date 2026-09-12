package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestLoadConfigRejectsInvalidExpandedYAML(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("conversation: ${WEKNORA_TEST_BAD_YAML}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEKNORA_TEST_BAD_YAML", "[unterminated")
	t.Chdir(filepath.Dir(path))
	_, err := LoadConfig()
	if err == nil || !strings.Contains(err.Error(), "error reading expanded config") {
		t.Fatalf("invalid interpolated YAML must fail startup: %v", err)
	}
}
