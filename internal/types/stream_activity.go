package types

import "context"

type streamActivityKey struct{}

// WithStreamActivity observes provider waiting separately from consumer backpressure
// and ledger work. waiting=true starts a new network-read/establishment interval.
func WithStreamActivity(ctx context.Context, observe func(bool)) context.Context {
	return context.WithValue(ctx, streamActivityKey{}, observe)
}

// StreamActivity reports whether the current stream is waiting on the provider to its context observer.
func StreamActivity(ctx context.Context, waiting bool) {
	if observe, ok := ctx.Value(streamActivityKey{}).(func(bool)); ok {
		observe(waiting)
	}
}
