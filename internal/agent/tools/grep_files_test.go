package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type testMemoryResources struct {
	interfaces.MemoryService
	files    map[string]string
	disabled bool
}

func (s *testMemoryResources) MemoryResources(context.Context) (map[string]string, error) {
	if s.disabled {
		return nil, fmt.Errorf("disabled")
	}
	return s.files, nil
}

func (s *testMemoryResources) ReadMemoryResource(_ context.Context, path string) (string, error) {
	if s.disabled {
		return "", fmt.Errorf("disabled")
	}
	v, ok := s.files[path]
	if !ok {
		return "", fmt.Errorf("not found")
	}
	return v, nil
}

type testOutputStore struct{ memoryWebPages }

func (s *testOutputStore) Save(_ context.Context, text string) (string, error) {
	if s.fail {
		return "", fmt.Errorf("storage unavailable")
	}
	p := fmt.Sprintf("output://log-%d", len(s.pages))
	s.pages[p] = text
	return p, nil
}

type longResultTool struct {
	BaseTool
	result *types.ToolResult
}

func (t *longResultTool) Execute(context.Context, json.RawMessage) (*types.ToolResult, error) {
	return t.result, nil
}

func TestGrepMemoryFullTextAndContinuation(t *testing.T) {
	memory := &testMemoryResources{files: map[string]string{
		"memory://items/a.md": "intro\nERROR 1\ncontext\nerror 2\n",
		"memory://items/b.md": strings.Repeat("long ", 1000) + "ERROR 3",
	}}
	reader := NewReadFileTool(nil).WithMemory(memory)
	tool := NewGrepFilesTool(reader)
	require.Equal(t, "read", reader.Name())
	require.Equal(t, "grep", tool.Name())
	first, err := tool.Execute(
		t.Context(),
		[]byte(`{"path":"memory://","pattern":"error","ignore_case":true,"limit":1}`),
	)
	require.NoError(t, err)
	require.True(t, first.Success)
	require.Contains(t, first.Output, "a.md:2:")
	require.Equal(t, 1, first.Data["next_offset"])
	second, err := tool.Execute(t.Context(),
		[]byte(`{"path":"memory://","pattern":"error","ignore_case":true,"offset":1,"limit":2}`))
	require.NoError(t, err)
	require.Contains(t, second.Output, "error 2")
	require.Contains(t, second.Output, "ERROR 3")
	require.Equal(t, false, second.Data["truncated"])
	memory.disabled = true
	denied, _ := tool.Execute(t.Context(), []byte(`{"path":"memory://","pattern":"error"}`))
	require.False(t, denied.Success)
}

func TestGrepRejectsUnreachableSourcesAndInvalidRegex(t *testing.T) {
	tool := NewGrepFilesTool(NewReadFileTool(nil).WithMemory(&testMemoryResources{files: map[string]string{}}))
	for _, args := range []string{
		`{"path":"memory://","pattern":"("}`,
		`{"path":"memory://../other","pattern":"a"}`,
		`{"path":"/etc/passwd","pattern":"root"}`,
		`{"path":"skill://unknown/","pattern":"a"}`,
		`{"path":"output://unknown","pattern":"a"}`,
	} {
		result, err := tool.Execute(t.Context(), []byte(args))
		require.NoError(t, err)
		require.False(t, result.Success, args)
	}
}

func TestLongToolOutputSavedBeforeTruncationAndReadableWithoutReexecution(t *testing.T) {
	store := &testOutputStore{memoryWebPages: memoryWebPages{pages: map[string]string{}}}
	original := strings.Repeat("begin\n", 1000) + "UNIQUE_MIDDLE_FAILURE\n" + strings.Repeat("end\n", 1000)
	registry := NewToolRegistry()
	registry.SetMaxToolOutputSize(2048)
	registry.SetOutputSource(store)
	resultTool := &longResultTool{
		BaseTool: NewBaseTool("test_long", "", nil),
		result:   &types.ToolResult{Success: true, Output: original},
	}
	registry.RegisterTool(resultTool)
	result, err := registry.ExecuteTool(t.Context(), "test_long", []byte(`{}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.LessOrEqual(t, utf8.RuneCountInString(result.Output), 2048)
	saved := result.Data["full_output_path"].(string)
	require.Contains(t, store.pages[saved], original)
	require.NotContains(t, result.Output, "UNIQUE_MIDDLE_FAILURE")
	grep := NewGrepFilesTool(NewReadFileTool(nil).WithOutputs(store))
	args, _ := json.Marshal(GrepFilesInput{Path: saved, Pattern: "UNIQUE_MIDDLE_FAILURE"})
	hit, err := grep.Execute(t.Context(), args)
	require.NoError(t, err)
	require.Contains(t, hit.Output, "UNIQUE_MIDDLE_FAILURE")
	encoded := modelcontext.NewRegistry(true).ModelToolResultForTool("knowledge_search", &types.ToolResult{
		Success: true, Output: result.Output,
		Data: map[string]interface{}{
			"output_truncated": true, "full_output_path": saved, "display_type": "search_results",
			"results": []map[string]interface{}{
				{"content": strings.Repeat("EXPANDED_DATA", 1000), "chunk_id": "chunk"},
			},
		},
	})
	require.Contains(t, encoded, saved)
	require.LessOrEqual(t, utf8.RuneCountInString(encoded), 2048)
	require.NotContains(t, encoded, "EXPANDED_DATA")
	store.fail = true
	resultTool.result = &types.ToolResult{Success: true, Output: original}
	failed, err := registry.ExecuteTool(t.Context(), "test_long", []byte(`{}`))
	require.NoError(t, err)
	require.True(t, failed.Success)
	require.Contains(t, failed.Output, "could not be saved")
	require.LessOrEqual(t, utf8.RuneCountInString(failed.Output), 2048)
}

func TestShellSavesCapturedStreamsBeforeItsOwnTruncation(t *testing.T) {
	store := &testOutputStore{memoryWebPages: memoryWebPages{pages: map[string]string{}}}
	stdout := strings.Repeat("x", 10000) + "MIDDLE_LOG" + strings.Repeat("x", 10000)
	executor := &fakeShellExecutor{result: &sandbox.ExecuteResult{Stdout: stdout, Stderr: "stderr", ExitCode: 0}}
	tool := NewShellExecTool(executor, nil).WithOutputSource(store)
	result, err := tool.Execute(sandboxFileTestContext(), []byte(`{"command":"run-once","max_output_bytes":1024}`))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	saved := result.Data["full_output_path"].(string)
	require.Contains(t, store.pages[saved], "MIDDLE_LOG")
	require.Equal(t, 1, executor.calls)
}

func TestWikiTruncatedPageCanBeReadFromSnapshot(t *testing.T) {
	page := newTestWikiPage("kb-1", "concept/large")
	page.Content = strings.Repeat("begin\n", 1000) + "WIKI_MIDDLE_FACT\n" + strings.Repeat("end\n", 1000)
	service := &fakeWikiPageService{pages: map[string]*types.WikiPage{wikiPageKey("kb-1", page.Slug): page}}
	store := &testOutputStore{memoryWebPages: memoryWebPages{pages: map[string]string{}}}
	registry := NewToolRegistry()
	registry.SetMaxToolOutputSize(2048)
	registry.RegisterTool(NewWikiReadPageTool(
		service, nil, NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver(),
	))
	registry.SetOutputSource(store)
	result, err := registry.ExecuteTool(t.Context(), ToolWikiReadPage, []byte(`{"slugs":["concept/large"]}`))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	saved, ok := result.Data["full_output_path"].(string)
	require.True(t, ok)
	require.NotEmpty(t, saved)
	require.Contains(t, store.pages[saved], "WIKI_MIDDLE_FACT")
	require.LessOrEqual(t, utf8.RuneCountInString(result.Output), 2048)
}

func TestStructuredResultsCannotEvadeOutputBudget(t *testing.T) {
	store := &testOutputStore{memoryWebPages: memoryWebPages{pages: map[string]string{}}}
	registry := NewToolRegistry()
	registry.SetMaxToolOutputSize(2048)
	registry.SetOutputSource(store)
	registry.RegisterTool(&longResultTool{
		BaseTool: NewBaseTool("knowledge_search", "", nil),
		result: &types.ToolResult{
			Success: true, Output: "a short summary",
			Data: map[string]interface{}{
				"display_type": "search_results",
				"results": []map[string]interface{}{
					{"chunk_id": "id", "content": strings.Repeat("long-content", 1000)},
				},
			},
		},
	})
	result, err := registry.ExecuteTool(t.Context(), "knowledge_search", []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, true, result.Data["output_truncated"])
	saved := result.Data["full_output_path"].(string)
	require.Contains(t, store.pages[saved], "long-content")
	visible := modelcontext.NewRegistry(true).ModelToolResultForTool("knowledge_search", result)
	require.NotContains(t, visible, "long-content")
	require.Contains(t, visible, saved)
}

func TestSkillGrepLineNumbersMatchReadRepresentation(t *testing.T) {
	manager, _ := readFileSkills(t)
	reader := NewReadFileTool(nil).WithSkills(manager, false)
	content, _, _, err := reader.loadSkillResource(t.Context(), ReadFileInput{Path: "skill://allowed/SKILL.md"})
	require.NoError(t, err)
	expectedLine := 0
	for i, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "Use the bundled guide.") {
			expectedLine = i + 1
			break
		}
	}
	require.Positive(t, expectedLine)
	result, err := NewGrepFilesTool(reader).Execute(t.Context(),
		[]byte(`{"path":"skill://allowed/","pattern":"Use the bundled guide","literal":true}`))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Contains(t, result.Output, fmt.Sprintf("skill://allowed/SKILL.md:%d:", expectedLine))
	args, _ := json.Marshal(ReadFileInput{Path: "skill://allowed/SKILL.md", Offset: expectedLine, Limit: 1})
	page, err := reader.Execute(t.Context(), args)
	require.NoError(t, err)
	require.Contains(t, page.Output, "Use the bundled guide.")
}
