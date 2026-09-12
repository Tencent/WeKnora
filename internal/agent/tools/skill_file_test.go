package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSkillDir = sandbox.SkillsImageRoot + "/pdf-tools"

type fakeSkillFileStore struct {
	existing map[string][]byte
	stat     *sandbox.RemoteStatEntry
	statErr  error
	writeErr error

	writes []string
	last   []byte
}

func (f *fakeSkillFileStore) StatSessionFile(
	_ context.Context, _, filePath string,
) (*sandbox.RemoteStatEntry, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	if f.stat != nil {
		return f.stat, nil
	}
	if _, ok := f.existing[filePath]; !ok {
		return nil, errors.New("no such file")
	}
	return &sandbox.RemoteStatEntry{Path: filePath, Type: sandbox.RemoteEntryFile}, nil
}

func (f *fakeSkillFileStore) ReadSessionFile(
	_ context.Context, _, filePath string,
) ([]byte, error) {
	raw, ok := f.existing[filePath]
	if !ok {
		return nil, errors.New("no such file")
	}
	return append([]byte(nil), raw...), nil
}

func (f *fakeSkillFileStore) WriteSessionFile(
	_ context.Context, _, filePath string, content []byte,
) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.writes = append(f.writes, filePath)
	f.last = append([]byte(nil), content...)
	if f.existing == nil {
		f.existing = map[string][]byte{}
	}
	f.existing[filePath] = append([]byte(nil), content...)
	return nil
}

// The reason these tools exist: the installer's only writer was `cat` with a
// heredoc, so anything past the shell's command-length cap arrived truncated.
func TestWriteSkillFileWritesAFileTooLargeForAHeredoc(t *testing.T) {
	content := strings.Repeat("# a line of a generated wrapper script\n", 500)
	require.Greater(t, len(content), 8*1024)
	store := &fakeSkillFileStore{}

	result, err := NewWriteSkillFileTool(store, testSkillDir).Execute(
		sandboxFileTestContext(),
		mustWriteSandboxArgs(testSkillDir+"/.weknora/requirements.json", content),
	)

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, []string{testSkillDir + "/.weknora/requirements.json"}, store.writes)
	assert.Equal(t, content, string(store.last))
	assert.NotContains(t, result.Output, content, "the model already has these bytes")
}

// The model is told the skill directory once, then reaches for paths relative
// to it. Resolving them beats refusing a call that meant the right file.
func TestWriteSkillFileResolvesARelativePathAgainstTheSkillDirectory(t *testing.T) {
	store := &fakeSkillFileStore{}

	result, err := NewWriteSkillFileTool(store, testSkillDir).Execute(
		sandboxFileTestContext(),
		mustWriteSandboxArgs(".weknora/requirements.json", `{"env":[]}`),
	)

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, []string{testSkillDir + "/.weknora/requirements.json"}, store.writes)
}

// One install writes one skill. The installer's shell runs as root in an image
// every session of the config inherits, so reaching a neighbouring skill has to
// fail in the tool rather than be discouraged by the prompt.
func TestSkillFileToolsRefuseEveryPathOutsideTheirOwnSkill(t *testing.T) {
	for _, requested := range []string{
		sandbox.SkillsImageRoot + "/other-skill/SKILL.md",
		sandbox.SkillsImageRoot + "/.manifest.json",
		sandbox.SkillsImageRoot,
		testSkillDir + "/../other-skill/run.py",
		testSkillDir + "/./../.manifest.json",
		"../other-skill/run.py",
		"/etc/passwd",
		"/workspace/output/run.py",
		testSkillDir,
		"",
	} {
		t.Run(requested, func(t *testing.T) {
			store := &fakeSkillFileStore{}

			result, err := NewWriteSkillFileTool(store, testSkillDir).Execute(
				sandboxFileTestContext(), mustWriteSandboxArgs(requested, "x = 1\n"))

			require.NoError(t, err)
			require.False(t, result.Success,
				"an install must not be able to write %q", requested)
			assert.Empty(t, store.writes, "the refusal must happen before any write")
		})
	}
}

func TestEditSkillFileReplacesASnippetInPlace(t *testing.T) {
	store := &fakeSkillFileStore{existing: map[string][]byte{
		testSkillDir + "/scripts/run.py": []byte("import sys\nLIB = '/wrong/path'\n"),
	}}

	args, err := json.Marshal(map[string]any{
		"path":       "scripts/run.py",
		"old_string": "'/wrong/path'",
		"new_string": "'" + testSkillDir + "/lib'",
	})
	require.NoError(t, err)

	result, err := NewEditSkillFileTool(store, testSkillDir).Execute(
		sandboxFileTestContext(), args)

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, 1, result.Data["replacements"])
	assert.Contains(t, string(store.last), testSkillDir+"/lib")
}

// A nested-quote break is reported but the write still lands, so the next call
// can be an edit rather than a rewrite of the whole file. The hint has to name
// edit_skill_file: edit_sandbox_file cannot reach this tree.
func TestWriteSkillFileFlagsNestedPythonQuotesAndStillWrites(t *testing.T) {
	store := &fakeSkillFileStore{}

	result, err := NewWriteSkillFileTool(store, testSkillDir).Execute(
		sandboxFileTestContext(),
		mustWriteSandboxArgs("scripts/run.py",
			"title = \"这不是一个\"大干快上\"的夜晚\"\n"))

	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Equal(t, true, result.Data["syntax_error"])
	assert.Contains(t, result.Error, ToolEditSkillFile)
	assert.Len(t, store.writes, 1, "edit_skill_file needs the file to exist")
}

// These write the shared snapshot image, so they are granted by install mode
// alone. A checkbox for them in the settings UI would turn "can edit an agent"
// into "can write the image every session of this config inherits", which is a
// different permission from "can upload a skill".
func TestSkillFileToolsAreNotSelectableOnATenantAgent(t *testing.T) {
	for _, name := range []string{ToolWriteSkillFile, ToolEditSkillFile} {
		require.NotContains(t, DefaultAllowedTools(), name)
		for _, definition := range AvailableToolDefinitions() {
			require.NotEqual(t, name, definition.Name,
				"a tenant-editable agent must not be able to request %s", name)
		}
	}
}

func TestSkillFileToolsRefuseToWriteWhenBoundToNoSkillDirectory(t *testing.T) {
	for _, dir := range []string{"", "/", "."} {
		store := &fakeSkillFileStore{}

		result, err := NewWriteSkillFileTool(store, dir).Execute(
			sandboxFileTestContext(), mustWriteSandboxArgs("/etc/passwd", "x"))

		require.NoError(t, err)
		require.False(t, result.Success, "skillDir %q must not authorise any write", dir)
		assert.Empty(t, store.writes)
	}
}
