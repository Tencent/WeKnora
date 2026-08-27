package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox"
)

// A model that probes an installed skill with a bare interpreter reads the
// resulting import failure as "this skill is broken" and reinstalls the
// packages into a session sandbox that is thrown away. read_skill has to hand
// it the way in for both languages, and say why a bare probe proves nothing.
//
// The two need different answers: Python is reached by naming the virtualenv's
// interpreter, Node by moving the working directory into the skill, because
// Node resolves from the importing file rather than from the interpreter.
func TestSkillEnvironmentSectionNamesTheWayIntoBothEnvironments(t *testing.T) {
	t.Parallel()

	dir := sandbox.SkillsImageRoot + "/smart-charts"
	section := skillEnvironmentSection(dir)

	require.Contains(t, section, dir)
	require.Contains(t, section, sandbox.SkillVenvPython(dir))
	require.Contains(t, section, "node_modules")
	require.Contains(t, strings.ToLower(section), "pip install")
	require.Contains(t, strings.ToLower(section), "npm install")
}
