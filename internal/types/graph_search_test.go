package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGraphSearchRequestNormalize(t *testing.T) {
	req := GraphSearchRequest{Query: "  腾讯  "}
	require.NoError(t, req.Normalize())
	require.Equal(t, "腾讯", req.Query)
	require.Equal(t, 50, req.MaxNodes)
	require.Equal(t, 100, req.MaxRelations)
	require.Equal(t, 20, req.MaxSourceChunks)
	for _, req := range []GraphSearchRequest{
		{Query: ""},
		{Query: "\n\t"},
		{Query: strings.Repeat("中", 513)},
		{Query: string([]byte{0xff})},
		{Query: "Acme", MaxNodes: -1},
		{Query: "Acme", MaxNodes: 101},
		{Query: "Acme", MaxRelations: -1},
		{Query: "Acme", MaxRelations: 201},
		{Query: "Acme", MaxSourceChunks: -1},
		{Query: "Acme", MaxSourceChunks: 51},
	} {
		require.Error(t, req.Normalize(), "%+v", req)
	}
	req = GraphSearchRequest{
		Query:    strings.Repeat("中", 512),
		MaxNodes: 100, MaxRelations: 200, MaxSourceChunks: 50,
	}
	require.NoError(t, req.Normalize())
}
