package types

import "context"

// CallerType identifies who is issuing a VLM request. The vlm manager maps the
// caller to a priority / concurrency budget without call-site code having to
// know the policy — adding a new caller is therefore a one-line constant here
// plus a one-line WithVLMCaller injection at the call site, and the manager's
// priority table needs no branch changes.
//
// The enum is defined exhaustively up front (one entry per known VLM call
// site), even though Phase 1 only marks CallerKBBackground explicitly. The rest
// fall back to CallerUnknown and are governed as interactive. Defining them now
// means a future caller switch is data-only.
type CallerType string

const (
	// CallerUnknown is the default fallback for any context that did not mark
	// itself. It carries every interactive, unmarked call (chat / agent /
	// temporary document OCR / model test), so it is governed as high-priority
	// with a short-queue fail-open soft cap.
	CallerUnknown CallerType = "unknown"
	// CallerKBBackground is the knowledge-base multimodal backfill (OCR /
	// caption / observation) — a high-volume, latency-insensitive batch. It is
	// explicitly marked and governed as low-priority, blocking behind
	// interactive traffic.
	CallerKBBackground CallerType = "kb_background"
	// CallerChat is chat image understanding (reserved).
	CallerChat CallerType = "chat"
	// CallerAgent is the agent tool image description (reserved).
	CallerAgent CallerType = "agent"
	// CallerTemporaryDocument is temporary document inline OCR (reserved).
	CallerTemporaryDocument CallerType = "temporary_document"
	// CallerDebug is a model test / connection probe (reserved).
	CallerDebug CallerType = "debug"
)

// WithVLMCaller returns a derived context carrying the VLM caller identity.
// A zero/empty caller is a no-op so callers can pass a computed value safely.
func WithVLMCaller(ctx context.Context, caller CallerType) context.Context {
	if caller == "" {
		return ctx
	}
	return context.WithValue(ctx, VLMCallerContextKey, caller)
}

// VLMCallerFromContext extracts the VLM caller identity, defaulting to
// CallerUnknown when absent. The default is safe: an unmarked context is
// governed as interactive rather than silently dropped.
func VLMCallerFromContext(ctx context.Context) CallerType {
	if ctx == nil {
		return CallerUnknown
	}
	c, _ := ctx.Value(VLMCallerContextKey).(CallerType)
	if c == "" {
		return CallerUnknown
	}
	return c
}
