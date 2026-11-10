package sandbox

import "context"

type commandOutputKey struct{}

// WithCommandOutput observes the command executed with this context. It does
// not grant installer privileges or change the final command result.
func WithCommandOutput(ctx context.Context, callback func(string, []byte)) context.Context {
	return context.WithValue(ctx, commandOutputKey{}, callback)
}

func commandOutputCallback(ctx context.Context, explicit func(string, []byte)) func(string, []byte) {
	if explicit != nil {
		return explicit
	}
	callback, _ := ctx.Value(commandOutputKey{}).(func(string, []byte))
	return callback
}
