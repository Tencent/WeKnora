package asynqdl

import (
	"context"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A panic below RecoverMiddleware reaches the dead-letter callback as an
// ordinary error instead of unwinding past it.
func TestRecoverMiddleware_PanicReachesDeadLetterCallback(t *testing.T) {
	repo := &fakeRepo{}
	var callbackErr error
	cb := func(_ context.Context, _ *asynq.Task, err error) { callbackErr = err }
	panicking := asynq.HandlerFunc(func(context.Context, *asynq.Task) error {
		panic("nil retrieve engine")
	})
	h := MiddlewareWithCallback(repo, cb)(RecoverMiddleware()(panicking))

	err := h.ProcessTask(context.Background(), asynq.NewTask("document:process", []byte(`{}`)))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "document:process panicked")
	assert.Equal(t, err, callbackErr)
	assert.Equal(t, 1, repo.rowCount())
}
