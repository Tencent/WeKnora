package interfaces

import (
	"context"
	"errors"
)

// ErrFileWriteIntentUnsupported is returned by storage implementations that
// cannot durably identify a physical object before writing it.
var ErrFileWriteIntentUnsupported = errors.New("file write intent is not supported")

type fileWriteIntentContextKey struct{}

type fileWriteIntentFunc func(context.Context, string) error

// WithFileWriteIntent attaches a callback that is invoked with the exact
// physical storage path immediately before SaveBytes writes an object.
func WithFileWriteIntent(
	ctx context.Context,
	intent func(context.Context, string) error,
) context.Context {
	if intent == nil {
		return ctx
	}
	return context.WithValue(ctx, fileWriteIntentContextKey{}, fileWriteIntentFunc(intent))
}

// FileWriteIntent returns the before-write callback attached to ctx, if any.
func FileWriteIntent(ctx context.Context) func(context.Context, string) error {
	intent, _ := ctx.Value(fileWriteIntentContextKey{}).(fileWriteIntentFunc)
	if intent == nil {
		return nil
	}
	return intent
}

// RecordFileWriteIntent records the physical path before storage I/O begins.
// Context cancellation is checked both before and after the callback so a
// callback that cancels its operation cannot be followed by a write.
func RecordFileWriteIntent(ctx context.Context, physicalPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if intent := FileWriteIntent(ctx); intent != nil {
		if err := intent(ctx, physicalPath); err != nil {
			return err
		}
	}
	return ctx.Err()
}
