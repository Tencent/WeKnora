package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestPreviousBrowserImageKeepsItsExactResources(t *testing.T) {
	old := legacyBrowserFiles()
	digest := DigestFiles(old)
	manifest := PublishedManifest()
	manifest.Version = "2026.09.1"
	for i := range manifest.Skills {
		if manifest.Skills[i].Name == "browser" {
			manifest.Skills[i].Digest = digest
		}
	}
	entries := CompatibleEntries(manifest)
	require.Len(t, entries, 8)
	for _, entry := range entries {
		if entry.Name != "browser" {
			continue
		}
		require.Equal(t, "2026.09.1", entry.Version)
		files, err := FilesForEntry(entry)
		require.NoError(t, err)
		require.Equal(t, digest, DigestFiles(files))
		require.NotContains(t, string(files["scripts/browser.py"]), "'capabilities': ['pointer']")
		current, err := Files(entry.ID)
		require.NoError(t, err)
		require.NotEqual(t, digest, DigestFiles(current))
	}
	oldArchive, err := archiveFiles(old)
	require.NoError(t, err)
	sum := sha256.Sum256(oldArchive)
	require.True(t, MatchesArchiveDigest("browser", hex.EncodeToString(sum[:])))
	require.Empty(
		t,
		CompatibleEntries(
			&types.BuiltinSkillsManifest{
				SchemaVersion: 1,
				Skills: []types.BuiltinSkillDeclaration{
					{Name: "browser", Digest: "unrecognized", Verified: true},
				},
			},
		),
	)
}
