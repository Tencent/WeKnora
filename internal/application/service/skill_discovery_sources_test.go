package service

import (
	"os"
	"path/filepath"
	"testing"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	"github.com/stretchr/testify/require"
)

func TestCommunityDiscoverySourcesSelectOneCompleteSkill(t *testing.T) {
	cases := map[string]struct {
		directory string
		resources []string
	}{
		"frontend-slides": {
			"plugins/frontend-slides/skills/frontend-slides",
			[]string{
				"STYLE_PRESETS.md", "viewport-base.css", "scripts/export-pdf.sh",
				"bold-template-pack/selection-index.json",
			},
		},
		"ppt-master": {"skills/ppt-master", []string{"scripts/example.py", "templates/example.svg"}},
	}
	for _, entry := range builtin.List() {
		if entry.Distribution != "community" {
			continue
		}
		t.Run(entry.Name, func(t *testing.T) {
			expected, ok := cases[entry.Name]
			require.True(t, ok, "add source import coverage for each recommended community skill")
			source, err := parseSkillSource(entry.InstallSource)
			require.NoError(t, err)
			require.Len(t, source.Ref, 40, "community sources must stay pinned to a commit")
			files := map[string][]byte{
				"repo-wrapper/README.md": []byte("outside the skill"),
				"repo-wrapper/SKILL.md": []byte(
					"---\nname: repository-entry\ndescription: another entry\n---\nRepository instructions\n",
				),
				"repo-wrapper/" + expected.directory + "/SKILL.md": []byte(
					"---\nname: " + entry.Name + "\ndescription: test skill\n---\nSkill instructions\n",
				),
			}
			for _, resource := range expected.resources {
				files["repo-wrapper/"+expected.directory+"/"+resource] = []byte("support resource")
			}
			archive, err := zipSkillFiles(files)
			require.NoError(t, err)
			_, _, err = normalizeFetchedSkill(archive, "application/zip", "")
			require.ErrorContains(t, err, "archive holds more than one skill")
			bundle, normalized, err := normalizeFetchedSkill(archive, "application/zip", source.Subdir)
			require.NoError(t, err)
			require.Equal(t, entry.Name, bundle.Name)
			require.NotContains(t, bundle.Files, "README.md")
			for _, resource := range expected.resources {
				require.Contains(t, bundle.Files, resource)
			}
			installed, err := ParseSkillBundle(normalized)
			require.NoError(t, err)
			require.Equal(t, bundle.Files, installed.Files)
		})
	}
}

// Opt-in validation of real, commit-pinned upstream repository ZIPs. This exercises
// production normalization without registering a skill or starting an install Agent.
func TestCommunityDiscoveryUpstreamArchives(t *testing.T) {
	directory := os.Getenv("WEKNORA_COMMUNITY_ARCHIVE_DIR")
	if directory == "" {
		t.Skip("set WEKNORA_COMMUNITY_ARCHIVE_DIR to the reviewed repository ZIPs")
	}
	for _, entry := range builtin.List() {
		if entry.Distribution != "community" {
			continue
		}
		t.Run(entry.Name, func(t *testing.T) {
			archive, err := os.ReadFile(filepath.Join(directory, entry.Name+".zip"))
			require.NoError(t, err)
			source, err := parseSkillSource(entry.InstallSource)
			require.NoError(t, err)
			bundle, normalized, err := normalizeFetchedSkill(archive, "application/zip", source.Subdir)
			require.NoError(t, err)
			require.Equal(t, entry.Name, bundle.Name)
			require.NotEmpty(t, bundle.Files["SKILL.md"])
			if entry.Name == "frontend-slides" {
				for _, name := range []string{
					"STYLE_PRESETS.md", "viewport-base.css", "scripts/export-pdf.sh",
					"bold-template-pack/selection-index.json",
				} {
					require.NotEmpty(t, bundle.Files[name], name)
				}
			}
			installed, err := ParseSkillBundle(normalized)
			require.NoError(t, err)
			require.Equal(t, bundle.Files, installed.Files)
			t.Logf(
				"validated %s: %d files, normalized archive %d bytes", entry.Name, len(bundle.Files), len(normalized),
			)
		})
	}
}
