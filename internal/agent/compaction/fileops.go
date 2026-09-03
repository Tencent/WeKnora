package compaction

// Sandbox file operations have to survive compaction.
//
// Dropping the history that says "I wrote /workspace/output/deck.html" leaves
// the model with no record of the artifacts it already produced, so it starts
// rebuilding files that are already on disk — and for a file large enough to
// need chunked writes, restarting is exactly the loop that never terminates.
// The LLM summary is supposed to carry this, but it is prose and a list of
// paths is the first detail a summarizer drops. So the paths are extracted
// mechanically, re-attached to every summary, and inherited by the next one.
//
// Three sets while collecting, two lists when rendering: a file that was
// written is reported only as modified even if it was also read.

import (
	"encoding/json"
	"fmt"
	"strings"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
)

const (
	readFilesTag     = "read-files"
	modifiedFilesTag = "modified-files"

	// maxTrackedFilePaths bounds what the block can add to every subsequent
	// request. A session touching more files than this has its oldest entries
	// dropped rather than growing the prompt without limit.
	maxTrackedFilePaths = 50
)

// fileOps is the set of sandbox paths the compacted history touched. Reads and
// writes are kept apart while collecting so a path can be reclassified when a
// later call writes something that was only read before.
type fileOps struct {
	read    []string
	written []string
	edited  []string
}

func appendUnique(list []string, path string) []string {
	path = strings.TrimSpace(path)
	if path == "" || len(list) >= maxTrackedFilePaths {
		return list
	}
	for _, existing := range list {
		if existing == path {
			return list
		}
	}
	return append(list, path)
}

// extractFileOps collects the sandbox paths the summarized-away messages read
// and wrote. previousSummary carries the block inherited from an earlier
// compaction, which is no longer part of the message range being summarized
// now that summaries are excluded from their own successor's input.
func extractFileOps(previousSummary string, groups ...[]chat.Message) fileOps {
	var ops fileOps
	ops.inherit(previousSummary)
	for _, group := range groups {
		for _, msg := range group {
			ops.inherit(msg.Content)
			for _, tc := range msg.ToolCalls {
				path := toolCallPath(tc.Function.Arguments)
				if path == "" {
					continue
				}
				switch tc.Function.Name {
				case agenttools.ToolWriteSandboxFile:
					ops.written = appendUnique(ops.written, path)
				case agenttools.ToolEditSandboxFile:
					ops.edited = appendUnique(ops.edited, path)
				case agenttools.ToolReadSandboxFile:
					ops.read = appendUnique(ops.read, path)
				}
			}
		}
	}
	return ops
}

// inherit folds an already-rendered block back into the collecting sets. Its
// modified list has been through resolve(), so it goes back in as written.
func (o *fileOps) inherit(content string) {
	if content == "" {
		return
	}
	inheritedRead, inheritedModified := parseFileOpsBlock(content)
	for _, p := range inheritedRead {
		o.read = appendUnique(o.read, p)
	}
	for _, p := range inheritedModified {
		o.written = appendUnique(o.written, p)
	}
}

// resolve collapses the three sets into what the model is shown: everything
// touched is "modified", and only files never written are listed as read.
func (o fileOps) resolve() (read, modified []string) {
	for _, p := range o.written {
		modified = appendUnique(modified, p)
	}
	for _, p := range o.edited {
		modified = appendUnique(modified, p)
	}
	for _, p := range o.read {
		if !contains(modified, p) {
			read = appendUnique(read, p)
		}
	}
	return read, modified
}

func contains(list []string, want string) bool {
	for _, p := range list {
		if p == want {
			return true
		}
	}
	return false
}

// toolCallPath pulls the `path` argument out of a tool call. Malformed or
// truncated arguments simply contribute nothing.
func toolCallPath(arguments string) string {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return ""
	}
	return strings.TrimSpace(args.Path)
}

// format renders the block appended to a summary, or "" when nothing was
// touched.
func (o fileOps) format() string {
	read, modified := o.resolve()
	if len(read) == 0 && len(modified) == 0 {
		return ""
	}
	var sb strings.Builder
	writeTagged(&sb, readFilesTag, read)
	writeTagged(&sb, modifiedFilesTag, modified)
	return sb.String()
}

func writeTagged(sb *strings.Builder, tag string, paths []string) {
	if len(paths) == 0 {
		return
	}
	fmt.Fprintf(sb, "\n\n<%s>\n%s\n</%s>", tag, strings.Join(paths, "\n"), tag)
}

// parseFileOpsBlock reads back what format wrote, so consecutive compactions
// accumulate instead of each forgetting the one before it.
func parseFileOpsBlock(content string) (read, modified []string) {
	return parseTagged(content, readFilesTag), parseTagged(content, modifiedFilesTag)
}

func parseTagged(content, tag string) []string {
	open, closeTag := "<"+tag+">", "</"+tag+">"
	start := strings.Index(content, open)
	if start < 0 {
		return nil
	}
	start += len(open)
	end := strings.Index(content[start:], closeTag)
	if end < 0 {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(content[start:start+end], "\n") {
		paths = appendUnique(paths, line)
	}
	return paths
}
