package router

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type documentHandlerProbe struct {
	interfaces.KnowledgeService
	legacy     int
	admitted   int
	dispatched int
}

func (p *documentHandlerProbe) ProcessDocument(context.Context, *asynq.Task) error {
	p.legacy++
	return nil
}

func (p *documentHandlerProbe) AdmitLegacyDocument(context.Context, *asynq.Task) error {
	p.admitted++
	return nil
}

func (p *documentHandlerProbe) ProcessDocumentDispatch(context.Context, *asynq.Task) error {
	p.dispatched++
	return nil
}

type legacyDocumentHandlerProbe struct {
	interfaces.KnowledgeService
	calls int
}

func (p *legacyDocumentHandlerProbe) ProcessDocument(context.Context, *asynq.Task) error {
	p.calls++
	return nil
}

func TestDocumentHandlersAdmitLegacyTasksBeforeDispatch(t *testing.T) {
	p := &documentHandlerProbe{}
	legacy, dispatch := documentTaskHandlers(p)
	require.NotNil(t, dispatch)
	require.NoError(t, legacy(context.Background(), asynq.NewTask("document:process", nil)))
	require.NoError(t, dispatch(context.Background(), asynq.NewTask("document:dispatch", nil)))
	require.Equal(t, 1, p.admitted)
	require.Equal(t, 1, p.dispatched)
	require.Zero(t, p.legacy, "the scheduler alone invokes the actual pipeline")
}

func TestDocumentHandlersPreserveLegacyServiceImplementations(t *testing.T) {
	p := &legacyDocumentHandlerProbe{}
	legacy, dispatch := documentTaskHandlers(p)
	require.Nil(t, dispatch)
	require.NoError(t, legacy(context.Background(), asynq.NewTask("document:process", nil)))
	require.Equal(t, 1, p.calls)
}
