package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
)

// ListWikiNodesWithShortcuts supplements the existing traversal with descendants
// of shortcuts. The original traversal and its partial failures are preserved.
func (c *Client) ListWikiNodesWithShortcuts(ctx context.Context, spaceID, nodeToken string) ([]WikiNode, error) {
	nodes, err := c.ListWikiNodesRecursiveFrom(ctx, spaceID, nodeToken)
	var failures []WikiNodeListFailure
	if err != nil {
		var partial *PartialWikiNodeListError
		if !errors.As(err, &partial) {
			return nodes, err
		}
		failures = append(failures, partial.Failures...)
	}
	return c.expandWikiShortcuts(ctx, spaceID, nodes, failures)
}

func (c *Client) expandWikiShortcuts(
	ctx context.Context, spaceID string, nodes []WikiNode, failures []WikiNodeListFailure,
) ([]WikiNode, error) {
	if err := ctx.Err(); err != nil {
		return nodes, err
	}
	// Existing nodes have already been emitted. The original traversal handles
	// ordinary nodes and shortcuts reporting children; only unexpanded shortcuts
	// need processing in this extension.
	seen := make(map[string]bool)
	processed := make(map[string]bool)
	expanded := make(map[string]bool)
	origins := make(map[string]WikiNode)
	var shortcuts []WikiNode
	for _, node := range nodes {
		if node.SpaceID == "" {
			node.SpaceID = spaceID
		}
		key := node.SpaceID + ":" + node.NodeToken
		seen[key] = true
		origins[key] = node
		if node.NodeType == "shortcut" && !node.HasChild {
			shortcuts = append(shortcuts, node)
		} else {
			processed[key] = true
			// Failed listings remain partial failures rather than being retried
			// through another alias during the same sync.
			if node.HasChild {
				expanded[key] = true
			}
		}
	}

	var walk func(string, []WikiNode) error
	walk = func(parentSpace string, children []WikiNode) error {
		for _, node := range children {
			if err := ctx.Err(); err != nil {
				return err
			}
			if node.SpaceID == "" {
				node.SpaceID = parentSpace
			}
			key := node.SpaceID + ":" + node.NodeToken
			if processed[key] {
				continue
			}
			processed[key] = true
			if !seen[key] {
				seen[key] = true
				nodes = append(nodes, node)
			}

			target, err := c.resolveWikiShortcut(ctx, node, origins)
			if err == nil && target.HasChild {
				key = target.SpaceID + ":" + target.NodeToken
				if expanded[key] {
					continue
				}
				expanded[key] = true
				var descendants []WikiNode
				descendants, err = c.ListWikiNodes(ctx, target.SpaceID, target.NodeToken)
				if err == nil {
					if node.NodeType == "shortcut" {
						// Keep the discovered subtree under the selected shortcut
						// for directory mapping, while retaining source space IDs.
						for i := range descendants {
							descendants[i].ParentNodeID = node.NodeToken
						}
					}
					if err := walk(target.SpaceID, descendants); err != nil {
						return err
					}
				}
			}
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				wrapped := fmt.Errorf("list children of %s: %w", node.NodeToken, err)
				failures = append(failures, WikiNodeListFailure{Node: node, Err: wrapped})
				logger.Warnf(ctx, "[Feishu] partial wiki node listing failure: space=%s node=%s err=%v",
					node.SpaceID, node.NodeToken, wrapped)
			}
		}
		return nil
	}
	if err := walk(spaceID, shortcuts); err != nil {
		return nodes, err
	}
	if len(failures) > 0 {
		return nodes, &PartialWikiNodeListError{Failures: failures}
	}
	return nodes, nil
}

func (c *Client) resolveWikiShortcut(ctx context.Context, node WikiNode, cache map[string]WikiNode) (WikiNode, error) {
	visited := make(map[string]bool)
	for node.NodeType == "shortcut" {
		key := node.SpaceID + ":" + node.NodeToken
		if visited[key] {
			return WikiNode{}, fmt.Errorf("wiki shortcut cycle at %s", node.NodeToken)
		}
		visited[key] = true
		if node.OriginNodeID == "" || node.OriginSpaceID == "" {
			return WikiNode{}, fmt.Errorf("wiki shortcut %s is missing its original node or space", node.NodeToken)
		}
		key = node.OriginSpaceID + ":" + node.OriginNodeID
		original, ok := cache[key]
		if !ok {
			var err error
			original, err = c.GetWikiNode(ctx, node.OriginSpaceID, node.OriginNodeID)
			if err != nil {
				return WikiNode{}, fmt.Errorf("resolve wiki shortcut %s: %w", node.NodeToken, err)
			}
			cache[key] = original
		}
		node = original
	}
	return node, nil
}
