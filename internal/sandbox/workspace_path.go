package sandbox

import (
	"path"
	"strings"
)

// ResolveWorkspacePath gives file tools the same relative-path semantics as
// commands. This normalizes only; callers still enforce their read/write roots.
func ResolveWorkspacePath(value string) string {
	value = strings.TrimSpace(value)
	if !path.IsAbs(value) {
		return path.Join(SessionWorkspaceRoot, value)
	}
	return path.Clean(value)
}
