// Package adapter connects the local sandbox to the agent's existing
// capability interfaces. It is the only package allowed to depend on both
// internal/localsandbox and internal/sandbox; neither of those depends on
// the other.
package adapter

import (
	"github.com/Tencent/WeKnora/internal/localsandbox"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

// LayoutFor maps a resolved workspace onto the layout the agent tools consume.
//
// The tools enforce this as a convention (what the model is told, and what the
// scope errors say). The real enforcement for host file operations is
// PathGuard inside the adapter's SessionFileStore, so a mistake here cannot
// widen actual access.
func LayoutFor(ws localsandbox.Workspace) sandbox.WorkspaceLayout {
	return sandbox.WorkspaceLayout{
		Origin:     sandbox.WorkspaceOriginHost,
		Root:       ws.Root,
		WriteRoots: []string{ws.Root},
		ReadRoots:  []string{ws.Root},
		Hint:       ws.Root,
	}
}
