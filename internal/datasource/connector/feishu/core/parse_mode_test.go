package core

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseModeConfig builds a minimal valid DataSourceConfig with the given
// Settings, enough for ParseFeishuConfig to succeed.
func parseModeConfig(settings map[string]interface{}) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type:        types.ConnectorTypeFeishu,
		Credentials: map[string]interface{}{"app_id": "cli-x", "app_secret": "sec"},
		ResourceIDs: []string{"space1"},
		Settings:    settings,
	}
}

// ParseFeishuConfig resolves Settings["parse_mode"] into Config.ParseMode.
// The default is blocks since the FEISHU_DOCX_PARSE_MODE env var was retired
// (2026-09 deep adaptation): unset and empty both mean blocks, an explicit
// export is preserved as the image-association escape hatch, and an
// unrecognized value falls back to blocks instead of failing the sync.
func TestParseFeishuConfig_ParseMode(t *testing.T) {
	t.Run("unset settings default to blocks", func(t *testing.T) {
		cfg, err := ParseFeishuConfig(parseModeConfig(nil), RegionFeishu)
		require.NoError(t, err)
		assert.Equal(t, ParseModeBlocks, cfg.ParseMode)
	})

	t.Run("settings without parse_mode default to blocks", func(t *testing.T) {
		cfg, err := ParseFeishuConfig(parseModeConfig(map[string]interface{}{"timezone": "Asia/Shanghai"}), RegionFeishu)
		require.NoError(t, err)
		assert.Equal(t, ParseModeBlocks, cfg.ParseMode)
	})

	t.Run("explicit blocks", func(t *testing.T) {
		cfg, err := ParseFeishuConfig(parseModeConfig(map[string]interface{}{"parse_mode": "blocks"}), RegionFeishu)
		require.NoError(t, err)
		assert.Equal(t, ParseModeBlocks, cfg.ParseMode)
	})

	t.Run("explicit export is preserved", func(t *testing.T) {
		cfg, err := ParseFeishuConfig(parseModeConfig(map[string]interface{}{"parse_mode": "export"}), RegionFeishu)
		require.NoError(t, err)
		assert.Equal(t, ParseModeExport, cfg.ParseMode)
	})

	t.Run("case and whitespace insensitive", func(t *testing.T) {
		cfg, err := ParseFeishuConfig(parseModeConfig(map[string]interface{}{"parse_mode": " Export "}), RegionFeishu)
		require.NoError(t, err)
		assert.Equal(t, ParseModeExport, cfg.ParseMode)
	})

	t.Run("invalid value falls back to blocks", func(t *testing.T) {
		cfg, err := ParseFeishuConfig(parseModeConfig(map[string]interface{}{"parse_mode": "yaml"}), RegionFeishu)
		require.NoError(t, err)
		assert.Equal(t, ParseModeBlocks, cfg.ParseMode)
	})

	t.Run("empty value falls back to blocks", func(t *testing.T) {
		cfg, err := ParseFeishuConfig(parseModeConfig(map[string]interface{}{"parse_mode": "  "}), RegionFeishu)
		require.NoError(t, err)
		assert.Equal(t, ParseModeBlocks, cfg.ParseMode)
	})
}

// NewClient carries Config.ParseMode so FetchDocxWithBlocks (which only sees
// the Client) picks the right path; empty stays empty and FetchDocxWithBlocks
// itself treats that as blocks.
func TestNewClient_CarriesParseMode(t *testing.T) {
	assert.Equal(t, ParseModeExport, NewClient(&Config{AppID: "a", AppSecret: "b", ParseMode: ParseModeExport}).parseMode)
	assert.Equal(t, ParseModeBlocks, NewClient(&Config{AppID: "a", AppSecret: "b", ParseMode: ParseModeBlocks}).parseMode)
	assert.Empty(t, NewClient(&Config{AppID: "a", AppSecret: "b"}).parseMode)
}
