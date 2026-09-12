package retriever

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type moveTestEngine struct {
	fakeEngine
	calls int
}

func (e *moveTestEngine) MoveKnowledgeIndices(context.Context, string, string, string, []string, int, string) error {
	e.calls++
	return nil
}

func TestCompositeMoveChecksAllEnginesBeforeMutation(t *testing.T) {
	supported := &moveTestEngine{fakeEngine: fakeEngine{engineType: types.PostgresRetrieverEngineType}}
	unsupported := &fakeEngine{engineType: types.ElasticsearchRetrieverEngineType}
	c := &CompositeRetrieveEngine{
		engineInfos: []*engineInfo{{retrieveEngine: supported}, {retrieveEngine: unsupported}},
	}
	require.Error(
		t,
		c.MoveKnowledgeIndices(context.Background(), "source", "target", "doc", []string{"chunk"}, 2, "document"),
	)
	require.Zero(t, supported.calls)
	c.engineInfos = c.engineInfos[:1]
	require.NoError(
		t,
		c.MoveKnowledgeIndices(context.Background(), "source", "target", "doc", []string{"chunk"}, 2, "document"),
	)
	require.Equal(t, 1, supported.calls)
}
