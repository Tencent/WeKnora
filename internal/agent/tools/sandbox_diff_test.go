package tools

import (
	"strings"
	"testing"
)

func TestSanitizeSandboxFileCallArgsStripsWriteBody(t *testing.T) {
	content := strings.Repeat("x\n", 15)
	out := SanitizeSandboxFileCallArgs(ToolWriteSandboxFile, map[string]any{
		"path":    "/workspace/output/a.py",
		"content": content,
		"mode":    "overwrite",
	})
	if _, ok := out["content"]; ok {
		t.Fatal("write progress must not carry the file body")
	}
	if out["path"] != "/workspace/output/a.py" {
		t.Fatalf("path = %v", out["path"])
	}
	if out["added_lines"] != 15 {
		t.Fatalf("added_lines = %v, want 15", out["added_lines"])
	}
	if out["removed_lines"] != 0 {
		t.Fatalf("removed_lines = %v", out["removed_lines"])
	}
	preview, _ := out["preview"].(string)
	if strings.Count(preview, "\n")+1 != sandboxFilePreviewMaxLines {
		t.Fatalf("preview lines = %q", preview)
	}
}

func TestSanitizeSandboxFileCallArgsStripsEditBodies(t *testing.T) {
	out := SanitizeSandboxFileCallArgs(ToolEditSandboxFile, map[string]any{
		"path": "/workspace/a.py",
		"edits": []any{
			map[string]any{"old_string": "a\nb\n", "new_string": "a\nb\nc\n"},
		},
	})
	if _, ok := out["edits"]; ok {
		t.Fatal("edit progress must not carry edits")
	}
	if out["removed_lines"] != 2 {
		t.Fatalf("removed_lines = %v, want 2", out["removed_lines"])
	}
	if out["added_lines"] != 3 {
		t.Fatalf("added_lines = %v, want 3", out["added_lines"])
	}
}

func TestSanitizeSandboxFileCallArgsLeavesOtherToolsAlone(t *testing.T) {
	args := map[string]any{"command": "ls"}
	out := SanitizeSandboxFileCallArgs(ToolShellExec, args)
	if out["command"] != "ls" {
		t.Fatalf("unrelated args = %#v", out)
	}
}
