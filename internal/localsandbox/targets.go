package localsandbox

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// maxReviewTargets is how many paths one approval describes. More than this
// means the description is incomplete, and an auto review must refuse.
const maxReviewTargets = 10

// maxDirectoryEntries caps how far a directory is counted.
const maxDirectoryEntries = 1000

// maxScriptBytes and maxReviewScripts bound what one approval shows of the
// scripts a command runs. Past either, the description is incomplete.
const (
	maxScriptBytes   = 32 << 10
	maxReviewScripts = 4
)

// ScriptFact is a script the agent could have written that a command runs.
type ScriptFact struct {
	Path    string
	Content string
}

// describeTargets stats each path. It reads no file contents. A wildcard is
// described by what it matches now, since the literal pattern names nothing.
// Only the shell can expand braces, so those make the description incomplete.
func describeTargets(paths []string) ([]TargetFact, bool) {
	complete := true
	expanded := make([]string, 0, len(paths))
	for _, p := range paths {
		if strings.ContainsAny(p, "{}") {
			complete = false
			expanded = append(expanded, p)
			continue
		}
		if !strings.ContainsAny(p, "*?[") {
			expanded = append(expanded, p)
			continue
		}
		matches, err := filepath.Glob(p)
		if err != nil {
			complete = false
		}
		if len(matches) == 0 {
			expanded = append(expanded, p)
			continue
		}
		expanded = append(expanded, matches...)
	}
	if len(expanded) > maxReviewTargets {
		complete = false
		expanded = expanded[:maxReviewTargets]
	}
	facts := make([]TargetFact, 0, len(expanded))
	for _, p := range expanded {
		facts = append(facts, describeTarget(p))
	}
	return facts, complete
}

func describeTarget(p string) TargetFact {
	info, err := os.Lstat(p)
	switch {
	case err != nil:
		return TargetFact{Path: p, Type: "missing"}
	case info.Mode().IsRegular():
		return TargetFact{Path: p, Type: "file", Size: info.Size()}
	case info.IsDir():
		return TargetFact{Path: p, Type: "directory", Entries: countEntries(p)}
	default:
		return TargetFact{Path: p, Type: "other"}
	}
}

func countEntries(dir string) int {
	f, err := os.Open(dir)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	names, _ := f.Readdirnames(maxDirectoryEntries)
	return len(names)
}

// describeScripts reads the scripts a command runs, for whoever approves it.
// Only files the agent could have written are read, through the same guard as
// the file tools: anything else is either not its code or not its to read.
// The second result is true when a script exists but cannot be shown.
func (s *Service) describeScripts(policy Policy, review, cwd string) ([]ScriptFact, bool) {
	guard := NewPathGuard(policy)
	var facts []ScriptFact
	incomplete := false
	seen := map[string]bool{}
	for _, p := range ScriptPaths(review, s.builder.HomeDir()) {
		target := p
		if !filepath.IsAbs(target) {
			target = filepath.Join(cwd, target)
		}
		target = filepath.Clean(target)
		resolved, err := guard.CheckWrite(target)
		if err != nil {
			incomplete = true
			continue
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		if len(facts) == maxReviewScripts {
			incomplete = true
			continue
		}
		content, found, ok := readScript(guard, resolved)
		incomplete = incomplete || !ok
		if found && ok {
			facts = append(facts, ScriptFact{Path: resolved, Content: content})
		}
	}
	return facts, incomplete
}

// readScript returns found=false for a path that holds no file yet, and
// ok=false for one that cannot be shown as text.
func readScript(guard *PathGuard, path string) (content string, found, ok bool) {
	info, err := guard.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", false, true
	case err != nil, !info.Mode().IsRegular() && !info.IsDir(), info.Size() > maxScriptBytes:
		return "", true, false
	case info.IsDir():
		return "", false, true
	}
	data, err := guard.ReadFile(path)
	if err != nil || bytes.IndexByte(data, 0) >= 0 {
		return "", true, false
	}
	return string(data), true, true
}
