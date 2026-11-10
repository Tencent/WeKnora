package retriever

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// scopeRecorderEngine records whether the composite reached it at all.
type scopeRecorderEngine struct {
	interfaces.RetrieveEngineService
	support []types.RetrieverType
	calls   int
}

func (e *scopeRecorderEngine) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (e *scopeRecorderEngine) Support() []types.RetrieverType { return e.support }

func (e *scopeRecorderEngine) Retrieve(_ context.Context, _ types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	e.calls++
	return nil, errors.New("engine must not be reached for an unscoped request")
}

func compositeWithRecorder(support ...types.RetrieverType) (*CompositeRetrieveEngine, *scopeRecorderEngine) {
	engine := &scopeRecorderEngine{support: support}
	composite := &CompositeRetrieveEngine{engineInfos: []*engineInfo{{
		retrieveEngine: engine,
		retrieverType:  support,
	}}}
	return composite, engine
}

// An unscoped param is a configuration defect, not a search that returned
// nothing: it must fail before any engine gets to degrade its filter into a
// full scan — even when a sibling param in the same batch is properly scoped.
func TestCompositeRetrieveRejectsUnscopedParamsBeforeFanOut(t *testing.T) {
	composite, engine := compositeWithRecorder(
		types.VectorRetrieverType, types.KeywordsRetrieverType)

	results, err := composite.Retrieve(context.Background(), []types.RetrieveParams{
		{
			Query:            "q",
			KnowledgeBaseIDs: []string{"kb-1"},
			TopK:             10,
			RetrieverType:    types.VectorRetrieverType,
		},
		{
			Query:         "q",
			TopK:          10,
			RetrieverType: types.KeywordsRetrieverType,
		},
	})

	require.ErrorIs(t, err, types.ErrRetrieveScopeRequired)
	require.Nil(t, results)
	require.Zero(t, engine.calls, "no engine may be dispatched for an unscoped request")
}

// A retriever type with no registered engine normally produces a routing
// error; the scope defect must still be reported, otherwise a misconfigured
// caller reads it as "this store is not wired up".
func TestCompositeRetrieveRejectsUnscopedParamsWithoutRegisteredEngine(t *testing.T) {
	composite := &CompositeRetrieveEngine{}

	results, err := composite.Retrieve(context.Background(), []types.RetrieveParams{{
		Query:         "q",
		TopK:          10,
		RetrieverType: types.VectorRetrieverType,
	}})

	require.ErrorIs(t, err, types.ErrRetrieveScopeRequired)
	require.Nil(t, results)
}
