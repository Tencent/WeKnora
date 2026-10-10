package retriever

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// questionToggleEngine records one generated-question update. A nil err means
// the engine applied the change.
type questionToggleEngine struct {
	interfaces.RetrieveEngineService
	engineType types.RetrieverEngineType
	rows       int64
	err        error
	calls      int
}

func (e *questionToggleEngine) EngineType() types.RetrieverEngineType { return e.engineType }

func (e *questionToggleEngine) SetGeneratedQuestionEnabled(context.Context, string, bool) (int64, error) {
	e.calls++
	if e.err != nil {
		return 0, e.err
	}
	return e.rows, nil
}

func TestSetGeneratedQuestionEnabledRejectsMixedEngineCoverage(t *testing.T) {
	keyword := &questionToggleEngine{engineType: types.PostgresRetrieverEngineType, rows: 3}
	vector := &cannedEngine{
		engineType: types.WeaviateRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType},
	}
	composite := &CompositeRetrieveEngine{engineInfos: []*engineInfo{
		{retrieveEngine: keyword, retrieverType: []types.RetrieverType{types.KeywordsRetrieverType}},
		{retrieveEngine: vector, retrieverType: vector.support},
	}}

	n, err := composite.SetGeneratedQuestionEnabled(context.Background(), "kb-off", false)
	require.ErrorIs(t, err, ErrGeneratedQuestionIndexPartial)
	require.NotErrorIs(t, err, ErrGeneratedQuestionIndexUnsupported)
	require.Equal(t, int64(3), n)
	require.Equal(t, 1, keyword.calls)

	n, err = composite.SetGeneratedQuestionEnabled(context.Background(), "kb-off", false)
	require.ErrorIs(t, err, ErrGeneratedQuestionIndexPartial)
	require.Equal(t, int64(3), n)
	require.Equal(t, 2, keyword.calls, "a completed engine stays safe to retry")
}

func TestSetGeneratedQuestionEnabledUnsupportedWhenNoEngineCanUpdate(t *testing.T) {
	composite := &CompositeRetrieveEngine{engineInfos: []*engineInfo{
		{retrieveEngine: &cannedEngine{engineType: types.QdrantRetrieverEngineType}},
		{retrieveEngine: &questionToggleEngine{
			engineType: types.WeaviateRetrieverEngineType,
			err:        ErrGeneratedQuestionIndexUnsupported,
		}},
	}}
	_, err := composite.SetGeneratedQuestionEnabled(context.Background(), "kb-off", false)
	require.ErrorIs(t, err, ErrGeneratedQuestionIndexUnsupported)
	require.NotErrorIs(t, err, ErrGeneratedQuestionIndexPartial)
}
