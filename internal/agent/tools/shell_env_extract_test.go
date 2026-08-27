package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractExportedEnvFromExportAndPrefixAssignment(t *testing.T) {
	command := `unset HTTP_PROXY; export BIGMODEL_API_KEY="sk-abc.def"; cd /workspace && ./run`

	got := extractExportedEnv(command)

	require.Equal(t, "sk-abc.def", got["BIGMODEL_API_KEY"])
	assert.NotContains(t, got, "HTTP_PROXY")
}

func TestExtractExportedEnvKeepsUnquotedAndSingleQuotedValues(t *testing.T) {
	command := `export FOO=bare-value; export BAR='quoted value'; BAZ=prefix ./cmd`

	got := extractExportedEnv(command)

	assert.Equal(t, "bare-value", got["FOO"])
	assert.Equal(t, "quoted value", got["BAR"])
	assert.Equal(t, "prefix", got["BAZ"])
}

func TestExtractExportedEnvIgnoresFlagsAndURLs(t *testing.T) {
	command := `python generate.py --model cogview-4 --size 1792x1024 https://example.com/x`

	got := extractExportedEnv(command)

	assert.Empty(t, got)
}

func TestCollectUsedSkillEnvOverlaysToolEnvOnExports(t *testing.T) {
	command := `export BIGMODEL_API_KEY="from-command"; cd /opt/weknora/tenant/skills/bigmodel-image-video && python x.py`
	toolEnv := map[string]string{"BIGMODEL_API_KEY": "from-tool", "EXTRA": "e"}

	got := collectUsedSkillEnv(command, toolEnv)

	assert.Equal(t, "from-tool", got["BIGMODEL_API_KEY"])
	assert.Equal(t, "e", got["EXTRA"])
}

func TestInferSkillNamePrefersExplicitToolField(t *testing.T) {
	command := `cd /opt/weknora/tenant/skills/other-skill && python x.py`

	assert.Equal(t, "pdf-tools", inferSkillName("pdf-tools", command))
}

func TestInferSkillNameFromCdIntoSkillsRoot(t *testing.T) {
	command := `cd /opt/weknora/tenant/skills/bigmodel-image-video && .venv/bin/python scripts/generate.py "草原"`

	assert.Equal(t, "bigmodel-image-video", inferSkillName("", command))
}

func TestInferSkillNameFromScriptPath(t *testing.T) {
	command := `/opt/weknora/tenant/skills/bigmodel-image-video/.venv/bin/python /opt/weknora/tenant/skills/bigmodel-image-video/scripts/generate.py`

	assert.Equal(t, "bigmodel-image-video", inferSkillName("", command))
}

func TestInferSkillNameEmptyWhenCommandDoesNotTouchSkillsRoot(t *testing.T) {
	assert.Empty(t, inferSkillName("", "ls /workspace"))
}
