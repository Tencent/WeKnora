package publicdata

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicCatalogRejectsMissingAndTamperedArtifacts(t *testing.T) {
	_, err := list(fstest.MapFS{})
	require.Error(t, err, "an empty catalog must not report success")
	manifest, err := fs.ReadFile(artifacts, "cmrc2018-dev/v1/manifest.json")
	require.NoError(t, err)
	fixture := fstest.MapFS{
		"cmrc2018-dev/v1/manifest.json":       {Data: manifest},
		"cmrc2018-dev/v1/registry-input.json": {Data: []byte(`{"passages":[]}`)},
	}
	_, _, err = readBundle(fixture, "cmrc2018-dev")
	require.ErrorContains(t, err, "SHA-256 mismatch")
	for _, id := range []string{"", "unknown", "weknora-docs", "../cmrc2018-dev", "https://example.com"} {
		_, err := Get(id)
		assert.ErrorIs(t, err, ErrNotFound)
	}
}

func TestPublicCatalogContainsSourceAttributedEvaluationBundles(t *testing.T) {
	items, err := List()
	require.NoError(t, err)
	require.Len(t, items, 2)
	for _, item := range items {
		assert.NotEmpty(t, item.SourceURL)
		assert.NotEmpty(t, item.License)
		assert.NotEmpty(t, item.Limitations)
		bundle, err := Get(item.ID)
		require.NoError(t, err)
		assert.Len(t, bundle.Content.Questions, item.Counts["questions"])
	}
}
