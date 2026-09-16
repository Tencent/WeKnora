package wiki

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// This file implements the P1 directory mapping: FetchedItem.FileName becomes
// "<space名>/<目录A>/<目录B>/<文档名>.md" so ingestion's
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

// buildWikiDirPaths maps each node token to its cleaned directory prefix:
// "<space名>/<父目录标题...>". Ancestors are resolved bottom-up through
// ParentNodeID within the listing; a top-level node's prefix is the space name.
// A docx node that also has children is both a document and a directory — its
// own item sits beside the child folder it forms.
func buildWikiDirPaths(spaceName string, nodes []core.WikiNode) map[string]string {
	byToken := make(map[string]core.WikiNode, len(nodes))
	for _, n := range nodes {
		byToken[n.NodeToken] = n
	}
	paths := make(map[string]string, len(nodes))
	for _, n := range nodes {
		// Walk up collecting ancestor titles, then reverse: the space name
		// leads and each level's directory follows root-first.
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
		segments := append([]string{spaceName}, ancestors...)
		paths[n.NodeToken] = strings.Join(segments, "/")
	}
	return paths
}

// spaceName resolves the display name of a wiki space for the path's first
// segment. One ListWikiSpaces call per sync run (cached across resources);
// on failure the space ID is used so the sync still proceeds.
func (o *wikiOps) spaceName(ctx context.Context, client *core.Client, spaceID string) string {
	if o.spaceNames == nil {
		o.spaceNames = make(map[string]string)
	}
	if name, ok := o.spaceNames[spaceID]; ok {
		return name
	}
	name := spaceID
	spaces, err := client.ListWikiSpaces(ctx)
	if err != nil {
		logger.Warnf(ctx, "[Feishu] resolve wiki space name for %s: %v (falling back to space ID)", spaceID, err)
	} else {
		for _, s := range spaces {
			if s.SpaceID == spaceID && s.Name != "" {
				name = s.Name
				break
			}
		}
	}
	name = core.SanitizeFileName(name)
	o.spaceNames[spaceID] = name
	return name
}

// prepareDirPaths rebuilds the per-resource directory table. Called from List
// after the shortcut filter, so shortcut tokens (and their subtrees) never
// appear as path segments.
func (o *wikiOps) prepareDirPaths(ctx context.Context, client *core.Client, spaceID string, nodes []core.WikiNode) {
	o.dirPaths = buildWikiDirPaths(o.spaceName(ctx, client, spaceID), nodes)
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
