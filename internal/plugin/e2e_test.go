package plugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// TestExternalLocalDirectoryProcessRuntime is the acceptance-path test. The
// binary is deliberately supplied from outside the test package so the test
// models an independently built plugin repository.
func TestExternalLocalDirectoryProcessRuntime(t *testing.T) {
	binary := os.Getenv("WEKNORA_LOCALDIR_PLUGIN_BIN")
	pluginRoot := os.Getenv("WEKNORA_LOCALDIR_PLUGIN_ROOT")
	var manifest Manifest
	if pluginRoot != "" {
		// When supplied, use the independent repository's own manifest instead
		// of synthesizing one in the host test. This is the strongest local
		// evidence for the "outside the main repository" acceptance criterion.
		packages, err := DiscoverPackages([]string{pluginRoot})
		require.NoError(t, err)
		require.Len(t, packages, 1)
		manifest = packages[0].Manifest
	} else {
		if binary == "" {
			t.Skip("set WEKNORA_LOCALDIR_PLUGIN_ROOT or WEKNORA_LOCALDIR_PLUGIN_BIN to run the external process acceptance test")
		}
		if _, err := os.Stat(binary); err != nil {
			t.Fatalf("plugin binary: %v", err)
		}
		pluginRoot = t.TempDir()
		manifest = Manifest{
			APIVersion:      APIVersionV1,
			ID:              "weknora.localdir",
			Name:            "Local Directory",
			Version:         "1.0.0",
			ExtensionType:   ExtensionDataSource,
			ProtocolVersion: ProtocolVersionV1,
			Entrypoint:      binary,
			Permissions:     Permissions{Network: NetworkNone},
			Capabilities:    []string{"incremental", "deletion_sync"},
		}
		manifestBytes, err := json.Marshal(manifest)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(pluginRoot, "plugin.json"), manifestBytes, 0o644))
	}

	sourceRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(sourceRoot, "a.md"), []byte("a-v1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sourceRoot, "b.md"), []byte("b-v1"), 0o644))

	manager := NewManager("")
	registry := datasource.NewConnectorRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, LoadExternal(ctx, []string{pluginRoot}, manager, registry))
	t.Cleanup(func() { _ = manager.Stop(context.Background(), manifest.ID) })

	health, err := manager.Health(ctx, manifest.ID)
	require.NoError(t, err)
	require.Equal(t, StateRunning, health.State)

	config := &types.DataSourceConfig{Type: manifest.ID, Settings: map[string]interface{}{"root": sourceRoot}}
	lease, err := registry.GetForScope(ctx, manifest.ID, datasource.ConnectorScope{DataSourceID: "datasource-1"}, config)
	require.NoError(t, err)
	connector := lease.Connector()
	all, err := connector.FetchAll(ctx, config, nil)
	require.NoError(t, err)
	require.Len(t, all, 2)

	changed, cursor, err := connector.FetchIncremental(ctx, config, nil)
	require.NoError(t, err)
	require.Len(t, changed, 2)
	require.NotNil(t, cursor)
	require.NoError(t, os.WriteFile(filepath.Join(sourceRoot, "a.md"), []byte("a-v2"), 0o644))
	changed, nextCursor, err := connector.FetchIncremental(ctx, config, cursor)
	require.NoError(t, err)
	require.Len(t, changed, 1)
	require.Equal(t, "file:a.md", changed[0].ExternalID)
	require.Equal(t, []byte("a-v2"), changed[0].Content)
	require.NotNil(t, nextCursor)

	require.NoError(t, lease.Close())
}
