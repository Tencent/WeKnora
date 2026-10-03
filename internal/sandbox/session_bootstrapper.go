package sandbox

import "context"

// SessionBootstrapper customises how ONE session's first sandbox is created.
// It exists for session fork: a forked session boots from a snapshot of its
// parent's sandbox and then rolls /workspace back to the fork point.
//
// It is optional. A nil bootstrapper leaves the lifecycle's behaviour
// unchanged, which is what every ordinary session gets.
//
// The lifecycle calls both methods inside the per-session lifecycle lock, so
// implementations need no locking of their own and cannot race with a
// concurrent resolve of the same session.
type SessionBootstrapper interface {
	// TemplateOverride returns a template ID to create this session's sandbox
	// from instead of the config's. An empty string means "no override".
	//
	// An error aborts the create: an implementation that cannot tell whether
	// an override applies must not be silently treated as "no override",
	// because that would boot a plain sandbox for a session the user expects
	// to carry forked state.
	TemplateOverride(ctx context.Context, key SessionSandboxKey) (string, error)

	// AfterCreate runs once, immediately after the binding is written.
	//
	// Returning an error makes the lifecycle destroy the sandbox and delete
	// the binding. That severity is deliberate: a half-bootstrapped fork looks
	// completely normal but has the wrong baseline, and that silent wrongness
	// is far worse than an honest failure the next resolve can retry from a
	// clean slate.
	AfterCreate(ctx context.Context, key SessionSandboxKey, handle RemoteSandboxHandle) error
}

// SessionCreateFailureHandler is an optional SessionBootstrapper extension.
//
// AfterCreate only runs when Create succeeded. A fork snapshot that the
// provider will never boot (deleted, unknown template) would otherwise stay
// on the session forever: every Resolve retries the same dead ID. The
// lifecycle calls this when Create used a TemplateOverride and still failed,
// so the bootstrapper can retire that snapshot and let the next Resolve use
// the ordinary template.
type SessionCreateFailureHandler interface {
	OnCreateFailed(ctx context.Context, key SessionSandboxKey, createErr error)
}

// SessionBootstrapperWithClient rebinds a bootstrapper to the client that
// created the sandbox handle. AfterCreate runs under the session lifecycle
// lock, which is not reentrant, so implementations must talk to `client`
// (and the handle) directly instead of calling Resolve.
type SessionBootstrapperWithClient interface {
	WithClient(client RemoteSandboxClient) SessionBootstrapper
}
