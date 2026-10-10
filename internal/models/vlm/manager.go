package vlm

import (
	"context"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// In-place transport-retry policy. A transient failure cheap enough to absorb is
// retried WITHOUT releasing the admission slot: holding the slot during the
// backoff is deliberate backpressure (it keeps the in-flight count down exactly
// when the provider is struggling) and it avoids re-entering admit, where a
// HIGH request would fail open and bypass the very throttle the retry exists to
// respect. A provider-requested wait longer than maxRetryWait is not held in
// place — the error is surfaced so the coarser task-level retry can absorb it
// later without pinning a concurrency slot.
const (
	maxRetryWait           = 10 * time.Second
	retryBaseDelay         = time.Second
	kbRetryBudget          = 2 // KB background: latency-insensitive, worth a couple of tries
	interactiveRetryBudget = 1 // interactive: bound the added latency
)

// managerVLM is the outermost VLM decorator. It applies per-model priority,
// rate and concurrency admission, server-feedback-driven adaptive control and
// runtime metrics around every VLM call — regardless of which caller issued it.
// It implements the full VLM interface (Predict + PredictWithOptions) so the
// two share one governance path; call sites need not switch to
// PredictWithOptions to be governed.
type managerVLM struct {
	inner VLM

	modelID   string
	modelName string

	configuredLimit int
	configuredRPM   int

	// innerSupportsPWO records whether the wrapped chain implements
	// PredictWithOptions. On this branch the VLM interface requires it, so it
	// is effectively always true; the flag is kept so the KB low-priority tier
	// degrades gracefully to interactive if a base ever lacks it (merge-order
	// independence, see plan §2.13).
	innerSupportsPWO bool
	// innerSupportsStreaming records whether the wrapped chain can stream
	// tokens to the manager; the manager always streams from the server for
	// first-token / TTFT observation when this is true.
	innerSupportsStreaming bool

	// usageReporter is an optional sink the inner exposes for token usage.
	usageReporter UsageReporter
}

// wrapVLMManager installs the vlm manager as the outermost decorator.
func wrapVLMManager(v VLM, config *Config, err error) (VLM, error) {
	if err != nil || v == nil {
		return v, err
	}
	m := &managerVLM{
		inner:                  v,
		modelID:                v.GetModelID(),
		modelName:              v.GetModelName(),
		configuredLimit:        config.MaxConcurrency,
		configuredRPM:          config.RequestsPerMinute,
		innerSupportsPWO:       supportsPWO(v),
		innerSupportsStreaming: supportsStreaming(v),
	}
	if ur, ok := v.(UsageReporter); ok {
		m.usageReporter = ur
	}
	return m, nil
}

// supportsPWO reports whether v implements PredictWithOptions (every VLM does
// on this branch; the flag exists for graceful degradation).
func supportsPWO(v VLM) bool {
	_, ok := v.(interface {
		PredictWithOptions(context.Context, [][]byte, string, *PredictOptions) (string, error)
	})
	return ok
}

func (m *managerVLM) GetModelName() string { return m.inner.GetModelName() }
func (m *managerVLM) GetModelID() string   { return m.inner.GetModelID() }

func (m *managerVLM) Predict(ctx context.Context, imgBytes [][]byte, prompt string) (string, error) {
	return m.call(ctx, imgBytes, prompt, nil)
}

func (m *managerVLM) PredictWithOptions(
	ctx context.Context, imgBytes [][]byte, prompt string, opts *PredictOptions,
) (string, error) {
	return m.call(ctx, imgBytes, prompt, opts)
}

// call is the single governance path for both Predict and PredictWithOptions.
func (m *managerVLM) call(
	ctx context.Context, imgBytes [][]byte, prompt string, opts *PredictOptions,
) (string, error) {
	caller := types.VLMCallerFromContext(ctx)
	rt := defaultRegistry.runtimeFor(m.modelID, m.configuredLimit, m.configuredRPM)
	prio := m.priorityFor(caller)

	// Circuit breaker: fail fast before any queuing or admission when the
	// endpoint is declared down. This is what turns "100 tasks waiting on a
	// dead model" into "100 tasks reporting the outage immediately" — no slot
	// consumed, no admission wait, no retry budget burned.
	if err := rt.guardCircuit(ctx); err != nil {
		return "", err
	}

	m.status(ctx, opts, PhaseQueued, PhaseInfo{})
	rt.markQueued(caller)
	start := time.Now()

	release, admitErr := rt.admit(ctx, prio)
	if admitErr != nil {
		rt.markCancelled(caller)
		// The manager could not obtain an admission slot (the caller context was
		// cancelled before it was queued). Report the raw error; the caller
		// decides what to do. The manager never imposes a success/failure verdict.
		return "", admitErr
	}
	defer release()
	rt.markAdmitted(caller)
	defer rt.markReleased(caller)

	m.status(ctx, opts, PhaseInflight, PhaseInfo{Dwell: time.Since(start)})

	budget := m.retryBudget(prio)
	for attempt := 0; ; attempt++ {
		genStart := time.Now()
		text, usage, _, innerErr := m.runInner(ctx, imgBytes, prompt, opts, genStart)

		if innerErr != nil {
			// Drive the adaptive controller and the circuit breaker on EVERY
			// failed attempt (each 429 should shed load; each hard-down should
			// be counted toward a trip). If the failure is not retried in place,
			// surface a typed verdict (dead endpoint / permanent client fault)
			// so the caller can stop spending its own retry budget on a lost
			// cause. The raw error survives via wrapping.
			m.handleFailure(rt, innerErr)
			kind, _, _ := classifyError(innerErr)
			if !m.retryInPlace(ctx, rt, innerErr, attempt, budget) {
				return "", wrapVerdict(innerErr, kind)
			}
			continue
		}

		if strings.TrimSpace(text) == "" {
			// A genuine empty answer (HTTP 200, no text) is the content
			// received. The caller routes it to "skipped" via the empty-string
			// result; the manager never fabricates a verdict. A 200 — even an
			// empty one — proves the endpoint is reachable, so close the breaker.
			rt.onEmptyContent()
			rt.cbOnSuccess()
			m.status(ctx, opts, PhaseDone, PhaseInfo{Dwell: time.Since(genStart)})
			return "", nil
		}

		rt.cbOnSuccess()
		rt.onSuccess(tokenTotal(usage), time.Since(genStart))
		if m.usageReporter != nil && usage != nil {
			m.usageReporter.Report(m.modelID, *usage)
		}
		m.status(ctx, opts, PhaseDone, PhaseInfo{Dwell: time.Since(genStart)})
		return text, nil
	}
}

// priorityFor maps a caller to an admission priority. KB gets the low (blocking)
// tier only when the base supports PredictWithOptions; otherwise it degrades to
// interactive-high so it shares the fail-open soft cap with chat.
func (m *managerVLM) priorityFor(caller types.CallerType) priority {
	if m.innerSupportsPWO && caller == types.CallerKBBackground {
		return prioLow
	}
	return prioHigh
}

// runInner executes the provider call (streaming when supported, else buffered)
// and returns the accumulated text, usage (best-effort), whether a first token
// was observed, and the raw error last. A non-nil error is always a failure the
// caller must honour — the manager never downgrades a failed call to a partial
// success by dropping the error (see consumeStream).
func (m *managerVLM) runInner(
	ctx context.Context, imgBytes [][]byte, prompt string, opts *PredictOptions, genStart time.Time,
) (string, *types.TokenUsage, bool, error) {
	if m.innerSupportsStreaming {
		if s, ok := m.inner.(StreamingVLM); ok {
			ch, err := s.PredictStream(ctx, imgBytes, prompt, opts)
			if err == nil {
				// The stream was established, so the request reached the
				// server. Consume it and report the outcome as-is: a mid-stream
				// transport reset, a provider "stream ended early" marker or a
				// cancellation is a FAILURE. Returning the tokens accumulated so
				// far with a nil error would let the caller record a half
				// answer as a complete one — the silent-corruption variant of
				// the #4064 class, and undetectable downstream.
				return m.consumeStream(ctx, ch, opts, genStart)
			}
			// Only a failure to ESTABLISH the stream (PredictStream returned
			// before any byte was read) falls back to the buffered path, so a
			// single transport hiccup at setup does not lose the request.
		}
	}
	text, err := m.inner.PredictWithOptions(ctx, imgBytes, prompt, opts)
	// Buffered path: first token coincides with completion.
	if text != "" {
		defaultRegistry.runtimeFor(m.modelID, m.configuredLimit, m.configuredRPM).markFirstToken(time.Since(genStart))
	}
	return text, nil, text != "", err
}

// consumeStream drains an established stream, forwarding chunks to the caller's
// OnChunk and recording first-token / TTFT plus generation-stall watchdogs. It
// is called only after PredictStream returned a channel, so every error it
// returns is a genuine mid-stream failure the caller must honour.
func (m *managerVLM) consumeStream(
	ctx context.Context, ch <-chan StreamChunk, opts *PredictOptions, genStart time.Time,
) (string, *types.TokenUsage, bool, error) {
	rt := defaultRegistry.runtimeFor(m.modelID, m.configuredLimit, m.configuredRPM)

	var sb strings.Builder
	var usage *types.TokenUsage
	firstToken := false
	seen := false

	// Prefill watchdog: request sent but no token within the threshold.
	prefillStop := make(chan struct{})
	time.AfterFunc(prefillStallTimeout, func() {
		select {
		case <-prefillStop:
			return
		default:
			rt.markPrefillStall()
		}
	})

	// Generation watchdog: no progress (new token / completion) within the
	// threshold since the last token. Reset on every token.
	var genTimer *time.Timer
	genStop := make(chan struct{})
	armGenWatchdog := func() {
		if genTimer == nil {
			genTimer = time.AfterFunc(genStallTimeout, func() {
				select {
				case <-genStop:
					return
				default:
					rt.markGenStall()
				}
			})
		} else {
			genTimer.Reset(genStallTimeout)
		}
	}

	defer func() {
		close(prefillStop)
		close(genStop)
		if genTimer != nil {
			genTimer.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return sb.String(), usage, seen, ctx.Err()
		case chunk, ok := <-ch:
			if !ok {
				if !firstToken {
					rt.markFirstToken(time.Since(genStart))
				}
				return sb.String(), usage, seen, nil
			}
			if chunk.Err != nil {
				return sb.String(), usage, seen, chunk.Err
			}
			if chunk.Usage != nil {
				usage = chunk.Usage
			}
			if chunk.Thinking {
				// A reasoning token: server progress for TTFT / watchdog
				// purposes, but never part of the answer — not buffered, not
				// forwarded to OnChunk. First token of ANY kind starts the
				// answer-phase clock; every reasoning token keeps the
				// generation watchdog alive through a long thinking phase.
				if chunk.Text != "" {
					if !firstToken {
						firstToken = true
						seen = true
						rt.markFirstToken(time.Since(genStart))
						m.status(ctx, opts, PhaseFirstToken, PhaseInfo{Dwell: time.Since(genStart)})
					}
					armGenWatchdog()
				}
				continue
			}
			if chunk.Text != "" {
				if !firstToken {
					firstToken = true
					seen = true
					rt.markFirstToken(time.Since(genStart))
					m.status(ctx, opts, PhaseFirstToken, PhaseInfo{Dwell: time.Since(genStart)})
					armGenWatchdog()
				} else {
					armGenWatchdog()
				}
				sb.WriteString(chunk.Text)
				if opts != nil && opts.OnChunk != nil {
					opts.OnChunk(chunk.Text, false, nil)
				}
			}
			if chunk.Done {
				if !firstToken {
					rt.markFirstToken(time.Since(genStart))
					seen = true
				}
				if opts != nil && opts.OnChunk != nil {
					opts.OnChunk(sb.String(), true, nil)
				}
				return sb.String(), usage, seen, nil
			}
		}
	}
}

// handleFailure classifies an inner error and drives the adaptive controller.
// It does NOT change what the caller receives: the raw error is passed through
// by call(). This is the manager's internal reaction (shed load on 429 / 5xx /
// timeout) only.
func (m *managerVLM) handleFailure(rt *modelRuntime, innerErr error) {
	kind, retryAfter, is5xx := classifyError(innerErr)
	switch kind {
	case KindRateLimited:
		rt.onRateLimited(retryAfter)
	case KindUnavailable:
		rt.onServerError(is5xx)
	case KindTimeout:
		// Reaching here means admission already succeeded, so any timeout is
		// observed after admission — shed load like a transport error.
		rt.onTimeout(true)
	case KindPermanent:
		// A client-side fault (bad key / oversized image / bad request): record
		// it but do NOT shed load or cool down.
		rt.onClientError()
	case KindHardDown:
		// A connection-level failure (refused / DNS / TLS / first-byte
		// timeout): count it toward the circuit-breaker trip window. The breaker
		// is what turns "this one request died" into "the whole endpoint is
		// declared down for every other caller".
		rt.cbRecordFailure()
	case KindCancelled:
		// The caller walked away; the endpoint said nothing. There is nothing
		// to learn about provider health here: no shedding, no cooldown, and no
		// circuit-breaker accounting (otherwise repeated user cancellations
		// could open the shared breaker against a healthy endpoint). Explicit
		// case so a future default branch cannot start punishing cancellations.
	}
}

// retryBudget returns how many in-place retries a caller may perform after the
// first attempt. KB background work is latency-insensitive and benefits from a
// couple of tries; interactive requests get at most one so a failing provider
// cannot pile latency onto the UI.
func (m *managerVLM) retryBudget(prio priority) int {
	if prio == prioLow {
		return kbRetryBudget
	}
	return interactiveRetryBudget
}

// retryInPlace decides whether a failed attempt should be retried after an
// in-place backoff, and if so performs the wait. The admission slot is NOT
// released across the wait (see the retry-policy comment at the top of this
// file): holding it is the intended backpressure, and it keeps a HIGH retry
// from re-entering admit's fail-open path.
func (m *managerVLM) retryInPlace(
	ctx context.Context, rt *modelRuntime, err error, attempt, budget int,
) bool {
	if ctx.Err() != nil {
		return false // the caller's context is gone; nothing to wait for
	}
	kind, retryAfter, _ := classifyError(err)
	if !isRetryable(kind) || attempt >= budget {
		return false
	}
	if retryAfter > maxRetryWait {
		// The provider asked for a longer pause than we are willing to pin a
		// slot for; surface the error so the task-level retry absorbs it later.
		return false
	}
	rt.markRetried()
	rt.beginRetryWait()
	defer rt.endRetryWait()
	return sleepCtx(ctx, retryWait(retryAfter, attempt))
}

// isRetryable reports whether an error class is worth retrying in place. A
// permanent client fault (bad key, oversized image, unsupported request) is
// not — repeating it only burns budget and bills the provider again. A
// hard-down (dead endpoint) is not either: a refused connection will not start
// answering if we simply try again, and once the circuit breaker trips it
// already fails the other requests fast, so an in-place retry here would just
// add latency to the one request that arrived before the trip. A caller
// cancellation is not either — its context is already gone.
func isRetryable(kind ErrorKind) bool {
	return kind != KindPermanent && kind != KindHardDown && kind != KindCancelled
}

// retryWait returns how long to wait before the next in-place retry, honouring
// the provider's Retry-After when it is longer than our own exponential base.
func retryWait(retryAfter time.Duration, attempt int) time.Duration {
	wait := retryBaseDelay << uint(attempt) // 1s, 2s, 4s, ...
	if retryAfter > wait {
		wait = retryAfter
	}
	// Jitter so a KB batch that all hit a 429 at once does not retry in lockstep
	// and re-flatten the provider. Derived from the clock rather than math/rand,
	// both to avoid a weak-randomness lint and because only the sub-second
	// spread matters here.
	jitter := time.Duration(time.Now().UnixNano() % (int64(wait/5) + 1))
	wait += jitter
	if wait > maxRetryWait {
		wait = maxRetryWait // hard cap, applied last so jitter cannot exceed it
	}
	return wait
}

// sleepCtx waits for d unless ctx is cancelled first, in which case it reports
// false so the caller stops retrying immediately.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (m *managerVLM) status(_ context.Context, opts *PredictOptions, phase Phase, info PhaseInfo) {
	if opts != nil && opts.StatusSink != nil {
		opts.StatusSink(phase, info)
	}
}

func tokenTotal(u *types.TokenUsage) int {
	if u == nil {
		return 0
	}
	return u.TotalTokens
}
