package wiki

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/types"
)

// This file implements the P1 directory mapping: FetchedItem.FileName becomes
// "<目录A>/<目录B>/<文档名>.md" relative to the selected sync root (the space
// = the knowledge base root) so ingestion's
// types.SplitKnowledgeRelativePath derives the KB folder_path (GitLab-style).
// The path table is built per sync run from the recursive node listing — zero
// engine changes. external_id stays the node token, so a move/rename lands in
// the new folder through the existing update=delete+recreate path.

// filterShortcutSubtrees drops shortcut nodes and every descendant below them
// from a recursive listing. A shortcut mirrors its origin node, which the same
// walk already enumerates at the origin's real location; without the filter
// the origin document is ingested a second time under the shortcut token.
// The engine's deletion detection converges the duplicates from earlier syncs:
// a filtered shortcut that a previous cursor still records yields one
// IsDeleted item, then leaves the cursor.
func filterShortcutSubtrees(nodes []core.WikiNode) []core.WikiNode {
	byToken := make(map[string]core.WikiNode, len(nodes))
	for _, n := range nodes {
		byToken[n.NodeToken] = n
	}
	kept := make([]core.WikiNode, 0, len(nodes))
	for _, n := range nodes {
		if underShortcut(n, byToken) {
			continue
		}
		kept = append(kept, n)
	}
	return kept
}

// underShortcut reports whether n is itself a shortcut or has one on its
// ancestor chain within the listed set. An ancestor missing from the listing
// (subtree root's parent, or a partial listing) ends the walk as "no shortcut".
func underShortcut(n core.WikiNode, byToken map[string]core.WikiNode) bool {
	if n.NodeType == "shortcut" {
		return true
	}
	visited := make(map[string]bool)
	for p := n.ParentNodeID; p != "" && !visited[p]; p = byToken[p].ParentNodeID {
		visited[p] = true
		parent, ok := byToken[p]
		if !ok {
			return false
		}
		if parent.NodeType == "shortcut" {
			return true
		}
	}
	return false
}

// buildWikiDirPaths maps each node token to its cleaned directory prefix,
// relative to the selected sync root: the user-selected space IS the knowledge
// base root, so a top-level node maps to "" (its document lands directly in
// the KB root) and deeper nodes to "<父目录标题...>". Ancestors are resolved
// bottom-up through ParentNodeID within the listing. A docx node that also has
// children is both a document and a directory — its own item sits beside the
// child folder it forms.
// NOTE: with several wiki spaces selected into one KB, same-named
// sub-directories of different spaces merge into one KB folder — accepted.
func buildWikiDirPaths(nodes []core.WikiNode) map[string]string {
	byToken := make(map[string]core.WikiNode, len(nodes))
	for _, n := range nodes {
		byToken[n.NodeToken] = n
	}
	paths := make(map[string]string, len(nodes))
	for _, n := range nodes {
		// Walk up collecting ancestor titles, then reverse so the topmost
		// directory leads; no space-name segment (selected root = KB root).
		var ancestors []string
		visited := make(map[string]bool)
		for p := n.ParentNodeID; p != "" && !visited[p]; p = byToken[p].ParentNodeID {
			visited[p] = true
			parent, ok := byToken[p]
			if !ok {
				break
			}
			ancestors = append(ancestors, core.SanitizeFileName(parent.Title))
		}
		for i, j := 0, len(ancestors)-1; i < j; i, j = i+1, j-1 {
			ancestors[i], ancestors[j] = ancestors[j], ancestors[i]
		}
		paths[n.NodeToken] = strings.Join(ancestors, "/")
	}
	return paths
}

// prepareDirPaths rebuilds the per-resource directory table. Called from List
// after the shortcut filter, so shortcut tokens (and their subtrees) never
// appear as path segments.
func (o *wikiOps) prepareDirPaths(nodes []core.WikiNode) {
	o.dirPaths = buildWikiDirPaths(nodes)
}

// qualifyItemFileNames prefixes every fetched item's FileName with dir so
// ingestion derives the KB folder path. The prefix applies to the main item
// and to attachment/image sub-items alike — they all live in the same folder.
// An empty dir (node not in the current listing) or FileName is untouched.
func qualifyItemFileNames(items []*types.FetchedItem, dir string) {
	if dir == "" {
		return
	}
	for _, it := range items {
		if it != nil && it.FileName != "" {
			it.FileName = dir + "/" + it.FileName
		}
	}
}
