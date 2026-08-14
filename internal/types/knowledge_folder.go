package types

import (
	"sort"
	"strings"
)

const (
	// MaxKnowledgeFolderDepth caps how many nested levels a folder upload can
	// create. Browsers happily hand us arbitrarily deep webkitRelativePath
	// values; the cap keeps the sidebar tree renderable and bounds the stored
	// path length.
	MaxKnowledgeFolderDepth = 16
	// MaxKnowledgeFolderPathLength matches the folder_path column width.
	MaxKnowledgeFolderPathLength = 1024
	// MaxKnowledgeFolderSegmentLength caps a single directory name so one
	// pathological segment cannot consume the whole path budget.
	MaxKnowledgeFolderSegmentLength = 128
)

// KnowledgeFolderScope selects how KnowledgeListFilter.FolderPath is applied.
type KnowledgeFolderScope string

const (
	// FolderScopeAny ignores the folder dimension entirely (flat listing).
	FolderScopeAny KnowledgeFolderScope = ""
	// FolderScopeExact keeps only entries stored directly in FolderPath.
	FolderScopeExact KnowledgeFolderScope = "exact"
	// FolderScopeSubtree keeps entries in FolderPath and any descendant folder.
	FolderScopeSubtree KnowledgeFolderScope = "subtree"
)

// NormalizeKnowledgeFolderPath turns a client-supplied relative directory into
// the canonical form stored in knowledges.folder_path: forward-slash separated,
// no leading/trailing separator, no "." / ".." segments, depth and length
// capped. The zero value ("") means "knowledge base root".
func NormalizeKnowledgeFolderPath(raw string) string {
	if raw == "" {
		return ""
	}
	raw = strings.ReplaceAll(raw, "\\", "/")
	segments := make([]string, 0, 8)
	for _, segment := range strings.Split(raw, "/") {
		segment = strings.TrimSpace(segment)
		// Trailing dots and spaces are stripped so "a." and "a" cannot become
		// two sibling folders that render identically.
		segment = strings.TrimRight(segment, ". ")
		if segment == "" || segment == "." || segment == ".." {
			continue
		}
		if len(segment) > MaxKnowledgeFolderSegmentLength {
			segment = strings.TrimSpace(segment[:MaxKnowledgeFolderSegmentLength])
		}
		if segment == "" {
			continue
		}
		segments = append(segments, segment)
		if len(segments) >= MaxKnowledgeFolderDepth {
			break
		}
	}
	path := strings.Join(segments, "/")
	for len(path) > MaxKnowledgeFolderPathLength && len(segments) > 0 {
		segments = segments[:len(segments)-1]
		path = strings.Join(segments, "/")
	}
	return path
}

// SplitKnowledgeRelativePath splits an upload path such as
// "docs/spec/design.md" into its normalized folder path ("docs/spec") and its
// base file name ("design.md"). A plain file name yields an empty folder path,
// so callers can pass the raw `fileName` form field unconditionally.
func SplitKnowledgeRelativePath(raw string) (folderPath string, fileName string) {
	normalized := strings.ReplaceAll(raw, "\\", "/")
	idx := strings.LastIndex(normalized, "/")
	if idx < 0 {
		return "", strings.TrimSpace(normalized)
	}
	fileName = strings.TrimSpace(normalized[idx+1:])
	folderPath = NormalizeKnowledgeFolderPath(normalized[:idx])
	return folderPath, fileName
}

// KnowledgeFolderName returns the display name of a folder path (its last
// segment). The root folder has no name.
func KnowledgeFolderName(path string) string {
	if path == "" {
		return ""
	}
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

// KnowledgeFolderParent returns the parent folder path of the given path, or
// an empty string when the path is a root-level folder.
func KnowledgeFolderParent(path string) string {
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[:idx]
	}
	return ""
}

// KnowledgeFolderCount is one row of the folder aggregation query: the number
// of knowledge entries stored directly in a folder path.
type KnowledgeFolderCount struct {
	FolderPath string `json:"folder_path" gorm:"column:folder_path"`
	Count      int64  `json:"count"       gorm:"column:count"`
}

// KnowledgeFolderNode is a node of the knowledge base folder tree exposed to
// clients. DocumentCount counts entries stored directly in this folder;
// TotalCount additionally counts every descendant folder's entries.
type KnowledgeFolderNode struct {
	// Path is the canonical folder path, e.g. "docs/spec".
	Path string `json:"path"`
	// Name is the last segment of Path, used as the tree row label.
	Name string `json:"name"`
	// DocumentCount counts entries whose folder_path equals Path.
	DocumentCount int64 `json:"document_count"`
	// TotalCount counts entries in Path and in all descendant folders.
	TotalCount int64 `json:"total_count"`
	// Children are the direct sub-folders, sorted by name.
	Children []*KnowledgeFolderNode `json:"children,omitempty"`
}

// KnowledgeFolderTree is the response payload of the folder tree endpoint.
type KnowledgeFolderTree struct {
	// RootDocumentCount counts entries that live at the knowledge base root
	// (folder_path = ""), i.e. documents that were not uploaded as part of a
	// folder.
	RootDocumentCount int64 `json:"root_document_count"`
	// TotalDocumentCount counts every entry in the knowledge base, matching
	// the unfiltered document list total.
	TotalDocumentCount int64 `json:"total_document_count"`
	// Folders are the top-level folders, sorted by name.
	Folders []*KnowledgeFolderNode `json:"folders"`
}

// BuildKnowledgeFolderTree turns the flat per-folder counts returned by the
// repository into a tree. Intermediate folders that hold no documents of their
// own (only sub-folders) are materialized so the hierarchy stays connected.
func BuildKnowledgeFolderTree(counts []*KnowledgeFolderCount) *KnowledgeFolderTree {
	tree := &KnowledgeFolderTree{Folders: []*KnowledgeFolderNode{}}
	nodes := map[string]*KnowledgeFolderNode{}
	children := map[string][]*KnowledgeFolderNode{}

	// ensure materializes a folder node and every missing ancestor.
	var ensure func(path string) *KnowledgeFolderNode
	ensure = func(path string) *KnowledgeFolderNode {
		if node, ok := nodes[path]; ok {
			return node
		}
		node := &KnowledgeFolderNode{Path: path, Name: KnowledgeFolderName(path)}
		nodes[path] = node
		parent := KnowledgeFolderParent(path)
		if parent == "" {
			tree.Folders = append(tree.Folders, node)
		} else {
			ensure(parent)
			children[parent] = append(children[parent], node)
		}
		return node
	}

	for _, row := range counts {
		if row == nil {
			continue
		}
		tree.TotalDocumentCount += row.Count
		path := NormalizeKnowledgeFolderPath(row.FolderPath)
		if path == "" {
			tree.RootDocumentCount += row.Count
			continue
		}
		ensure(path).DocumentCount += row.Count
	}

	// Roll descendant counts up. Deepest paths first so a parent always sees
	// final child totals.
	paths := make([]string, 0, len(nodes))
	for path := range nodes {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		di, dj := strings.Count(paths[i], "/"), strings.Count(paths[j], "/")
		if di != dj {
			return di > dj
		}
		return paths[i] < paths[j]
	})
	for _, path := range paths {
		node := nodes[path]
		node.TotalCount += node.DocumentCount
		if parent := KnowledgeFolderParent(path); parent != "" {
			nodes[parent].TotalCount += node.TotalCount
		}
	}

	sortNodes := func(list []*KnowledgeFolderNode) {
		sort.Slice(list, func(i, j int) bool {
			return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
		})
	}
	for path, list := range children {
		sortNodes(list)
		nodes[path].Children = list
	}
	sortNodes(tree.Folders)
	return tree
}
