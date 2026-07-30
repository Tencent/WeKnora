package embedding

import "context"

type providerCallObserverKey struct{}

// WithProviderCallObserver installs an internal callback at the real provider
// boundary. It does not change the public Embedder interface and is used by the
// ingestion artifact cache to distinguish cache lookups from model requests.
func WithProviderCallObserver(ctx context.Context, observer func([]string)) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, providerCallObserverKey{}, observer)
}

func observeProviderCall(ctx context.Context, texts []string) {
	if ctx == nil {
		return
	}
	if observer, ok := ctx.Value(providerCallObserverKey{}).(func([]string)); ok {
		observer(texts)
	}
}
