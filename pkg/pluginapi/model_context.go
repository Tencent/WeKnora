package pluginapi

import (
	"context"

	"google.golang.org/grpc/metadata"
)

const (
	mdModelIDKey   = "x-weknora-model-id"
	mdModelNameKey = "x-weknora-model-name"
)

// ModelContext carries the identity of the model instance a model-plugin call
// targets. It is transported as gRPC metadata (the same mechanism as
// InvocationContext) so v1 protobuf messages stay wire compatible. This lets a
// single plugin process serve multiple model instances / tenants without
// guessing which model a call targets from process-global state.
type ModelContext struct {
	ModelID   string
	ModelName string
}

// WithModelContext attaches model identity to an outgoing gRPC call. Call it on
// the host side before invoking a model capability RPC.
func WithModelContext(ctx context.Context, mc ModelContext) context.Context {
	return metadata.AppendToOutgoingContext(ctx, mdModelIDKey, mc.ModelID, mdModelNameKey, mc.ModelName)
}

// ModelContextFromContext reads model identity inside a plugin RPC handler.
// Plugins use it in any capability callback to know which model instance the
// current call targets.
func ModelContextFromContext(ctx context.Context) ModelContext {
	md, _ := metadata.FromIncomingContext(ctx)
	first := func(key string) string {
		values := md.Get(key)
		if len(values) == 0 {
			return ""
		}
		return values[0]
	}
	return ModelContext{ModelID: first(mdModelIDKey), ModelName: first(mdModelNameKey)}
}
