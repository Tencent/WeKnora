package types

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeKnowledgeFolderPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty stays root", "", ""},
		{"plain path", "docs/spec", "docs/spec"},
		{"windows separators", `docs\spec`, "docs/spec"},
		{"leading and trailing separators", "/docs/spec/", "docs/spec"},
		{"collapses empty segments", "docs//spec", "docs/spec"},
		{"drops traversal segments", "docs/../../etc/passwd", "docs/etc/passwd"},
		{"drops dot segments", "./docs/./spec", "docs/spec"},
		{"trims whitespace and trailing dots", " docs . / spec ", "docs/spec"},
		{"root only separators", "///", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, NormalizeKnowledgeFolderPath(tc.in))
		})
	}
}

func TestNormalizeKnowledgeFolderPathCapsDepthAndLength(t *testing.T) {
	deep := strings.Repeat("a/", MaxKnowledgeFolderDepth+5) + "a"
	normalized := NormalizeKnowledgeFolderPath(deep)
	assert.Equal(t, MaxKnowledgeFolderDepth, strings.Count(normalized, "/")+1,
		"depth must be capped so the sidebar tree stays renderable")

	longSegment := strings.Repeat("x", MaxKnowledgeFolderSegmentLength+50)
	assert.Len(t, NormalizeKnowledgeFolderPath(longSegment), MaxKnowledgeFolderSegmentLength)

	wide := strings.TrimSuffix(strings.Repeat(strings.Repeat("y", 100)+"/", 12), "/")
	assert.LessOrEqual(t, len(NormalizeKnowledgeFolderPath(wide)), MaxKnowledgeFolderPathLength)
}

func TestNormalizeKnowledgeFolderPathKeepsSegmentsValidUTF8(t *testing.T) {
	// 60 Chinese characters is 180 bytes, so the segment cap cuts inside the
	// 43rd character. The stored folder_path has to stay valid UTF-8: an
	// invalid one no longer survives a JSON round trip, so the path the client
	// gets back never matches the row again on rename or move.
	segment := strings.Repeat("中", 60)
	normalized := NormalizeKnowledgeFolderPath(segment)

	assert.True(t, utf8.ValidString(normalized),
		"segment truncated mid-rune: % x", []byte(normalized))
	assert.LessOrEqual(t, len(normalized), MaxKnowledgeFolderSegmentLength,
		"the cap is a byte budget and must still hold")
	assert.True(t, strings.HasPrefix(segment, normalized),
		"truncation must only drop a suffix")
	assert.Equal(t, normalized, NormalizeKnowledgeFolderPath(normalized),
		"normalizing an already-normalized path must be a no-op")

	// A multi-byte character straddling the cap in an otherwise ASCII name.
	mixed := strings.Repeat("a", MaxKnowledgeFolderSegmentLength-1) + "é" + "tail"
	assert.True(t, utf8.ValidString(NormalizeKnowledgeFolderPath(mixed)))
	assert.Equal(t, strings.Repeat("a", MaxKnowledgeFolderSegmentLength-1),
		NormalizeKnowledgeFolderPath(mixed))
}

func TestSplitKnowledgeRelativePath(t *testing.T) {
	cases := []struct {
		in         string
		wantFolder string
		wantName   string
	}{
		{"design.md", "", "design.md"},
		{"docs/spec/design.md", "docs/spec", "design.md"},
		{`docs\spec\design.md`, "docs/spec", "design.md"},
		{"/docs/design.md", "docs", "design.md"},
		{"docs/../design.md", "docs", "design.md"},
		{"docs/", "docs", ""},
	}
	for _, tc := range cases {
		folder, name := SplitKnowledgeRelativePath(tc.in)
		assert.Equal(t, tc.wantFolder, folder, "folder for %q", tc.in)
		assert.Equal(t, tc.wantName, name, "name for %q", tc.in)
	}
}

func TestBuildKnowledgeFolderTree(t *testing.T) {
	tree := BuildKnowledgeFolderTree([]*KnowledgeFolderCount{
		{FolderPath: "", Count: 2},
		{FolderPath: "docs/spec", Count: 3},
		{FolderPath: "docs/spec/v2", Count: 1},
		{FolderPath: "Assets", Count: 4},
	})

	assert.Equal(t, int64(2), tree.RootDocumentCount)
	assert.Equal(t, int64(10), tree.TotalDocumentCount)

	// Top level is sorted case-insensitively: "Assets" before "docs".
	require.Len(t, tree.Folders, 2)
	assert.Equal(t, "Assets", tree.Folders[0].Path)
	assert.Equal(t, int64(4), tree.Folders[0].TotalCount)

	docs := tree.Folders[1]
	assert.Equal(t, "docs", docs.Path)
	// "docs" itself holds no document but is materialized to keep the tree
	// connected, and its total rolls up both descendants.
	assert.Equal(t, int64(0), docs.DocumentCount)
	assert.Equal(t, int64(4), docs.TotalCount)

	require.Len(t, docs.Children, 1)
	spec := docs.Children[0]
	assert.Equal(t, "docs/spec", spec.Path)
	assert.Equal(t, "spec", spec.Name)
	assert.Equal(t, int64(3), spec.DocumentCount)
	assert.Equal(t, int64(4), spec.TotalCount)

	require.Len(t, spec.Children, 1)
	assert.Equal(t, "docs/spec/v2", spec.Children[0].Path)
	assert.Equal(t, int64(1), spec.Children[0].TotalCount)
}

func TestBuildKnowledgeFolderTreeEmpty(t *testing.T) {
	tree := BuildKnowledgeFolderTree(nil)
	assert.Equal(t, int64(0), tree.TotalDocumentCount)
	assert.NotNil(t, tree.Folders, "folders must serialize as [] rather than null")
	assert.Empty(t, tree.Folders)
}
