package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatSkillFileTreeIndentsSharedDirectories(t *testing.T) {
	t.Parallel()

	got := formatSkillFileTree([]string{
		"SKILL.md",
		"LICENSE",
		"scripts/generate_ppt.py",
		"scripts/helpers/layout.py",
		"docs/guide.md",
		"scripts/office/validate.py",
	})

	want := strings.Join([]string{
		"docs/",
		"  guide.md",
		"scripts/",
		"  helpers/",
		"    layout.py",
		"  office/",
		"    validate.py",
		"  generate_ppt.py",
		"LICENSE",
		"",
	}, "\n")
	assert.Equal(t, want, got)

	// The prefix is paid once; the old bullet list repeated `scripts/` on
	// every line plus backticks and "(script - can be executed)".
	assert.Equal(t, 1, strings.Count(got, "scripts/"))
	assert.NotContains(t, got, "`")
	assert.NotContains(t, got, "can be executed")
}

func TestFormatSkillFileTreeSkipsInstallAndCacheTrees(t *testing.T) {
	t.Parallel()

	got := formatSkillFileTree([]string{
		"scripts/run.py",
		".venv/bin/python",
		".venv/lib/site.py",
		"node_modules/pptx/index.js",
		"scripts/__pycache__/run.cpython-312.pyc",
		".git/HEAD",
	})
	assert.Equal(t, "scripts/\n  run.py\n", got)
}

func TestFormatSkillFileTreeEmptyWhenOnlySkillMd(t *testing.T) {
	t.Parallel()

	assert.Empty(t, formatSkillFileTree(nil))
	assert.Empty(t, formatSkillFileTree([]string{"SKILL.md", "", "/"}))
}

func TestFormatSkillFileTreeAcceptsWindowsSeparators(t *testing.T) {
	t.Parallel()

	got := formatSkillFileTree([]string{`scripts\office\validate.py`})
	require.Equal(t, "scripts/\n  office/\n    validate.py\n", got)
}
