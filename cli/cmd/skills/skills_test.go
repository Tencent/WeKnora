package skillscmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	skillsfs "github.com/Tencent/WeKnora/cli/skills"
)

// TestEmbeddedSkillsPresent: the binary embeds the bundled skills with their
// SKILL.md (the parity tests guard the command vocabulary; this guards the
// embed itself stays wired).
func TestEmbeddedSkillsPresent(t *testing.T) {
	skills, err := skillsfs.List()
	require.NoError(t, err)
	require.NotEmpty(t, skills, "at least one skill must be embedded")
	for _, s := range skills {
		assert.NotEmpty(t, s.Files, "%s must embed files", s.Name)
		var hasMD bool
		for _, f := range s.Files {
			if filepath.Base(f) == "SKILL.md" {
				hasMD = true
			}
		}
		assert.True(t, hasMD, "%s must embed a SKILL.md", s.Name)
	}
}

// TestWriteSkills_WritesAndIsIdempotent: install writes every embedded file
// preserving the dir structure; a second run without --force overwrites
// nothing (returns no newly-written paths); --force rewrites them.
func TestWriteSkills_WritesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	skills, err := skillsfs.List()
	require.NoError(t, err)

	// First install: every file written.
	written, err := writeSkills(skills, dir, false)
	require.NoError(t, err)
	var total int
	for _, s := range skills {
		total += len(s.Files)
	}
	assert.Len(t, written, total, "first install writes every embedded file")
	for _, p := range written {
		_, statErr := os.Stat(p)
		assert.NoError(t, statErr, "written file must exist: %s", p)
	}

	// Second install without --force: nothing overwritten.
	again, err := writeSkills(skills, dir, false)
	require.NoError(t, err)
	assert.Empty(t, again, "without --force, existing files are left untouched")

	// With --force: all rewritten.
	forced, err := writeSkills(skills, dir, true)
	require.NoError(t, err)
	assert.Len(t, forced, total, "--force rewrites every file")
}

// TestResolveDir: explicit --dir wins; empty falls back to ~/.claude/skills.
func TestResolveDir(t *testing.T) {
	got, err := resolveDir("/custom/path")
	require.NoError(t, err)
	assert.Equal(t, "/custom/path", got)

	def, err := resolveDir("")
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(def), "default dir must be absolute")
	assert.Contains(t, def, filepath.Join(".claude", "skills"))
}
