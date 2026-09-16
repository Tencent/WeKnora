package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func readFileSkills(t *testing.T) (*skills.Manager, string) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"allowed", "other"} {
		dir := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(dir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Test file resources\n---\n# Instructions\nUse the bundled guide.\n"), 0644))
	}
	mgr := skills.NewManager(&skills.ManagerConfig{Enabled: true, SkillDirs: []string{root}, AllowedSkills: []string{"allowed"}}, sandbox.NewDisabledManager())
	require.NoError(t, mgr.Initialize(context.Background()))
	return mgr, filepath.Join(root, "allowed")
}

func TestReadFileCombinesSourcesWithoutGrantingHostAccess(t *testing.T) {
	mgr, dir := readFileSkills(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "guide.txt"), []byte("bundled guide\n"), 0644))
	outside := filepath.Join(t.TempDir(), "private.txt")
	require.NoError(t, os.WriteFile(outside, []byte("host secret"), 0644))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "link.txt")))
	source := &fakeSandboxFileSource{stat: &sandbox.RemoteStatEntry{Type: sandbox.RemoteEntryFile, Size: 10}, data: []byte("workspace\n")}
	reader := NewReadFileTool(source).WithSkills(mgr, false)
	registry := NewToolRegistry()
	registry.RegisterTool(reader)
	read := func(address string) *types.ToolResult {
		args, err := json.Marshal(ReadFileInput{Path: address})
		require.NoError(t, err)
		result, err := registry.ExecuteTool(sandboxFileTestContext(), ToolReadFile, args)
		require.NoError(t, err)
		return result
	}
	result := read("input/file.txt")
	require.True(t, result.Success)
	require.Equal(t, "/workspace/input/file.txt", result.Data["path"])
	require.Contains(t, result.Output, "workspace")
	result = read("skill://allowed/SKILL.md")
	require.True(t, result.Success)
	require.Contains(t, result.Output, "Use the bundled guide")
	require.Contains(t, result.Output, "guide.txt")
	require.Contains(t, result.Output, "Execution is unavailable")
	require.NotContains(t, result.Output, dir, "host paths are not execution paths")
	require.NotContains(t, result.Data, "instructions")
	require.NotContains(t, result.Data, "content")
	result = read("skill://allowed/guide.txt")
	require.True(t, result.Success)
	require.Contains(t, result.Output, "bundled guide")
	for _, address := range []string{outside, "skill://other/SKILL.md", "skill://unknown/SKILL.md", "skill://allowed/../other/SKILL.md", "skill://allowed//guide.txt", "skill://allowed/link.txt"} {
		result := read(address)
		require.False(t, result.Success, address)
		require.NotContains(t, result.Output, "host secret")
	}
	require.Equal(t, 1, source.readCalls, "skill and invalid paths must never touch workspace storage")
}

func TestReadFileSkillPagesPreserveEveryLineAndSuppressBinary(t *testing.T) {
	mgr, dir := readFileSkills(t)
	var content strings.Builder
	for i := 0; i < 250; i++ {
		fmt.Fprintf(&content, "line-%03d-%s\n", i, strings.Repeat("文", 50))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "guide.txt"), []byte(content.String()), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "image.bin"), []byte{0, 1, 2, 3}, 0644))
	registry := NewToolRegistry()
	registry.RegisterTool(NewReadFileTool(nil).WithSkills(mgr, false))
	var collected strings.Builder
	for offset, count := 1, 0; ; count++ {
		require.Less(t, count, 100)
		args, err := json.Marshal(ReadFileInput{Path: "skill://allowed/guide.txt", Offset: offset, MaxBytes: 2048})
		require.NoError(t, err)
		// Skill-only reading must not require a session or create a sandbox.
		result, err := registry.ExecuteTool(context.Background(), ToolReadFile, args)
		require.NoError(t, err)
		require.True(t, result.Success, "%+v", result)
		body := strings.SplitN(result.Output, "```\n", 2)[1]
		collected.WriteString(strings.SplitN(body, "```\n", 2)[0])
		next, more := result.Data["next_offset"].(int)
		if !more {
			break
		}
		require.Greater(t, next, offset)
		offset = next
	}
	require.Equal(t, content.String(), collected.String())
	result, err := registry.ExecuteTool(context.Background(), ToolReadFile, json.RawMessage(`{"path":"skill://allowed/image.bin"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, true, result.Data["binary"])
	require.Equal(t, 0, result.Data["returned_bytes"])
}

func TestReadFileInstalledSkillSelectsShellAndDisabledSkillsStayHidden(t *testing.T) {
	mgr := shellTestSkillEnvironment(t)
	reader := NewReadFileTool(nil).WithSkills(mgr, true)
	result, err := reader.Execute(context.Background(), json.RawMessage(`{"path":"skill://pdf-tools/SKILL.md"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Contains(t, result.Output, `shell_exec(skill_name="pdf-tools"`)
	require.NotContains(t, result.Output, "execute_skill_script")
	result, err = NewReadFileTool(nil).Execute(context.Background(), json.RawMessage(`{"path":"skill://pdf-tools/SKILL.md"}`))
	require.NoError(t, err)
	require.False(t, result.Success)
}
