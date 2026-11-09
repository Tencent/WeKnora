package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func resetDeploymentParserDefaults(t *testing.T) {
	t.Helper()
	SetDeploymentParserDefaults("", nil, nil)
	t.Cleanup(func() { SetDeploymentParserDefaults("", nil, nil) })
}

func TestDeploymentParserDefaultEngine(t *testing.T) {
	resetDeploymentParserDefaults(t)
	SetDeploymentParserDefaults("mineru", []string{"pdf", ".DOCX"}, nil)

	require.Equal(t, "mineru", DefaultParserEngine("pdf"))
	require.Equal(t, "mineru", DefaultParserEngine(".PDF"))
	require.Equal(t, "mineru", DefaultParserEngine("docx"))
	require.Empty(t, DefaultParserEngine("xlsx"))
	require.Equal(t, "mineru", (&ParserEngineConfig{}).ResolveChatParserEngine("pdf"))
}

func TestDeploymentParserDefaultEngineRespectsExplicitRules(t *testing.T) {
	resetDeploymentParserDefaults(t)
	SetDeploymentParserDefaults("mineru", []string{"pdf"}, nil)

	kbConfig := ChunkingConfig{
		ParserEngineRules: []ParserEngineRule{{FileTypes: []string{"pdf"}, Engine: "builtin"}},
	}
	require.Equal(t, "builtin", kbConfig.ResolveParserEngine("pdf"))

	chatConfig := &ParserEngineConfig{
		ChatParserEngineRules: []ParserEngineRule{{FileTypes: []string{"pdf"}, Engine: "builtin"}},
	}
	require.Equal(t, "builtin", chatConfig.ResolveChatParserEngine("pdf"))
}

func TestDeploymentParserDefaultEngineRequiresFileTypes(t *testing.T) {
	resetDeploymentParserDefaults(t)
	SetDeploymentParserDefaults("mineru", nil, map[string]string{"mineru_endpoint": "http://mineru:8080"})

	require.Empty(t, DefaultParserEngine("pdf"))
	require.Equal(t, "http://mineru:8080", ParserOverridesFor(nil)["mineru_endpoint"])
}

func TestParserOverridesForMergesTenantOverDeployment(t *testing.T) {
	resetDeploymentParserDefaults(t)
	SetDeploymentParserDefaults("mineru", []string{"pdf"}, map[string]string{
		"mineru_endpoint": "http://deploy:8080",
		"mineru_model":    "vlm-auto-engine",
	})

	require.Equal(t, map[string]string{
		"mineru_endpoint": "http://deploy:8080",
		"mineru_model":    "vlm-auto-engine",
	}, ParserOverridesFor(nil))

	tenantConfig := &ParserEngineConfig{MinerUEndpoint: "http://tenant:8080"}
	require.Equal(t, map[string]string{
		"mineru_endpoint": "http://tenant:8080",
		"mineru_model":    "vlm-auto-engine",
	}, ParserOverridesFor(tenantConfig))
}

func TestParserOverridesForWithoutDeploymentDefaults(t *testing.T) {
	resetDeploymentParserDefaults(t)
	require.Empty(t, ParserOverridesFor(nil))
	require.Empty(t, ParserOverridesFor(&ParserEngineConfig{}))
}

func TestSetDeploymentParserDefaultsResetsOnReinstall(t *testing.T) {
	resetDeploymentParserDefaults(t)
	SetDeploymentParserDefaults("mineru", []string{"pdf"}, map[string]string{"mineru_endpoint": "http://mineru:8080"})
	require.Equal(t, "mineru", DefaultParserEngine("pdf"))

	SetDeploymentParserDefaults("", nil, nil)
	require.Empty(t, DefaultParserEngine("pdf"))
	require.Empty(t, ParserOverridesFor(nil))
}

func TestExplicitChatParserEngineSkipsTypeDefault(t *testing.T) {
	resetDeploymentParserDefaults(t)
	SetDeploymentParserDefaults("mineru", []string{"pdf"}, nil)

	agentConfig := &CustomAgentConfig{
		ChatParserEngineRules: []ParserEngineRule{{FileTypes: []string{"pdf"}, Engine: "paddleocr_vl"}},
	}
	engine, ok := agentConfig.ExplicitChatParserEngine("PDF")
	require.True(t, ok)
	require.Equal(t, "paddleocr_vl", engine)

	// Without an agent rule the deployment default must not look like an
	// agent choice, or the chat paths would skip tenant rules.
	engine, ok = (&CustomAgentConfig{}).ExplicitChatParserEngine("pdf")
	require.False(t, ok)
	require.Empty(t, engine)
	require.Equal(t, "mineru", (&CustomAgentConfig{}).ResolveChatParserEngine("pdf"))
}
