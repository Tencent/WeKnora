package git_repo

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// errPathEscapesWorktree is returned when a resolved path (after following
// symlinks) lands outside the clone worktree. Callers skip the file rather
// than ingesting host or cross-tenant content.
var errPathEscapesWorktree = errors.New("git_repo: path escapes worktree")

// resolveUnderRoot maps a repository-relative path to an absolute file under
// root, following symlinks and rejecting any target that leaves the worktree.
func resolveUnderRoot(root, rel string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootResolved, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		// Root may not exist yet (clone mkdir); use the cleaned abs path.
		if !os.IsNotExist(err) {
			return "", err
		}
		rootResolved = rootAbs
	}

	candidate := filepath.Join(rootResolved, filepath.FromSlash(rel))
	if !underRoot(rootResolved, candidate) {
		return "", errPathEscapesWorktree
	}

	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	if !underRoot(rootResolved, resolved) {
		return "", errPathEscapesWorktree
	}
	return resolved, nil
}

func underRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
