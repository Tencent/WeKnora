// Package safepaths provides path-traversal protection for file inputs to
// commands such as `weknora doc upload`.
//
// Two primary checks:
//   - Validate(path): reject empty, ".", "..", or paths containing embedded ".." segments.
//   - WithinRoot(path, root): both are made absolute and compared with a
//     separator-aware prefix check, so a directory named "..foo" cannot
//     masquerade as escaping ".." (the lexical strings.HasPrefix(rel, "..")
//     trap).
//
// Symlinks are NOT resolved; callers needing realpath semantics must
// pre-resolve. Modeled on the same pattern as `internal/utils/security.go`
// SafePathUnderBase in the server module.
package safepaths

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrPathEscapes signals that the given path resolves outside the allowed root.
var ErrPathEscapes = errors.New("path escapes allowed root")

// ErrEmptyPath signals an empty input.
var ErrEmptyPath = errors.New("path is empty")

// ErrSuspiciousPath signals a "."/".." or embedded ".." traversal.
var ErrSuspiciousPath = errors.New("path contains suspicious traversal segment")

// Validate cleans path and rejects empty inputs, "." / "..", or any path that
// contains an embedded ".." segment after cleaning. It does not resolve symlinks.
func Validate(path string) (string, error) {
	if path == "" {
		return "", ErrEmptyPath
	}
	cleaned := filepath.Clean(path)
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return "", fmt.Errorf("%w: %q", ErrSuspiciousPath, path)
	}
	for _, seg := range strings.Split(cleaned, string(filepath.Separator)) {
		if seg == ".." {
			return "", fmt.Errorf("%w: %q", ErrSuspiciousPath, path)
		}
	}
	return cleaned, nil
}

// WithinRoot reports whether path lies inside root after both are made
// absolute. Comparison is separator-aware: a path "/srv/data..foo" is NOT
// treated as escaping "/srv/data" just because "..foo" textually starts
// with "..".
func WithinRoot(path, root string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	// Equal paths count as "within"; otherwise require a separator-bounded prefix.
	if absPath == absRoot {
		return nil
	}
	withSep := absRoot + string(filepath.Separator)
	if !strings.HasPrefix(absPath, withSep) {
		return fmt.Errorf("%w: %s not under %s", ErrPathEscapes, absPath, absRoot)
	}
	return nil
}
