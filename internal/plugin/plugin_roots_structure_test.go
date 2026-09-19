package plugin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// composePluginRootDirs are the per-extension-type subdirectories of the
// repository's ./plugins tree. docker-compose.yml always injects
// WEKNORA_PLUGIN_DIR_* pointing at /app/plugins/<type> and bind-mounts the
// repository's ./plugins over /app/plugins, so every directory must be present
// in a fresh clone even when no plugin is installed.
var composePluginRootDirs = []string{"datasource", "parser", "search", "model", "retriever"}

// TestRepoPluginRootDirsArePublishable guards a startup-ordering property that
// is easy to break by accident: DiscoverPackages (discovery.go) returns an error
// for a scan root that does not exist, and that error reaches
// must(container.Invoke(loadExternalPlugins)) in container.go — so a missing
// directory panics the app at startup instead of degrading to "no plugins
// installed". Because git cannot track empty directories, each directory keeps a
// committed placeholder file; this test fails if a placeholder or a whole
// directory disappears from the tree, which is exactly the regression that would
// make a fresh `docker compose up` crash-loop.
func TestRepoPluginRootDirsArePublishable(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	for _, dir := range composePluginRootDirs {
		path := filepath.Join(repoRoot, "plugins", dir)

		info, err := os.Stat(path)
		require.NoErrorf(t, err,
			"plugins/%s must exist: docker-compose.yml points WEKNORA_PLUGIN_DIR_* at /app/plugins/%s, "+
				"and app startup panics when that root is missing", dir, dir)
		require.Truef(t, info.IsDir(), "plugins/%s must be a directory", dir)

		entries, err := os.ReadDir(path)
		require.NoErrorf(t, err, "read plugins/%s", dir)
		require.NotEmptyf(t, entries,
			"plugins/%s must not be empty: git cannot track empty directories, so it would be absent "+
				"from a fresh clone and the app would fail to start", dir)
	}
}
