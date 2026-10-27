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
// The default is blocks since parse_mode moved to the data source (2026-09
// deep adaptation): unset and empty both mean blocks, an explicit export is
// preserved as the image-association escape hatch, and an unrecognized value
// falls back to blocks instead of failing the sync. The retired
// FEISHU_DOCX_PARSE_MODE env var is still honored as a deprecated fallback
// while the per-source setting is unset.
func TestParseFeishuConfig_ParseMode(t *testing.T) {
	// Hermetic baseline: neutralize any ambient FEISHU_DOCX_PARSE_MODE so the
	// default cases below never depend on the host environment.
	t.Setenv("FEISHU_DOCX_PARSE_MODE", "")

	t.Run("unset settings default to blocks", func(t *testing.T) {
		cfg, err := ParseFeishuConfig(parseModeConfig(nil), RegionFeishu)
		require.NoError(t, err)
		assert.Equal(t, ParseModeBlocks, cfg.ParseMode)
	})

	t.Run("settings without parse_mode default to blocks", func(t *testing.T) {
		cfg, err := ParseFeishuConfig(
			parseModeConfig(map[string]interface{}{"timezone": "Asia/Shanghai"}), RegionFeishu)
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

	t.Run("deprecated env fallback honored while settings unset", func(t *testing.T) {
		t.Setenv("FEISHU_DOCX_PARSE_MODE", "export")
		cfg, err := ParseFeishuConfig(parseModeConfig(nil), RegionFeishu)
		require.NoError(t, err)
		assert.Equal(t, ParseModeExport, cfg.ParseMode)
	})

	t.Run("deprecated env fallback is case and whitespace insensitive", func(t *testing.T) {
		t.Setenv("FEISHU_DOCX_PARSE_MODE", " Export ")
		cfg, err := ParseFeishuConfig(
			parseModeConfig(map[string]interface{}{"timezone": "Asia/Shanghai"}), RegionFeishu)
		require.NoError(t, err)
		assert.Equal(t, ParseModeExport, cfg.ParseMode)
	})

	t.Run("per-source parse_mode overrides deprecated env", func(t *testing.T) {
		t.Setenv("FEISHU_DOCX_PARSE_MODE", "export")
		cfg, err := ParseFeishuConfig(parseModeConfig(map[string]interface{}{"parse_mode": "blocks"}), RegionFeishu)
		require.NoError(t, err)
		assert.Equal(t, ParseModeBlocks, cfg.ParseMode)
	})

	t.Run("invalid deprecated env value falls back to blocks", func(t *testing.T) {
		t.Setenv("FEISHU_DOCX_PARSE_MODE", "yaml")
		cfg, err := ParseFeishuConfig(parseModeConfig(nil), RegionFeishu)
		require.NoError(t, err)
		assert.Equal(t, ParseModeBlocks, cfg.ParseMode)
	})
}

// ParseMode threading: DocxFetchInput.ParseMode is the single transport
// (connector entry resolves it via ParseFeishuConfig and passes it through
// ops → fetchNodeContent → DocxFetchInput). Empty degrades to blocks inside
// FetchDocxWithBlocks; resolution itself is covered by
// TestParseFeishuConfig_ParseMode above, and the wiki/drive suites exercise
// the blocks default end-to-end with ops built without an explicit mode.
