package artifact

import "context"

// FaultPoint names a durability boundary that recovery tests can interrupt.
// Production code never installs an injector, so the check is a no-op outside
// explicitly instrumented tests.
type FaultPoint string

const (
	FaultAfterProviderCall  FaultPoint = "after_provider_call"
	FaultAfterArtifactPut   FaultPoint = "after_artifact_put"
	FaultAfterChunkUpsert   FaultPoint = "after_chunk_upsert"
	FaultAfterVectorUpsert  FaultPoint = "after_vector_upsert"
	FaultAfterGraphBinding  FaultPoint = "after_graph_binding"
	FaultBeforeFence        FaultPoint = "before_fence"
	FaultAfterPublish       FaultPoint = "after_publish"
	FaultDuringStaleCleanup FaultPoint = "during_stale_cleanup"
)

type faultInjectorKey struct{}

// FaultInjector is invoked synchronously at a named durability boundary. It
// may return an error for graceful fault tests or panic to emulate a process
// crash before the worker acknowledges its task.
type FaultInjector func(FaultPoint) error

// WithFaultInjector installs a request-scoped test injector.
func WithFaultInjector(ctx context.Context, injector FaultInjector) context.Context {
	if injector == nil {
		return ctx
	}
	return context.WithValue(ctx, faultInjectorKey{}, injector)
}

// InjectFault executes the request-scoped injector, if any.
func InjectFault(ctx context.Context, point FaultPoint) error {
	if ctx == nil {
		return nil
	}
	injector, _ := ctx.Value(faultInjectorKey{}).(FaultInjector)
	if injector == nil {
		return nil
	}
	return injector(point)
}
