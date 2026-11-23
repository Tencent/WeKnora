package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// A shortcut the API returned without its origin node cannot be expanded, but
// its own document is still fetched through obj_token. Missing origin metadata
// is permanent, so it must not be reported as a document failure on every
// sync.
func TestExpandWikiShortcutsSkipsMissingOrigin(t *testing.T) {
	shortcut := WikiNode{
		SpaceID: "space", NodeToken: "shortcut-a", NodeType: "shortcut",
		ObjType: "docx", ObjToken: "doc-a", Title: "Document A",
		OriginSpaceID: "origin-space", // origin node token is empty
	}

	nodes, err := (&Client{}).expandWikiShortcuts(context.Background(), "space", []WikiNode{shortcut}, nil)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.Equal(t, "shortcut-a", nodes[0].NodeToken)
}

func TestResolveWikiShortcutDistinguishesMissingOrigin(t *testing.T) {
	node := WikiNode{SpaceID: "space", NodeToken: "shortcut", NodeType: "shortcut", OriginSpaceID: "origin"}

	_, err := (&Client{}).resolveWikiShortcut(context.Background(), node, nil)
	require.ErrorIs(t, err, errWikiShortcutMissingOrigin)
}

// Genuine expansion failures still surface: a shortcut cycle is a real listing
// failure, so the partial-failure path must stay intact.
func TestExpandWikiShortcutsReportsCycle(t *testing.T) {
	a := WikiNode{SpaceID: "space", NodeToken: "a", NodeType: "shortcut", OriginSpaceID: "space", OriginNodeID: "b"}
	b := WikiNode{SpaceID: "space", NodeToken: "b", NodeType: "shortcut", OriginSpaceID: "space", OriginNodeID: "a"}

	nodes, err := (&Client{}).expandWikiShortcuts(context.Background(), "space", []WikiNode{a, b}, nil)
	require.Len(t, nodes, 2)
	var partial *PartialWikiNodeListError
	require.ErrorAs(t, err, &partial)
	require.Len(t, partial.Failures, 2)
}
