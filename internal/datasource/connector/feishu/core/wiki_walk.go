package core

import (
	"context"
	"fmt"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
)

// Bound outstanding directory requests while retaining the shared API rate gate.
const wikiDirectoryConcurrency = 4

type wikiDirectory struct {
	node     WikiNode
	parent   *wikiDirectory
	children []*wikiDirectory
	err      error
}

type wikiDirectoryResult struct {
	directory *wikiDirectory
	nodes     []WikiNode
	err       error
}

// Only the coordinator mutates the tree. Completion order must not change the
// provider's depth-first output order or the order of partial failures.
func (c *Client) walkWikiDirectories(
	ctx context.Context, spaceID string, selected *WikiNode,
) ([]WikiNode, error) {
	root := &wikiDirectory{}
	if selected != nil {
		root.node = *selected
		if root.node.NodeToken == "" {
			return nil, &PartialWikiNodeListError{Failures: []WikiNodeListFailure{{
				Node: root.node, Err: fmt.Errorf("wiki directory has no node token"),
			}}}
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	results := make(chan wikiDirectoryResult, wikiDirectoryConcurrency)
	pending := []*wikiDirectory{root}
	active := 0
	for len(pending) > 0 || active > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for active < wikiDirectoryConcurrency && len(pending) > 0 {
			directory := pending[0]
			pending[0] = nil
			pending = pending[1:]
			active++
			workers.Add(1)
			go func() {
				defer workers.Done()
				nodes, err := c.ListWikiNodes(ctx, spaceID, directory.node.NodeToken)
				// At most concurrency results can be outstanding, including after cancellation.
				results <- wikiDirectoryResult{directory: directory, nodes: nodes, err: err}
			}()
		}
		var result wikiDirectoryResult
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result = <-results:
			active--
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		directory := result.directory
		if result.err != nil {
			if directory == root && selected == nil {
				return nil, result.err
			}
			directory.err = fmt.Errorf("list children of %s: %w", directory.node.NodeToken, result.err)
			logger.Warnf(ctx, "[Feishu] partial wiki node listing failure: space=%s node=%s err=%v",
				spaceID, directory.node.NodeToken, result.err)
			continue
		}
		for _, node := range result.nodes {
			child := &wikiDirectory{node: node, parent: directory}
			directory.children = append(directory.children, child)
			if !node.HasChild {
				continue
			}
			if child.invalidToken() {
				child.err = fmt.Errorf("invalid or cyclic wiki directory token %q", node.NodeToken)
				continue
			}
			pending = append(pending, child)
		}
	}
	return root.listing()
}

func (directory *wikiDirectory) invalidToken() bool {
	if directory.node.NodeToken == "" {
		return true
	}
	for parent := directory.parent; parent != nil; parent = parent.parent {
		if parent.node.NodeToken == directory.node.NodeToken {
			return true
		}
	}
	return false
}

// Exclude the synthetic/selected root; the selected-subtree caller prepends it.
func (directory *wikiDirectory) listing() ([]WikiNode, error) {
	var nodes []WikiNode
	var failures []WikiNodeListFailure
	stack := []*wikiDirectory{directory}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current != directory {
			nodes = append(nodes, current.node)
		}
		if current.err != nil {
			failures = append(failures, WikiNodeListFailure{Node: current.node, Err: current.err})
		}
		for i := len(current.children) - 1; i >= 0; i-- {
			stack = append(stack, current.children[i])
		}
	}
	if len(failures) > 0 {
		return nodes, &PartialWikiNodeListError{Failures: failures}
	}
	return nodes, nil
}
