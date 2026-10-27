package workspace

import (
	"fmt"
	"path"
	"strings"
)

// ResolvePathIn anchors a relative path at the layout root.
func ResolvePathIn(l Layout, value string) string {
	value = strings.TrimSpace(value)
	if !path.IsAbs(value) {
		root := l.Root
		if root == "" {
			root = Root
		}
		return path.Join(root, value)
	}
	return path.Clean(value)
}

// IsUnder reports whether p is root or lies beneath it. Both must be clean.
func IsUnder(p, root string) bool {
	if root == "/" {
		return strings.HasPrefix(p, "/")
	}
	return p == root || strings.HasPrefix(p, root+"/")
}

func underAny(p string, roots []string) bool {
	for _, root := range roots {
		if IsUnder(p, root) {
			return true
		}
	}
	return false
}

// ReadOnlyInputRoot returns the attachment root containing a clean absolute
// path. Shared machines reserve every session's input tree, not only the
// caller's: widening file access must not make another session's originals
// writable. This is a lexical tool rule, not an OS permission boundary.
func (l Layout) ReadOnlyInputRoot(clean string) string {
	if l.InputDir != "" && IsUnder(clean, l.InputDir) {
		return l.InputDir
	}
	if !l.IsSharedRemote() {
		return ""
	}
	if IsUnder(clean, InputDir) {
		return InputDir
	}
	relative, ok := strings.CutPrefix(clean, UserSessionsRoot+"/")
	if !ok {
		return ""
	}
	name, _, ok := strings.Cut(relative, "/")
	if !ok || name == "" {
		return ""
	}
	input := path.Join(UserSessionsRoot, name, "input")
	if IsUnder(clean, input) {
		return input
	}
	return ""
}

// CleanWriteFileIn normalizes a model-authored sandbox write and protects the
// attachment tree. The rule is the same on a session-owned and a shared
// machine: tools enforce WriteRoots at the tool layer.
func CleanWriteFileIn(l Layout, filePath string) (string, error) {
	clean := ResolvePathIn(l, filePath)
	if !path.IsAbs(clean) || clean == "." || clean == "/" {
		return "", fmt.Errorf("sandbox: workspace write path %q must be an absolute file path", filePath)
	}
	if clean == l.Root || clean == l.OutputDir || clean == l.InputDir {
		return "", fmt.Errorf("sandbox: workspace write path %q is a directory, not a file", filePath)
	}
	if input := l.ReadOnlyInputRoot(clean); input != "" {
		return "", fmt.Errorf("sandbox: session input %s is read-only", input)
	}
	return clean, nil
}

// CleanInputPathIn requires a path inside the layout's attachment tree.
func CleanInputPathIn(l Layout, filePath string) (string, error) {
	clean := path.Clean(strings.TrimSpace(filePath))
	if l.InputDir != "" && IsUnder(clean, l.InputDir) {
		return clean, nil
	}
	return "", fmt.Errorf("sandbox: session input path %q is outside %s", filePath, l.InputDir)
}

// CleanWorkDirIn requires an absolute working directory. Install calls keep
// the workspace/skills scope; every other layout allows any directory on the
// machine. None of these checks changes filesystem privileges.
func CleanWorkDirIn(l Layout, workDir string, allowSkillsRoot bool) (string, error) {
	clean := path.Clean(strings.TrimSpace(workDir))
	if !path.IsAbs(clean) {
		return "", fmt.Errorf("sandbox: work dir %q must be absolute", workDir)
	}
	if allowSkillsRoot {
		allowed := []string{l.Root, SkillsImageRoot}
		if underAny(clean, allowed) {
			return clean, nil
		}
		return "", fmt.Errorf("sandbox: work dir %q is outside allowed roots (%s)",
			workDir, strings.Join(allowed, ", "))
	}
	return clean, nil
}

// ValidOutputDirIn reports whether dir may be used as the artifact directory.
// The workspace root itself is refused: it is the whole workspace, drafts
// included, and artifact collection reads that as "collects nothing".
func ValidOutputDirIn(l Layout, dir string) (string, bool) {
	root := l.Root
	if root == "" {
		root = Root
	}
	clean := path.Clean(strings.TrimSpace(dir))
	if !strings.HasPrefix(clean, root+"/") {
		return "", false
	}
	return clean, true
}

// RunnableScriptIn reports whether scriptPath is a session-writable file that
// may be executed with an installed skill's interpreter. The attachment tree
// and the directory roots themselves are excluded.
func RunnableScriptIn(l Layout, scriptPath string) (string, bool) {
	clean := path.Clean(strings.TrimSpace(scriptPath))
	if clean == l.Root || clean == l.OutputDir || clean == l.InputDir {
		return "", false
	}
	if !strings.HasPrefix(clean, l.Root+"/") {
		return "", false
	}
	if l.InputDir != "" && strings.HasPrefix(clean, l.InputDir+"/") {
		return "", false
	}
	return clean, true
}
