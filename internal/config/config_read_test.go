package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
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

func TestLoadConfigParsesParserDefaults(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	previousDir := resolvedConfigDir
	t.Cleanup(func() { resolvedConfigDir = previousDir })
	t.Cleanup(func() { types.SetDeploymentParserDefaults("", nil, nil) })
	t.Setenv("WEKNORA_TEST_MINERU_ENDPOINT", "http://mineru:8080")
	// Deployment overrides are SSRF-checked at load; private endpoints need the
	// allow-list entry documented next to the config.yaml example.
	t.Setenv("SSRF_WHITELIST_EXTRA", "mineru")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "server:\n  port: 8080\n  host: 127.0.0.1\n" +
		"knowledge_base:\n  chunk_size: 512\n  chunk_overlap: 80\n" +
		"parser_defaults:\n" +
		"  engine: mineru\n" +
		"  file_types: [pdf, docx]\n" +
		"  overrides:\n" +
		"    mineru_endpoint: ${WEKNORA_TEST_MINERU_ENDPOINT}\n"
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	viper.AddConfigPath(filepath.Dir(path))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg.ParserDefaults)
	require.Equal(t, "mineru", cfg.ParserDefaults.Engine)
	require.Equal(t, []string{"pdf", "docx"}, cfg.ParserDefaults.FileTypes)
	require.Equal(t, "http://mineru:8080", cfg.ParserDefaults.Overrides["mineru_endpoint"])
	require.Equal(t, "mineru", types.DefaultParserEngine("pdf"))
	require.Equal(t, "http://mineru:8080", types.ParserOverridesFor(nil)["mineru_endpoint"])
}

// Startup cannot see the DB-backed ssrf.whitelist and SSRF_WHITELIST is dropped
// at runtime once that setting exists, so a private endpoint only warns here: the
// parse-time gate stays the enforcement point. Structural mistakes stay fatal.
func TestLoadConfigValidatesParserDefaultOverrideURLs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
		wantErr  string // empty: the config must load
	}{
		{"private endpoint only warns", "http://127.0.0.1:8080", ""},
		{"malformed URL", "mineru:8080", "must be an absolute http(s) URL with a host"},
		{"hostless URL", "http://:8000", "must be an absolute http(s) URL with a host"},
		{"unset env placeholder", "${WEKNORA_TEST_MISSING_ENDPOINT}", "unset environment variable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			previousDir := resolvedConfigDir
			t.Cleanup(func() { resolvedConfigDir = previousDir })
			t.Cleanup(func() { types.SetDeploymentParserDefaults("", nil, nil) })
			t.Setenv("SSRF_WHITELIST", "")
			t.Setenv("SSRF_WHITELIST_EXTRA", "")
			utils.ResetSSRFWhitelistForTest()
			t.Cleanup(utils.ResetSSRFWhitelistForTest)
			path := filepath.Join(t.TempDir(), "config.yaml")
			contents := "server:\n  port: 8080\n  host: 127.0.0.1\n" +
				"knowledge_base:\n  chunk_size: 512\n  chunk_overlap: 80\n" +
				"parser_defaults:\n" +
				"  engine: mineru\n" +
				"  file_types: [pdf]\n" +
				"  overrides:\n" +
				"    mineru_endpoint: \"" + tc.endpoint + "\"\n"
			require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
			viper.AddConfigPath(filepath.Dir(path))

			cfg, err := LoadConfig()
			if tc.wantErr == "" {
				require.NoError(t, err)
				require.Equal(t, tc.endpoint, cfg.ParserDefaults.Overrides["mineru_endpoint"])
				require.Equal(t, "mineru", types.DefaultParserEngine("pdf"))
				require.Equal(t, tc.endpoint, types.ParserOverridesFor(nil)["mineru_endpoint"])
				return
			}
			require.Error(t, err)
			require.Nil(t, cfg)
			require.Contains(t, err.Error(), "parser_defaults.overrides.mineru_endpoint")
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestLoadConfigAcceptsWhitelistedParserDefaultOverride(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	previousDir := resolvedConfigDir
	t.Cleanup(func() { resolvedConfigDir = previousDir })
	t.Cleanup(func() { types.SetDeploymentParserDefaults("", nil, nil) })
	t.Setenv("SSRF_WHITELIST", "")
	t.Setenv("SSRF_WHITELIST_EXTRA", "mineru.example.internal")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "server:\n  port: 8080\n  host: 127.0.0.1\n" +
		"knowledge_base:\n  chunk_size: 512\n  chunk_overlap: 80\n" +
		"parser_defaults:\n" +
		"  overrides:\n" +
		"    mineru_endpoint: http://mineru.example.internal:8080\n"
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	viper.AddConfigPath(filepath.Dir(path))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "http://mineru.example.internal:8080", cfg.ParserDefaults.Overrides["mineru_endpoint"])
	require.Equal(t, "http://mineru.example.internal:8080", types.ParserOverridesFor(nil)["mineru_endpoint"])
}
