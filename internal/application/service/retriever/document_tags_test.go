package retriever

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tagProjectionEngine struct {
	interfaces.RetrieveEngineService
	supported bool
	called    bool
	err       error
}

func (e *tagProjectionEngine) SupportsDocumentTags() bool { return e.supported }
func (e *tagProjectionEngine) SyncDocumentTags(context.Context, string, []string) error {
	e.called = true
	return e.err
}

func TestDocumentTagCompositeRequiresEveryNativeBackend(t *testing.T) {
	a := &tagProjectionEngine{supported: true}
	b := &tagProjectionEngine{supported: true, err: errors.New("store unavailable")}
	fallback := &tagProjectionEngine{}
	c := &CompositeRetrieveEngine{engineInfos: []*engineInfo{{retrieveEngine: a}, {retrieveEngine: b}, {retrieveEngine: fallback}}}
	require.True(t, c.SupportsDocumentTags())
	require.Error(t, c.SyncDocumentTags(context.Background(), "kb", []string{"doc"}))
	assert.True(t, a.called)
	assert.True(t, b.called)
	assert.False(t, fallback.called)
	b.err = nil
	require.NoError(t, c.SyncDocumentTags(context.Background(), "kb", []string{"doc"}))
}
