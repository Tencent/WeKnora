package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigParsesExpandedYAML(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		valid bool
	}{
		{"valid expansion", "127.0.0.9", true},
		{"malformed expansion", "[", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			previousDir := resolvedConfigDir
			t.Cleanup(func() { resolvedConfigDir = previousDir })
			t.Setenv("WEKNORA_TEST_CONFIG_HOST", tc.value)
			t.Setenv("SERVER_HOST", "")
			path := filepath.Join(t.TempDir(), "config.yaml")
			contents := "server:\n  port: 8080\n  host: ${WEKNORA_TEST_CONFIG_HOST}\n" +
				"knowledge_base:\n  chunk_size: 512\n  chunk_overlap: 80\n"
			require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
			viper.AddConfigPath(filepath.Dir(path))

			cfg, err := LoadConfig()
			if !tc.valid {
				var parseErr viper.ConfigParseError
				require.ErrorAs(t, err, &parseErr)
				require.Nil(t, cfg)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, cfg.Server)
			require.Equal(t, tc.value, cfg.Server.Host)
		})
	}
}
