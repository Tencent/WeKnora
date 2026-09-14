package compaction

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/stretchr/testify/assert"
)

func toolCallMsg(name, arguments string) chat.Message {
	return chat.Message{
		Role: "assistant",
		ToolCalls: []chat.ToolCall{{
			ID:       "call-1",
			Type:     "function",
			Function: chat.FunctionCall{Name: name, Arguments: arguments},
		}},
	}
}

func TestResolveSplitsReadsFromWrites(t *testing.T) {
	read, modified := extractFileOps("", []chat.Message{
		toolCallMsg("write_sandbox_file", `{"path":"/workspace/output/deck.html","content":"<html>"}`),
		toolCallMsg("edit_sandbox_file", `{"path":"/workspace/output/deck.html","old_string":"a"}`),
		toolCallMsg("read_file", `{"path":"/workspace/input/notes.txt"}`),
		toolCallMsg("read_file", `{"path":"skill://pdf/SKILL.md"}`),
		toolCallMsg("shell_exec", `{"command":"ls"}`),
	}).resolve()

	// Written then edited is one entry, not two.
	assert.Equal(t, []string{"/workspace/output/deck.html"}, modified)
	assert.Equal(t, []string{"/workspace/input/notes.txt", "skill://pdf/SKILL.md"}, read)
}

// A file the agent read and then rewrote is only interesting as something it
// modified; listing it twice invites the model to re-read its own output.
func TestResolveDropsReadsThatWereAlsoWritten(t *testing.T) {
	read, modified := extractFileOps("", []chat.Message{
		toolCallMsg("read_sandbox_file", `{"path":"/workspace/output/deck.html"}`),
		toolCallMsg("write_sandbox_file", `{"path":"/workspace/output/deck.html","content":"x"}`),
	}).resolve()

	assert.Equal(t, []string{"/workspace/output/deck.html"}, modified)
	assert.Empty(t, read)
}

// Truncated arguments are the normal case for the calls this feature exists
// to survive, so they must be skipped rather than poison the list.
func TestExtractFileOpsIgnoresUnparseableArguments(t *testing.T) {
	read, modified := extractFileOps("", []chat.Message{
		toolCallMsg("write_sandbox_file", `{"path":"/workspace/output/a.html","content":"<htm`),
		toolCallMsg("write_sandbox_file", `{"content":"no path here"}`),
	}).resolve()

	assert.Empty(t, modified)
	assert.Empty(t, read)
}

// Without inheritance the second compaction forgets what the first one
// recorded, which is the whole failure mode this guards against. The prior
// block now arrives via the previous summary rather than as a message, because
// summaries no longer re-enter their successor's input.
func TestExtractFileOpsInheritsFromPreviousSummary(t *testing.T) {
	earlier := fileOps{
		written: []string{"/workspace/output/deck.html"},
		read:    []string{"/workspace/input/notes.txt"},
	}

	read, modified := extractFileOps("prose"+earlier.format(), []chat.Message{
		toolCallMsg("write_sandbox_file", `{"path":"/workspace/output/report.md","content":"x"}`),
	}).resolve()

	assert.Equal(t, []string{"/workspace/output/deck.html", "/workspace/output/report.md"}, modified)
	assert.Equal(t, []string{"/workspace/input/notes.txt"}, read)
}

func TestFileOpsFormatIsEmptyWhenNothingTouched(t *testing.T) {
	assert.Empty(t, fileOps{}.format())
}

func TestFileOpsCapsTrackedPaths(t *testing.T) {
	msgs := make([]chat.Message, 0, maxTrackedFilePaths+10)
	for i := 0; i < maxTrackedFilePaths+10; i++ {
		msgs = append(msgs, toolCallMsg("write_sandbox_file",
			`{"path":"/workspace/output/`+strings.Repeat("a", i+1)+`.txt"}`))
	}

	_, modified := extractFileOps("", msgs).resolve()
	assert.Len(t, modified, maxTrackedFilePaths)
}
