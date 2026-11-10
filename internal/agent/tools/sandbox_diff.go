package tools

import (
	"fmt"
	"strings"
)

const sandboxFilePreviewMaxLines = 10

// CountContentLines is the +N / -M unit for sandbox file mutations: empty is
// 0, a trailing newline does not add an extra line.
func CountContentLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if s[len(s)-1] != '\n' {
		n++
	}
	return n
}

func sandboxContentPreview(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	end := len(lines)
	if end > 0 && lines[end-1] == "" {
		end--
	}
	lines = lines[:end]
	if len(lines) > sandboxFilePreviewMaxLines {
		lines = lines[:sandboxFilePreviewMaxLines]
	}
	return strings.Join(lines, "\n")
}

func isSandboxMutationTool(name string) bool {
	return name == ToolWriteSandboxFile || name == ToolEditSandboxFile
}

func attachSandboxDiffStats(data map[string]interface{}, added, removed int) {
	if data == nil {
		return
	}
	data["added_lines"] = added
	data["removed_lines"] = removed
}

func sandboxEditDiffStats(content string, edits []SandboxEdit) (added, removed int) {
	for _, e := range edits {
		found := indexAllNonOverlapping(content, e.OldString)
		n := len(found)
		if n == 0 {
			continue
		}
		if !e.ReplaceAll {
			n = 1
		}
		removed += n * CountContentLines(e.OldString)
		added += n * CountContentLines(e.NewString)
	}
	return added, removed
}

func editArgsLineStats(args map[string]any) (added, removed int) {
	edits, _ := args["edits"].([]any)
	for _, raw := range edits {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		oldS := stringArg(m, "old_string", "oldText")
		newS := stringArg(m, "new_string", "newText")
		removed += CountContentLines(oldS)
		added += CountContentLines(newS)
	}
	if added == 0 && removed == 0 {
		removed = CountContentLines(stringArg(args, "old_string", "oldText"))
		added = CountContentLines(stringArg(args, "new_string", "newText"))
	}
	return added, removed
}

func stringArg(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := m[key].(string); ok {
			return s
		}
	}
	return ""
}

// SandboxFileCallProgress is the UI payload for a write/edit call: path,
// running +/− line counts, and a short preview. File bodies stay off the wire.
func SandboxFileCallProgress(toolName string, args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	out := map[string]any{}
	if path := stringArg(args, "path", "file_path"); path != "" {
		out["path"] = path
	}
	switch toolName {
	case ToolWriteSandboxFile:
		content := stringArg(args, "content")
		if mode := stringArg(args, "mode"); mode != "" {
			out["mode"] = mode
		}
		out["added_lines"] = CountContentLines(content)
		out["removed_lines"] = 0
		out["bytes"] = len(content)
		if preview := sandboxContentPreview(content); preview != "" {
			out["preview"] = preview
		}
	case ToolEditSandboxFile:
		added, removed := editArgsLineStats(args)
		out["added_lines"] = added
		out["removed_lines"] = removed
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SanitizeSandboxFileCallArgs strips write/edit file bodies from arguments
// sent to the client. Live progress events already carry stats only; this
// covers the later "tool hint" emit that still has the full JSON.
func SanitizeSandboxFileCallArgs(toolName string, args map[string]any) map[string]any {
	if !isSandboxMutationTool(toolName) {
		return args
	}
	if args == nil {
		return nil
	}
	if _, hasContent := args["content"]; hasContent {
		return SandboxFileCallProgress(toolName, args)
	}
	if _, hasEdits := args["edits"]; hasEdits {
		return SandboxFileCallProgress(toolName, args)
	}
	return args
}

func formatSandboxDiffStat(added, removed int) string {
	switch {
	case added > 0 && removed > 0:
		return fmt.Sprintf("+%d -%d", added, removed)
	case added > 0:
		return fmt.Sprintf("+%d", added)
	case removed > 0:
		return fmt.Sprintf("-%d", removed)
	default:
		return ""
	}
}
