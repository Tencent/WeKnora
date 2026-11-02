package types

import (
	"strings"
	"testing"
)

func TestBuildKnowledgeFolderChildPath(t *testing.T) {
	cases := []struct {
		name   string
		parent string
		child  string
		want   string
	}{
		{"root", "", "设计资料", "设计资料"},
		{"nested", "docs/spec", "修订版", "docs/spec/修订版"},
		{"trim outer spaces", "docs", "  draft  ", "docs/draft"},
		{"hidden name", "docs", ".draft", "docs/.draft"},
		{"name byte boundary", "", strings.Repeat("a", 128), strings.Repeat("a", 128)},
		{"UTF8 within byte boundary", "", strings.Repeat("中", 42), strings.Repeat("中", 42)},
		{"depth boundary", strings.Repeat("a/", 14) + "a", "b", strings.Repeat("a/", 15) + "b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildKnowledgeFolderChildPath(tc.parent, tc.child)
			if err != nil || got != tc.want {
				t.Fatalf(
					"BuildKnowledgeFolderChildPath(%q, %q) = %q, %v; want %q",
					tc.parent, tc.child, got, err, tc.want,
				)
			}
		})
	}
}

func TestBuildKnowledgeFolderChildPathRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name   string
		parent string
		child  string
	}{
		{"empty", "", ""},
		{"blank", "docs", "   "},
		{"dot", "", "."},
		{"dotdot", "", ".."},
		{"slash", "", "a/b"},
		{"backslash", "", `a\b`},
		{"fullwidth slash", "", "a／b"},
		{"division slash", "", "a∕b"},
		{"trailing dot", "", "draft."},
		{"embedded newline", "", "a\nb"},
		{"surrounding control", "", "\tname\n"},
		{"nul", "", "a\x00b"},
		{"DEL", "", "a\x7fb"},
		{"invalid UTF8 name", "", string([]byte{0xff})},
		{"name too long", "", strings.Repeat("a", 129)},
		{"UTF8 name too long", "", strings.Repeat("中", 43)},
		{"parent leading separator", "/docs", "child"},
		{"parent empty segment", "docs//spec", "child"},
		{"parent trailing separator", "docs/", "child"},
		{"parent traversal", "docs/../spec", "child"},
		{"parent dot segment", "docs/./spec", "child"},
		{"parent spaces", " docs", "child"},
		{"parent trailing dot", "docs.", "child"},
		{"parent backslash", `docs\spec`, "child"},
		{"parent control", "docs\nspec", "child"},
		{"parent invalid UTF8", string([]byte{0xff}), "child"},
		{"parent segment too long", strings.Repeat("a", 129), "child"},
		{"depth overflow", strings.Repeat("a/", 15) + "a", "child"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildKnowledgeFolderChildPath(tc.parent, tc.child)
			if err == nil || got != "" {
				t.Fatalf(
					"BuildKnowledgeFolderChildPath(%q, %q) = %q, %v; want empty path and error",
					tc.parent, tc.child, got, err,
				)
			}
		})
	}
}

func TestBuildKnowledgeFolderChildPathTotalByteLimit(t *testing.T) {
	parent := strings.TrimSuffix(strings.Repeat(strings.Repeat("a", 128)+"/", 7), "/")
	remaining := MaxKnowledgeFolderPathLength - len(parent) - 1
	path, err := BuildKnowledgeFolderChildPath(parent, strings.Repeat("b", remaining))
	if err != nil || len(path) != MaxKnowledgeFolderPathLength {
		t.Fatalf("exact path byte limit: len=%d, err=%v", len(path), err)
	}
	if path, err := BuildKnowledgeFolderChildPath(parent, strings.Repeat("b", remaining+1)); err == nil || path != "" {
		t.Fatalf("over path byte limit: got %q, %v", path, err)
	}
}

func TestBuildKnowledgeFolderTreeKeepsExplicitEmptyPaths(t *testing.T) {
	tree := BuildKnowledgeFolderTree([]*KnowledgeFolderCount{
		{FolderPath: "empty/child", Count: 0},
		{FolderPath: "docs", Count: 2},
		{FolderPath: "docs", Count: 0},
	})
	if tree.TotalDocumentCount != 2 || len(tree.Folders) != 2 {
		t.Fatalf("zero-count paths changed document totals or disappeared: %+v", tree)
	}
	empty := tree.Folders[1]
	if empty.Path != "empty" || empty.TotalCount != 0 ||
		len(empty.Children) != 1 || empty.Children[0].Path != "empty/child" {
		t.Fatalf("explicit empty path and its ancestor must remain visible: %+v", empty)
	}
	if tree.Folders[0].DocumentCount != 2 {
		t.Fatalf("explicit/derived overlap must not duplicate document counts: %+v", tree.Folders[0])
	}
}
