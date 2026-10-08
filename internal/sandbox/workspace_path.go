package sandbox

import (
	"path"
	"strings"
)

// ResolveWorkspacePathIn gives file tools the same relative-path semantics as
// commands, anchored at the session's workspace root. This normalizes only;
// callers still enforce their read/write roots.
func ResolveWorkspacePathIn(l WorkspaceLayout, value string) string {
	value = strings.TrimSpace(value)
	if !path.IsAbs(value) {
		root := l.Root
		if root == "" {
			root = RemoteWorkspaceLayout().Root
		}
		return path.Join(root, value)
	}
	return path.Clean(value)
}
