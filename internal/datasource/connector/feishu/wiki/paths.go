package wiki

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/types"
)

// This file implements the P1 directory mapping: FetchedItem.FileName becomes
// "<空间名>/<目录A>/<目录B>/<文档名>.md" rooted at the synced space's own name
// (the space = a top-level KB folder) so ingestion's
// types.SplitKnowledgeRelativePath derives the KB folder_path (GitLab-style).
// The path table is built per sync run from the recursive node listing — zero
// engine changes. external_id stays the node token, so a move/rename lands in
// the new folder through the existing update=delete+recreate path.

// filterShortcutSubtrees drops only shortcuts whose original subtree is also
// in this complete listing. Never follow origins outside the selected scope:
// the shortcut can be the only accessible copy of a document.
func filterShortcutSubtrees(nodes []core.WikiNode) []core.WikiNode {
	byToken := make(map[string]core.WikiNode, len(nodes))
	for _, n := range nodes {
		byToken[n.NodeToken] = n
	}
	redundant := make(map[string]bool)
	for _, n := range nodes {
		origin, ok := byToken[n.OriginNodeID]
		if n.NodeType != "shortcut" || !ok || origin.NodeType == "shortcut" {
			continue
		}
		if n.OriginSpaceID != "" && origin.SpaceID != "" && n.OriginSpaceID != origin.SpaceID {
			continue
		}
		// An origin reached only through a shortcut is not an independently
		// enumerated copy. In particular, do not discard mutually linked trees.
		if !underShortcut(origin, byToken) {
			redundant[n.NodeToken] = true
		}
	}
	kept := make([]core.WikiNode, 0, len(nodes))
	for _, n := range nodes {
		if underFilteredNode(n, byToken, redundant) {
			continue
		}
		kept = append(kept, n)
	}
	return kept
}

func underFilteredNode(n core.WikiNode, byToken map[string]core.WikiNode, filtered map[string]bool) bool {
	visited := make(map[string]bool)
	for token := n.NodeToken; token != "" && !visited[token]; token = byToken[token].ParentNodeID {
		if filtered[token] {
			return true
		}
		visited[token] = true
	}
	return false
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
// rooted at spacePrefix (the synced space's sanitized name): a top-level node
// maps to spacePrefix (its document lands in the space's own KB folder) and
// deeper nodes to "<space>/<父目录标题...>". Ancestors are resolved bottom-up
// through ParentNodeID within the listing; for a sub-tree selection the
// ancestors above the sub-tree root are unknown, so its documents land
// directly in the space folder. A docx node that also has children is both a
// document and a directory — its own item sits beside the child folder it
// forms. An empty spacePrefix (space name unresolved) falls back to paths
// relative to the listing root.
func buildWikiDirPaths(nodes []core.WikiNode, spacePrefix string) map[string]string {
	byToken := make(map[string]core.WikiNode, len(nodes))
	for _, n := range nodes {
		byToken[n.NodeToken] = n
	}
	paths := make(map[string]string, len(nodes))
	for _, n := range nodes {
		// Walk up collecting ancestor titles, then reverse so the topmost
		// directory leads; the space name itself leads the chain.
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
		if spacePrefix != "" {
			ancestors = append([]string{spacePrefix}, ancestors...)
		}
		paths[n.NodeToken] = strings.Join(ancestors, "/")
	}
	return paths
}

// prepareDirPaths rebuilds the per-resource directory table. Called from List
// after removing redundant shortcut subtrees.
func (o *wikiOps) prepareDirPaths(nodes []core.WikiNode, spacePrefix string) {
	o.dirPaths = buildWikiDirPaths(nodes, spacePrefix)
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
