package tools

import (
	"github.com/Tencent/WeKnora/internal/agent/skills"
	"sort"
	"strings"
)

// skillTreeSkipDirs are install/cache trees that walk the skill root but are
// not something the model should open. Listing them as a flat bullet list
// (or even as a tree) would dump thousands of paths into the turn.
var skillTreeSkipDirs = map[string]struct{}{
	".venv":        {},
	"node_modules": {},
	"__pycache__":  {},
	".git":         {},
}

type skillTreeNode struct {
	children map[string]*skillTreeNode
}

// formatSkillFileTree renders the skill's files as an indented tree so each
// directory name is paid for once. Box-drawing `tree` characters are skipped
// on purpose: they cost tokens and are not part of file_path.
func formatSkillFileTree(files []string) string {
	root := &skillTreeNode{children: map[string]*skillTreeNode{}}
	for _, raw := range files {
		rel := strings.Trim(strings.ReplaceAll(raw, "\\", "/"), "/")
		if rel == "" || rel == skills.SkillFileName {
			continue
		}
		parts := strings.Split(rel, "/")
		skip := false
		for _, part := range parts {
			if _, junk := skillTreeSkipDirs[part]; junk {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		n := root
		for _, part := range parts {
			if n.children == nil {
				n.children = map[string]*skillTreeNode{}
			}
			child := n.children[part]
			if child == nil {
				child = &skillTreeNode{}
				n.children[part] = child
			}
			n = child
		}
	}
	if len(root.children) == 0 {
		return ""
	}
	var b strings.Builder
	writeSkillFileTree(&b, root, "")
	return b.String()
}

func writeSkillFileTree(b *strings.Builder, n *skillTreeNode, indent string) {
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		left, right := n.children[names[i]], n.children[names[j]]
		leftDir, rightDir := len(left.children) > 0, len(right.children) > 0
		if leftDir != rightDir {
			return leftDir
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		child := n.children[name]
		b.WriteString(indent)
		b.WriteString(name)
		if len(child.children) > 0 {
			b.WriteByte('/')
			b.WriteByte('\n')
			writeSkillFileTree(b, child, indent+"  ")
			continue
		}
		b.WriteByte('\n')
	}
}
