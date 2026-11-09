package service

import (
	"archive/zip"
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePluginMarketplaceWithAcmePluginsTarGz(t *testing.T) {
	data := acmePluginsArchive(t)

	files, err := UnpackMarketplaceArchive(data)
	require.NoError(t, err)
	require.NotEmpty(t, files)

	mp, err := ParsePluginMarketplace(files)
	require.NoError(t, err)
	require.NotNil(t, mp)
	assert.Equal(t, "acme-plugins", mp.Manifest.Name)
	assert.Len(t, mp.Plugins, 3)

	// Verify neon
	neon, ok := mp.Plugins["neon"]
	require.True(t, ok)
	assert.Equal(t, "neon", neon.Name)
	assert.True(t, neon.RequiresSandbox)
	assert.Equal(t, 2, neon.SkillCount)
	assert.Equal(t, 2, neon.MCPCount)
	assert.True(t, neon.HasPluginJSON)
	assert.Len(t, neon.Skills, 2)
	assert.Equal(t, "neon__neon-branches", neon.Skills[0].PrefixedName)
	assert.Equal(t, "neon__neon-postgres", neon.Skills[1].PrefixedName)

	// Verify calcom
	calcom, ok := mp.Plugins["calcom"]
	require.True(t, ok)
	assert.Equal(t, "calcom", calcom.Name)
	assert.False(t, calcom.RequiresSandbox)
	assert.Equal(t, 0, calcom.SkillCount)
	assert.Equal(t, 1, calcom.MCPCount)
	assert.True(t, calcom.HasPluginJSON)

	// Verify sql-style (fallback since no plugin.json)
	sqlStyle, ok := mp.Plugins["sql-style"]
	require.True(t, ok)
	assert.Equal(t, "sql-style", sqlStyle.Name)
	assert.True(t, sqlStyle.RequiresSandbox)
	assert.Equal(t, 1, sqlStyle.SkillCount)
	assert.Equal(t, 0, sqlStyle.MCPCount)
	assert.False(t, sqlStyle.HasPluginJSON)
	assert.Equal(t, "sql-style__sql-style", sqlStyle.Skills[0].PrefixedName)
}

func TestPluginEnvMissingDeclarationError(t *testing.T) {
	files := map[string][]byte{
		".weknora-plugin/marketplace.json": []byte(`{
			"name": "test-mp",
			"plugins": [{"name": "bad-plugin", "source": "./bad"}]
		}`),
		"bad/.weknora-plugin/plugin.json": []byte(`{
			"name": "bad-plugin",
			"env": [{"name": "DECLARED_VAR", "required": true}]
		}`),
		"bad/.mcp.json": []byte(`{
			"mcpServers": {
				"bad-srv": {
					"type": "http",
					"url": "https://example.com/mcp",
					"headers": {"Authorization": "Bearer ${UNDECLARED_VAR}"}
				}
			}
		}`),
	}

	mp, err := ParsePluginMarketplace(files)
	require.NoError(t, err)
	var names []string
	for _, env := range mp.Plugins["bad-plugin"].EnvDeclarations {
		if env.Required {
			names = append(names, env.Name)
		}
	}
	assert.ElementsMatch(t, []string{"DECLARED_VAR", "UNDECLARED_VAR"}, names)
}

func TestPluginStdioSkippedWithWarning(t *testing.T) {
	files := map[string][]byte{
		".weknora-plugin/marketplace.json": []byte(`{
			"name": "test-mp",
			"plugins": [{"name": "stdio-plugin", "source": "./stdio"}]
		}`),
		"stdio/.mcp.json": []byte(`{
			"mcpServers": {
				"stdio-srv": {
					"type": "stdio",
					"command": "npx",
					"args": ["-y", "mcp-server"]
				},
				"http-srv": {
					"type": "http",
					"url": "https://example.com/mcp"
				}
			}
		}`),
	}

	mp, err := ParsePluginMarketplace(files)
	require.NoError(t, err)
	p := mp.Plugins["stdio-plugin"]
	require.NotNil(t, p)
	assert.Equal(t, 1, p.MCPCount)
	assert.Contains(t, p.MCPServers, "http-srv")
	assert.NotContains(t, p.MCPServers, "stdio-srv")
	assert.Len(t, p.MCPWarnings, 1)
	assert.Contains(t, p.MCPWarnings[0], "已禁用 stdio transport")
}

func TestValidatePluginName(t *testing.T) {
	for _, name := range []string{"neon", "sql-style", "my_plugin", "插件1", "acme.tools"} {
		assert.NoError(t, validatePluginName(name), name)
	}
	for _, name := range []string{
		"", "a/b", "a b", "-lead", "a__b", "a..b", strings.Repeat("x", maxPluginNameLength+1),
	} {
		assert.Error(t, validatePluginName(name), name)
	}
}

func TestRewriteSkillFrontmatterNameKeepsNestedKeys(t *testing.T) {
	in := "---\nname: orig\nmetadata:\n  name: nested\ndescription: d\n---\nbody\n"
	out, err := rewriteSkillFrontmatterName([]byte(in), "acme__orig")
	require.NoError(t, err)
	assert.Equal(t, "---\nname: acme__orig\nmetadata:\n  name: nested\ndescription: d\n---\nbody\n", string(out))
}

const sampleSkillMD = "---\nname: demo\ndescription: A demo skill\n---\nDo the thing.\n"

func TestParseClaudeMarketplaceNormalizesToWeKnora(t *testing.T) {
	files := map[string][]byte{
		"repo/.claude-plugin/marketplace.json": []byte(`{
			"name": "claude-market",
			"description": "From Claude",
			"owner": {"name": "Acme"},
			"metadata": {"pluginRoot": "plugins"},
			"plugins": [{
				"name": "acme.tools",
				"source": "docs",
				"version": "1.2.0",
				"description": "Docs helper"
			}]
		}`),
		"repo/plugins/docs/.claude-plugin/plugin.json": []byte(`{
			"name": "acme.tools",
			"skills": ["./guides"],
			"mcpServers": "./servers.json"
		}`),
		"repo/plugins/docs/guides/SKILL.md": []byte(sampleSkillMD),
		"repo/plugins/docs/servers.json": []byte(`{
			"mcpServers": {
				"docs": {
					"type": "streamable-http",
					"url": "https://example.com/mcp",
					"headers": {"Authorization": "Bearer ${DOCS_TOKEN}"}
				}
			}
		}`),
	}
	raw, err := normalizeMarketplaceFiles(files)
	require.NoError(t, err)
	mp, err := ParsePluginMarketplace(raw)
	require.NoError(t, err)
	assert.Equal(t, "claude-market", mp.Manifest.Name)
	assert.Equal(t, "From Claude", mp.Manifest.Metadata.Description)
	plugin := mp.Plugins["acme.tools"]
	require.NotNil(t, plugin)
	assert.Equal(t, "1.2.0", plugin.Version)
	assert.Equal(t, "http", plugin.MCPServers["docs"].Type)
	require.Len(t, plugin.EnvDeclarations, 1)
	assert.Equal(t, "DOCS_TOKEN", plugin.EnvDeclarations[0].Name)
	assert.True(t, plugin.EnvDeclarations[0].Required)
	require.Len(t, plugin.Skills, 1)
	assert.Equal(t, "acme.tools__guides", plugin.Skills[0].PrefixedName)
}

func TestRemotePluginSourceIsSkipped(t *testing.T) {
	files := map[string][]byte{
		".cursor-plugin/marketplace.json": []byte(`{
			"name": "cursor-market",
			"owner": {"name": "Acme"},
			"plugins": [
				{"name": "local", "source": "./local"},
				{"name": "remote", "source": {"source": "github", "repo": "acme/remote"}}
			]
		}`),
		"local/mcp.json": []byte(`{"mcpServers": {"api": {"type": "http", "url": "https://example.com/mcp"}}}`),
	}
	mp, err := ParsePluginMarketplace(files)
	require.NoError(t, err)
	assert.Contains(t, mp.Plugins, "local")
	assert.NotContains(t, mp.Plugins, "remote")
	require.Len(t, mp.Warnings, 1)
	assert.Contains(t, mp.Warnings[0], "remote")
	assert.Contains(t, mp.Warnings[0], "github acme/remote")
}

func TestRemoteOnlyMarketplaceIsRejected(t *testing.T) {
	files := map[string][]byte{
		".agents/plugins/marketplace.json": []byte(`{
			"name": "codex-market",
			"interface": {"displayName": "Codex"},
			"plugins": [{"name": "remote", "source": {"source": "npm", "package": "remote-plugin"}}]
		}`),
	}
	_, err := ParsePluginMarketplace(files)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSkillBundleInvalid)
	assert.Contains(t, err.Error(), "remote")
}

func TestWeKnoraManifestWinsOverClaudeAtTheSameRoot(t *testing.T) {
	files := map[string][]byte{
		".weknora-plugin/marketplace.json": []byte(`{
			"name": "ours",
			"plugins": [{"name": "ours", "source": "./ours"}]
		}`),
		".claude-plugin/marketplace.json": []byte(`{
			"name": "theirs",
			"plugins": [{"name": "theirs", "source": "./theirs"}]
		}`),
		"ours/SKILL.md": []byte(sampleSkillMD),
	}
	mp, err := ParsePluginMarketplace(files)
	require.NoError(t, err)
	assert.Equal(t, "ours", mp.Manifest.Name)
	assert.Contains(t, mp.Plugins, "ours")
	assert.NotContains(t, mp.Plugins, "theirs")
}

func TestBrokenClaudeManifestFallsThroughToCopilot(t *testing.T) {
	files := map[string][]byte{
		"copilot-plugins-HEAD/.claude-plugin/marketplace.json": []byte(`{`),
		"copilot-plugins-HEAD/.github/plugin/marketplace.json": []byte(`{
			"name": "copilot-plugins",
			"metadata": {"description": "GitHub Copilot plugins", "version": "1.0.0"},
			"plugins": [{"name": "spark", "source": "./plugins/spark"}]
		}`),
		"copilot-plugins-HEAD/plugins/spark/skills/spark/SKILL.md": []byte(sampleSkillMD),
	}
	mp, err := ParsePluginMarketplace(files)
	require.NoError(t, err)
	assert.Equal(t, "copilot-plugins", mp.Manifest.Name)
	assert.Contains(t, mp.Plugins, "spark")
	require.NotEmpty(t, mp.Warnings)
	assert.Contains(t, mp.Warnings[0], ".claude-plugin/marketplace.json")
}

func TestCopilotMarketplaceSymlinkUsesTheRealCatalog(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	link := &zip.FileHeader{Name: "copilot-plugins-HEAD/.claude-plugin/marketplace.json"}
	link.SetMode(os.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(link)
	require.NoError(t, err)
	_, err = w.Write([]byte("../.github/plugin/marketplace.json"))
	require.NoError(t, err)
	catalog := `{
		"name": "copilot-plugins",
		"metadata": {"description": "GitHub Copilot plugins"},
		"plugins": [{"name": "spark", "source": "./plugins/spark"}]
	}`
	w, err = zw.Create("copilot-plugins-HEAD/.github/plugin/marketplace.json")
	require.NoError(t, err)
	_, err = w.Write([]byte(catalog))
	require.NoError(t, err)
	w, err = zw.Create("copilot-plugins-HEAD/plugins/spark/skills/spark/SKILL.md")
	require.NoError(t, err)
	_, err = w.Write([]byte(sampleSkillMD))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	files, err := UnpackMarketplaceArchive(buf.Bytes())
	require.NoError(t, err)
	_, stillLinked := files[".claude-plugin/marketplace.json"]
	assert.False(t, stillLinked)
	assert.Contains(t, files, ".github/plugin/marketplace.json")
	mp, err := ParsePluginMarketplace(files)
	require.NoError(t, err)
	assert.Equal(t, "copilot-plugins", mp.Manifest.Name)
	require.Contains(t, mp.Plugins, "spark")
	assert.Equal(t, 1, mp.Plugins["spark"].SkillCount)
}

func TestShallowClaudeManifestBeatsNestedWeKnora(t *testing.T) {
	files := map[string][]byte{
		".claude-plugin/marketplace.json": []byte(`{
			"name": "claude-root",
			"plugins": [{"name": "root", "source": "./root"}]
		}`),
		"vendor/example/.weknora-plugin/marketplace.json": []byte(`{
			"name": "nested",
			"plugins": [{"name": "nested", "source": "./nested"}]
		}`),
		"root/mcp.json": []byte(`{"mcpServers": {"api": {"url": "https://example.com/mcp"}}}`),
	}
	mp, err := ParsePluginMarketplace(files)
	require.NoError(t, err)
	assert.Equal(t, "claude-root", mp.Manifest.Name)
	assert.Equal(t, "http", mp.Plugins["root"].MCPServers["api"].Type)
}

func TestLoosePluginBundleReadsRootAndOneLevel(t *testing.T) {
	root := map[string][]byte{
		"repo-sha/plugin.json":            []byte(`{"name": "docs-helper", "description": "Docs"}`),
		"repo-sha/skills/search/SKILL.md": []byte(sampleSkillMD),
		"repo-sha/mcp.json": []byte(
			`{"mcpServers": {"docs": {"type": "sse", "url": "https://example.com/mcp"}}}`,
		),
	}
	mp, err := ParsePluginMarketplace(root)
	require.NoError(t, err)
	assert.Equal(t, "docs-helper", mp.Manifest.Name)
	assert.Equal(t, "sse", mp.Plugins["docs-helper"].MCPServers["docs"].Type)
	assert.Equal(t, "docs-helper__search", mp.Plugins["docs-helper"].Skills[0].PrefixedName)

	siblings := map[string][]byte{
		"alpha/plugin.json":       []byte(`{"name": "alpha"}`),
		"alpha/skills/a/SKILL.md": []byte(sampleSkillMD),
		"beta/gemini-extension.json": []byte(
			`{"name": "beta", "description": "Gemini", "mcpServers": {"g": {"command": "node", "args": ["s.js"]}}}`,
		),
		"beta/skills/b/SKILL.md": []byte(sampleSkillMD),
		"notes/README.md":        []byte("ignore"),
	}
	mp, err = ParsePluginMarketplace(siblings)
	require.NoError(t, err)
	assert.Equal(t, "plugins", mp.Manifest.Name)
	assert.ElementsMatch(t, []string{"alpha", "beta"}, mp.Order)
	assert.Empty(t, mp.Plugins["beta"].MCPServers)
	assert.Contains(t, mp.Plugins["beta"].MCPWarnings[0], "stdio")
}

func TestCodexLocalSourceObject(t *testing.T) {
	files := map[string][]byte{
		".agents/plugins/marketplace.json": []byte(`{
			"name": "codex-local",
			"interface": {"displayName": "Team"},
			"plugins": [{
				"name": "linear",
				"source": {"source": "local", "path": "./plugins/linear"},
				"category": "Productivity"
			}]
		}`),
		"plugins/linear/.codex-plugin/plugin.json": []byte(`{"name": "linear", "description": "Linear"}`),
		"plugins/linear/.mcp.json": []byte(
			`{"mcpServers": {"linear": {"type": "http", "url": "https://example.com/mcp"}}}`,
		),
	}
	mp, err := ParsePluginMarketplace(files)
	require.NoError(t, err)
	assert.Equal(t, "Team", mp.Manifest.Metadata.Description)
	assert.Equal(t, "Productivity", mp.Plugins["linear"].Category)
	assert.Equal(t, "Linear", mp.Plugins["linear"].Description)
}

func TestCommandOnlyPluginIsSkipped(t *testing.T) {
	files := map[string][]byte{
		".claude-plugin/marketplace.json": []byte(`{
			"name": "claude-plugins-official",
			"plugins": [
				{"name": "asana", "source": "./external_plugins/asana", "description": "Asana"},
				{"name": "frontend-design", "source": "./plugins/frontend-design"}
			]
		}`),
		"external_plugins/asana/.claude-plugin/plugin.json": []byte(
			`{"name":"asana","description":"Connect Asana"}`,
		),
		"external_plugins/asana/README.md": []byte("# Asana\n"),
		"external_plugins/asana/commands/asana-setup.md": []byte(
			"---\ndescription: Set up Asana\n---\nRun claude mcp add.\n",
		),
		"plugins/frontend-design/skills/frontend-design/SKILL.md": []byte(sampleSkillMD),
	}
	// The stdio-only plugin is a separate marketplace so its absence is asserted on its own.
	mp, err := ParsePluginMarketplace(files)
	require.NoError(t, err)
	assert.NotContains(t, mp.Plugins, "asana")
	assert.Contains(t, mp.Plugins, "frontend-design")
	require.Len(t, mp.Warnings, 1)
	assert.Contains(t, mp.Warnings[0], "asana")
	assert.Contains(t, mp.Warnings[0], "没有可安装的技能或 MCP")
	names := make([]string, 0, len(mp.Manifest.Plugins))
	for _, entry := range mp.Manifest.Plugins {
		names = append(names, entry.Name)
	}
	assert.Equal(t, []string{"frontend-design"}, names)

	stdioOnly := map[string][]byte{
		".claude-plugin/marketplace.json": []byte(`{
			"name": "stdio-market",
			"plugins": [{"name": "local-only", "source": "./stdio-only"}]
		}`),
		"stdio-only/.mcp.json": []byte(`{"mcpServers":{"local":{"command":"npx","args":["srv"]}}}`),
	}
	_, err = ParsePluginMarketplace(stdioOnly)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSkillBundleInvalid)
	assert.Contains(t, err.Error(), "local-only")
	assert.Contains(t, err.Error(), "stdio")
}

func TestOfficialClaudeSkillNamesAndPlaceholdersImport(t *testing.T) {
	files := map[string][]byte{
		".claude-plugin/marketplace.json": []byte(`{
			"name": "claude-plugins-official",
			"owner": {"name": "Anthropic"},
			"plugins": [
				{"name": "claude-code-setup", "source": "./plugins/claude-code-setup"},
				{"name": "math-proof", "source": "./plugins/math-proof"}
			]
		}`),
		"plugins/claude-code-setup/.claude-plugin/plugin.json": []byte(`{"name":"claude-code-setup"}`),
		"plugins/claude-code-setup/skills/claude-automation-recommender/SKILL.md": []byte(
			"---\nname: claude-automation-recommender\n" +
				"description: Recommend Claude Code automations.\n---\nRead the repo.\n"),
		"plugins/math-proof/.claude-plugin/plugin.json": []byte(`{"name":"math-proof"}`),
		"plugins/math-proof/skills/siege/SKILL.md": []byte(
			"---\nname: siege\n" +
				"description: \"Work a problem. Usage: <the problem, stated in full>.\"\n---\nFollow the steps.\n"),
	}
	mp, err := ParsePluginMarketplace(files)
	require.NoError(t, err)
	setup := mp.Plugins["claude-code-setup"]
	require.NotNil(t, setup)
	require.Len(t, setup.Skills, 1)
	assert.Equal(t, "claude-code-setup__claude-automation-recommender", setup.Skills[0].PrefixedName)
	proof := mp.Plugins["math-proof"]
	require.NotNil(t, proof)
	require.Len(t, proof.Skills, 1)
	assert.Contains(t, proof.Skills[0].Bundle.Description, "<the problem, stated in full>")
}

func TestNormalizeMarketplaceFilesPicksShallowestManifest(t *testing.T) {
	raw := map[string][]byte{
		"root/.weknora-plugin/marketplace.json":          []byte(`{}`),
		"root/vendor/x/.weknora-plugin/marketplace.json": []byte(`{}`),
		"root/plugins/a/.weknora-plugin/plugin.json":     []byte(`{}`),
	}
	for i := 0; i < 20; i++ {
		out, err := normalizeMarketplaceFiles(raw)
		require.NoError(t, err)
		assert.Contains(t, out, ".weknora-plugin/marketplace.json")
		assert.Contains(t, out, "plugins/a/.weknora-plugin/plugin.json")
	}
}
