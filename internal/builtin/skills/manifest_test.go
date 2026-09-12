package skills

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestPublishedImageProfileContainsOnlyCoreOfficeSkills(t *testing.T) {
	manifest := PublishedManifest()
	require.Equal(t, "office-core", manifest.Profile)
	names := make([]string, 0, len(manifest.Skills))
	for _, skill := range manifest.Skills {
		names = append(names, skill.Name)
		require.NotEmpty(t, skill.Digest)
		require.True(t, skill.Verified)
	}
	require.ElementsMatch(t, []string{"docx", "xlsx", "pdf", "powerpoint"}, names)
	require.Len(t, CompatibleEntries(manifest), 4)
}

func TestCompatibleEntriesRequireCurrentVerifiedResources(t *testing.T) {
	manifest := PublishedManifest()
	manifest.Skills[0].Digest = "retired-development-resource"
	manifest.Skills[1].Verified = false
	entries := CompatibleEntries(manifest)
	require.Len(t, entries, 2)
	for _, entry := range entries {
		_, err := FilesForEntry(entry)
		require.NoError(t, err)
	}

	manifest.Skills = append(manifest.Skills, manifest.Skills[2])
	require.Empty(t, CompatibleEntries(manifest), "duplicate declarations must be rejected")
	require.Empty(t, CompatibleEntries(nil))
	require.Empty(t, CompatibleEntries(&types.BuiltinSkillsManifest{SchemaVersion: 2}))
}

func TestRetiredImageProfilesCannotBePublished(t *testing.T) {
	for _, profile := range []string{"office", "office-browser", "office-core-browser", "unknown"} {
		t.Run(profile, func(t *testing.T) {
			_, err := PublishedManifestForProfile(profile)
			require.Error(t, err)
		})
	}
}
