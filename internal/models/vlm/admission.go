// Package vlm wraps a VLM model with per-model admission control, adaptive
// concurrency, and in-place retry so a single overloaded upstream does not
// take down the whole knowledge-base ingestion pipeline.
package vlm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// defaultVLMMaxConcurrency is the per-model VLM concurrency cap when the model
// row carries none. It mirrors the chat governor's default (32) so VLM and chat
// share the same intuition; the system sets no cross-model global cap — control
// is purely per-model-id.
const defaultVLMMaxConcurrency = 32

// Interactive (HIGH) requests wait at most this long for a free concurrency
// slot before failing open — a soft cap that protects latency without letting a
// saturated provider hard-block the UI.
const interactiveFailOpenWait = 500 * time.Millisecond

// Stall watchdogs (Phase 1: record only, never abort).
const (
	prefillStallTimeout = 60 * time.Second // request sent -> first token
	genStallTimeout     = 60 * time.Second // first token -> completion, from last token
)

// Adaptive backoff constants.
const (
	cooldown429       = 30 * time.Second
	cooldown5xx       = 10 * time.Second
	cooldownTransport = 15 * time.Second
	recoveryThreshold = 10 // consecutive successes before nudging the limit up
)

// Circuit-breaker (per-model) configuration. Cross-request: every caller of a
// given model id shares one breaker, so a single dead endpoint is detected once
// and all queued / inflight requests fail fast instead of each burning a full
// retry budget. These are constants now; flipping them to env overrides
// (WEKNORA_VLM_CB_*) is a trivial follow-up once a good default is confirmed.
const (
	// cbTripThreshold is how many CONSECUTIVE hard-down events (since the last
	// successful round-trip) trip the breaker open.
	//
	// We deliberately count consecutively rather than within a tight time
	// window. A dead endpoint configured with low concurrency (e.g.
	// concurrency=1) may only produce one failing attempt every ~10s, so a 10s
	// sliding window would never accumulate three failures and the breaker would
	// never open — the exact scenario the user wants to catch. Consecutive
	// counting also resets on any success, so a merely-flaky (mostly-up)
	// endpoint is never tripped, while a genuinely dead one trips after three
	// failures in a row regardless of how slowly they arrive.
	cbTripThreshold = 3
	// cbCooldown is how long the breaker stays open (serverDown) before it
	// allows a single probe request.
	cbCooldown = 30 * time.Second
)

// Circuit-breaker states (stored in modelRuntime.cbState).
const (
	cbServerUp      int32 = iota // normal: requests flow
	cbServerDown                 // open: fail fast, no request issued
	cbServerProbing              // half-open: exactly one probe allowed
)

type priority int

const (
	prioHigh priority = iota // interactive: short-queue fail-open
	prioLow                  // background KB: blocking FIFO
)

func failOpenFor(p priority) time.Duration {
	if p == prioHigh {
		return interactiveFailOpenWait
	}
	return 0 // block indefinitely
}

// ---------------------------------------------------------------------------
// Per-caller counters
// ---------------------------------------------------------------------------

type callerStats struct {
	served    atomic.Int64
	queued    atomic.Int64
	inFlight  atomic.Int64
	cancelled atomic.Int64
}

// ---------------------------------------------------------------------------
// Admission scheduler + adaptive controller, one instance per model id
// ---------------------------------------------------------------------------

type admitResp struct {
	admitted  bool
	soft      bool // true => failed open (no hard permit), counts as soft in-flight
	cancelled bool
}

type waiter struct {
	prio         priority
	resp         chan admitResp
	failOpenWait time.Duration
	deadline     time.Time
}

type (
	releaseMsg struct{}
	cancelMsg  struct{ w *waiter }
)

// modelRuntime owns the per-model admission scheduler, RPM token bucket,
// adaptive state and runtime metrics. All permit accounting happens inside its
// single goroutine (loop) so there is no lost-permit race; result handlers
// mutate only the atomic scalar values.
type modelRuntime struct {
	modelID string

	configuredLimit int
	configuredRPM   int

	// Adaptive (atomic — written by result handlers, read by the loop).
	effectiveLimit atomic.Int64
	effectiveRPM   atomic.Int64
	available      atomic.Bool
	cooldownUntil  atomic.Int64 // unix nanos; advisory (reduces limit, never hard-blocks)
	consecSuccess  atomic.Int64

	// Circuit breaker (cross-request, per model id). Written by result handlers
	// and read on the hot admit path, so it is entirely atomic / mutex-guarded.
	cbState          atomic.Int32 // cbServerUp | cbServerDown | cbServerProbing
	cbProbeAt        atomic.Int64 // unix nanos: earliest time to probe after a trip
	cbProbeRemaining atomic.Int64 // probe permits issued during cbServerProbing
	cbConsecHardDown atomic.Int64 // consecutive hard-downs since last success

	// Scheduler-owned.
	admitCh   chan *waiter
	releaseCh chan releaseMsg
	cancelCh  chan cancelMsg
	stopCh    chan struct{}
	inFlight  int // hard permits held
	highQ     []*waiter
	lowQ      []*waiter

	// RPM bucket (scheduler-owned).
	rpmTokens float64
	rpmLast   time.Time

	// Metrics (atomic where read by stat builder).
	softInflight      atomic.Int64
	retryingInflight  atomic.Int64
	retriedCount      atomic.Int64
	rateLimitedCount  atomic.Int64
	skippedCount      atomic.Int64
	failedCount       atomic.Int64
	prefillStallCount atomic.Int64
	genStallCount     atomic.Int64
	lastTTFTMs        atomic.Int64
	lastTokens        atomic.Int64
	lastGenSpeed      atomic.Uint64 // math.Float64bits of the last gen speed

	perCallerMu sync.Mutex
	perCaller   map[types.CallerType]*callerStats
}

func newModelRuntime(modelID string, configuredLimit, configuredRPM int) *modelRuntime {
	if configuredLimit <= 0 {
		configuredLimit = defaultVLMMaxConcurrency
	}
	if configuredRPM < 0 {
		configuredRPM = 0
	}
	rt := &modelRuntime{
		modelID:         modelID,
		configuredLimit: configuredLimit,
		configuredRPM:   configuredRPM,
		admitCh:         make(chan *waiter, 64),
		releaseCh:       make(chan releaseMsg, 256),
		cancelCh:        make(chan cancelMsg, 64),
		stopCh:          make(chan struct{}),
		rpmTokens:       float64(configuredRPM),
		rpmLast:         time.Now(),
		perCaller:       make(map[types.CallerType]*callerStats),
	}
	rt.effectiveLimit.Store(int64(configuredLimit))
	rt.effectiveRPM.Store(int64(configuredRPM))
	rt.available.Store(true)
	go rt.loop()
	return rt
}

// loop is the single scheduler goroutine. It is the only place that grants or
// frees hard permits, so the permit count can never leak.
func (rt *modelRuntime) loop() {
	for {
		rt.refillRPM()
		limit := int(rt.effectiveLimit.Load())
		rpmLimit := int(rt.effectiveRPM.Load())
		for rt.inFlight < limit && (len(rt.highQ) > 0 || len(rt.lowQ) > 0) {
			if rpmLimit > 0 && rt.rpmTokens < 1 {
				break
			}
			var w *waiter
			if len(rt.highQ) > 0 {
				w, rt.highQ = rt.highQ[0], rt.highQ[1:]
			} else {
				w, rt.lowQ = rt.lowQ[0], rt.lowQ[1:]
			}
			if rpmLimit > 0 {
				rt.rpmTokens--
			}
			rt.admitWaiter(w, false)
		}

		// Arm a timer for the earliest HIGH fail-open deadline.
		var nextWait time.Duration
		hasTimeout := false
		now := time.Now()
		for _, w := range rt.highQ {
			if w.failOpenWait > 0 {
				d := w.deadline.Sub(now)
				if !hasTimeout || d < nextWait {
					nextWait = d
					hasTimeout = true
				}
			}
		}
		var timer *time.Timer
		var timerC <-chan time.Time
		if hasTimeout {
			if nextWait < 0 {
				nextWait = 0
			}
			timer = time.NewTimer(nextWait)
			timerC = timer.C
		}

		select {
		case w := <-rt.admitCh:
			if w.prio == prioHigh {
				rt.highQ = append(rt.highQ, w)
			} else {
				rt.lowQ = append(rt.lowQ, w)
			}
		case <-rt.releaseCh:
			rt.inFlight--
		case cm := <-rt.cancelCh:
			// If the waiter is still queued, withdraw it and tell the caller it
			// was cancelled (exactly one response per waiter, so the caller's
			// w.resp read never blocks). If it was already admitted, the
			// buffered admit response is what the caller will read — do nothing.
			if rt.removeWaiter(cm.w) {
				cm.w.resp <- admitResp{cancelled: true}
			}
		case <-timerC:
			rt.failOpenEarliest()
		case <-rt.stopCh:
			if timer != nil {
				timer.Stop()
			}
			return
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func (rt *modelRuntime) admitWaiter(w *waiter, soft bool) {
	if soft {
		rt.softInflight.Add(1)
	} else {
		rt.inFlight++
	}
	w.resp <- admitResp{admitted: true, soft: soft}
}

// failOpenEarliest admits the HIGH waiter with the earliest passed deadline as
// a soft (fail-open) slot. Called when the timer fires.
func (rt *modelRuntime) failOpenEarliest() {
	now := time.Now()
	best := -1
	for i, w := range rt.highQ {
		if w.failOpenWait > 0 && !w.deadline.After(now) {
			if best == -1 || w.deadline.Before(rt.highQ[best].deadline) {
				best = i
			}
		}
	}
	if best == -1 {
		return
	}
	w := rt.highQ[best]
	rt.highQ = append(rt.highQ[:best], rt.highQ[best+1:]...)
	rt.admitWaiter(w, true)
}

func (rt *modelRuntime) removeWaiter(target *waiter) bool {
	for i, w := range rt.highQ {
		if w == target {
			rt.highQ = append(rt.highQ[:i], rt.highQ[i+1:]...)
			return true
		}
	}
	for i, w := range rt.lowQ {
		if w == target {
			rt.lowQ = append(rt.lowQ[:i], rt.lowQ[i+1:]...)
			return true
		}
	}
	return false
}

func (rt *modelRuntime) refillRPM() {
	rpmLimit := int(rt.effectiveRPM.Load())
	if rpmLimit <= 0 {
		return
	}
	now := time.Now()
	elapsed := now.Sub(rt.rpmLast).Seconds()
	if elapsed <= 0 {
		return
	}
	rt.rpmTokens = minFloat(rt.rpmTokens+elapsed*float64(rpmLimit)/60.0, float64(rpmLimit))
	rt.rpmLast = now
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// admit blocks until the scheduler grants a hard permit (LOW: indefinitely;
// HIGH: up to interactiveFailOpenWait, then fails open), or ctx is cancelled.
// The scheduler is the sole authority on the permit, so there is no lost-permit
// race regardless of which select branch wins.
func (rt *modelRuntime) admit(ctx context.Context, prio priority) (func(), error) {
	w := &waiter{
		prio:         prio,
		resp:         make(chan admitResp, 1),
		failOpenWait: failOpenFor(prio),
	}
	if w.failOpenWait > 0 {
		w.deadline = time.Now().Add(w.failOpenWait)
	}
	rt.admitCh <- w
	select {
	case r := <-w.resp:
		return rt.releaseFor(r), nil
	case <-ctx.Done():
		rt.cancelCh <- cancelMsg{w: w}
		r := <-w.resp
		return rt.releaseFor(r), ctx.Err()
	}
}

func (rt *modelRuntime) releaseFor(r admitResp) func() {
	switch {
	case r.cancelled:
		return func() {}
	case r.soft:
		var once sync.Once
		return func() { once.Do(func() { rt.softInflight.Add(-1) }) }
	default:
		var once sync.Once
		return func() { once.Do(func() { rt.releaseCh <- releaseMsg{} }) }
	}
}

// ---------------------------------------------------------------------------
// Caller stats
// ---------------------------------------------------------------------------

func (rt *modelRuntime) callerStats(caller types.CallerType) *callerStats {
	rt.perCallerMu.Lock()
	cs, ok := rt.perCaller[caller]
	if !ok {
		cs = &callerStats{}
		rt.perCaller[caller] = cs
	}
	rt.perCallerMu.Unlock()
	return cs
}

func (rt *modelRuntime) markQueued(caller types.CallerType) {
	rt.callerStats(caller).queued.Add(1)
}

func (rt *modelRuntime) markAdmitted(caller types.CallerType) {
	cs := rt.callerStats(caller)
	cs.queued.Add(-1)
	cs.inFlight.Add(1)
}

func (rt *modelRuntime) markReleased(caller types.CallerType) {
	rt.callerStats(caller).inFlight.Add(-1)
	rt.callerStats(caller).served.Add(1)
}

func (rt *modelRuntime) markCancelled(caller types.CallerType) {
	cs := rt.callerStats(caller)
	cs.queued.Add(-1)
	cs.cancelled.Add(1)
}

func (rt *modelRuntime) markFirstToken(ttft time.Duration) {
	rt.lastTTFTMs.Store(ttft.Milliseconds())
}

func (rt *modelRuntime) markPrefillStall() { rt.prefillStallCount.Add(1) }
func (rt *modelRuntime) markGenStall()     { rt.genStallCount.Add(1) }

// ---------------------------------------------------------------------------
// Adaptive feedback
// ---------------------------------------------------------------------------

func (rt *modelRuntime) onRateLimited(retryAfter time.Duration) {
	rt.rateLimitedCount.Add(1)
	rt.consecSuccess.Store(0)
	rt.available.Store(false)
	cd := cooldown429
	if retryAfter > cd {
		cd = retryAfter
	}
	rt.cooldownUntil.Store(time.Now().Add(cd).UnixNano())
	rt.scaleLimit(0.5)
	rt.scaleRPM(0.5)
}

func (rt *modelRuntime) onServerError(is5xx bool) {
	rt.failedCount.Add(1)
	rt.consecSuccess.Store(0)
	rt.available.Store(false)
	if is5xx {
		rt.cooldownUntil.Store(time.Now().Add(cooldown5xx).UnixNano())
		rt.scaleLimit(0.7)
	} else {
		rt.cooldownUntil.Store(time.Now().Add(cooldownTransport).UnixNano())
		rt.scaleLimit(0.5)
	}
}

func (rt *modelRuntime) onTimeout(observedAfterAdmission bool) {
	rt.failedCount.Add(1)
	rt.consecSuccess.Store(0)
	// A timeout seen after admission (server was prefilling / generating)
	// usually means a stuck provider — shed load like a transport error.
	if observedAfterAdmission {
		rt.available.Store(false)
		rt.cooldownUntil.Store(time.Now().Add(cooldownTransport).UnixNano())
		rt.scaleLimit(0.5)
	}
}

func (rt *modelRuntime) onEmptyContent() {
	rt.skippedCount.Add(1)
	rt.consecSuccess.Store(0)
}

// onClientError records a permanent client-side fault (a 4xx that is neither
// 429 nor 408). Unlike a server or transport failure it carries no signal about
// the provider's capacity, so it must NOT shed load or start a cooldown: doing
// so would let one misconfigured request halve concurrency and cool the model
// down for everyone else.
func (rt *modelRuntime) onClientError() {
	rt.failedCount.Add(1)
	rt.consecSuccess.Store(0)
}

// markRetried counts in-place retries; beginRetryWait/endRetryWait track slots
// that are held but idle while a request sleeps between retries, so the stat
// does not show a busy in-flight while nothing is actually running.
func (rt *modelRuntime) markRetried() { rt.retriedCount.Add(1) }

func (rt *modelRuntime) beginRetryWait() { rt.retryingInflight.Add(1) }

func (rt *modelRuntime) endRetryWait() { rt.retryingInflight.Add(-1) }

func (rt *modelRuntime) onSuccess(tokens int, genDur time.Duration) {
	rt.consecSuccess.Add(1)
	if tokens > 0 {
		rt.lastTokens.Store(int64(tokens))
		if genDur > 0 {
			rt.lastGenSpeed.Store(math.Float64bits(float64(tokens) / genDur.Seconds()))
		}
	}
	if rt.consecSuccess.Load() >= recoveryThreshold {
		cur := rt.effectiveLimit.Load()
		if cur < int64(rt.configuredLimit) {
			rt.effectiveLimit.Store(cur + 1)
		}
		if rt.effectiveRPM.Load() < int64(rt.configuredRPM) {
			rt.effectiveRPM.Add(1)
		}
		rt.consecSuccess.Store(0)
	}
	if !rt.available.Load() && rt.effectiveLimit.Load() >= int64(rt.configuredLimit) {
		rt.available.Store(true)
	}
}

func (rt *modelRuntime) scaleLimit(factor float64) {
	cur := rt.effectiveLimit.Load()
	// Floor the intended ratio, but absorb the tiny floating-point error that
	// would otherwise shave a slot off e.g. 10 * 0.7 (6.9999… → 6 instead of
	// 7). The epsilon is far below the 0.5 step between distinct integers, so a
	// true 2.5 still floors to 2.
	next := int64(math.Floor(float64(cur)*factor + 1e-9))
	if next < 1 {
		next = 1
	}
	if next > int64(rt.configuredLimit) {
		next = int64(rt.configuredLimit)
	}
	rt.effectiveLimit.Store(next)
}

func (rt *modelRuntime) scaleRPM(factor float64) {
	if rt.configuredRPM <= 0 {
		return
	}
	cur := rt.effectiveRPM.Load()
	next := int64(float64(cur) * factor)
	if next < 1 {
		next = 1
	}
	if next > int64(rt.configuredRPM) {
		next = int64(rt.configuredRPM)
	}
	rt.effectiveRPM.Store(next)
}

// ---------------------------------------------------------------------------
// Circuit breaker (cross-request, per model id)
// ---------------------------------------------------------------------------

// errServerDown is the error surfaced to callers when the breaker is open: the
// per-model circuit breaker has tripped because the endpoint is unreachable, so
// the request is turned away without ever hitting the model. It carries the
// ErrServerDown sentinel (for callers that import vlm and can errors.Is it) AND
// an api.TransportError in the "send request" phase, so callers that only
// inspect the stable api.* types — e.g. the image task handler on the #3746
// branch, which cannot import the vlm sentinel — still classify it as a
// permanent, server-down failure and skip the task retry.
func errServerDown() error {
	return fmt.Errorf("%w: %w", ErrServerDown, &api.TransportError{
		Op:  "send request",
		Err: errors.New("circuit breaker open: model endpoint is down"),
	})
}

// guardCircuit enforces the per-model circuit breaker. It returns nil when the
// request may proceed, or errServerDown (or the caller's context error) when it
// must fail fast. It is called once at the top of every call — BEFORE queuing
// or admission — so a tripped breaker costs no slot, no wait and no retry:
// the 100 requests piled behind a dead endpoint all surface immediately instead
// of each burning a full retry budget.
func (rt *modelRuntime) guardCircuit(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	state := rt.cbState.Load()
	switch state {
	case cbServerUp:
		return nil
	case cbServerDown:
		if time.Now().Before(time.Unix(0, rt.cbProbeAt.Load())) {
			return errServerDown()
		}
		// Cooldown elapsed: transition to probing and issue a single probe
		// permit, then let this request be that probe.
		if rt.cbState.CompareAndSwap(cbServerDown, cbServerProbing) {
			rt.cbProbeRemaining.Store(1)
		}
		fallthrough
	case cbServerProbing:
		if rt.cbProbeRemaining.Add(-1) >= 0 {
			return nil // this request is the allowed probe
		}
		return errServerDown()
	}
	return nil
}

// cbRecordFailure records a connection-level (hard-down) failure and trips the
// breaker once cbTripThreshold CONSECUTIVE hard-downs have occurred since the
// last success. A failed probe (state == probing) re-arms the breaker as down
// and restarts the cooldown, so an endpoint that is still dead after a probe
// does not get stuck in the probing state. While the breaker is already open
// (serverDown) it is a no-op: the cooldown already covers the outage, so
// repeated hard-downs during a blackout cannot re-extend it.
func (rt *modelRuntime) cbRecordFailure() {
	switch rt.cbState.Load() {
	case cbServerDown:
		return // already open; cooldown covers the outage
	case cbServerProbing:
		// The single probe failed: still down, restart the cooldown.
		rt.cbTrip(time.Now())
		return
	}
	// cbServerUp: count consecutive hard-downs since the last success.
	n := rt.cbConsecHardDown.Add(1)
	if n >= cbTripThreshold {
		rt.cbTrip(time.Now())
	}
}

// cbTrip opens the breaker (serverDown) and arms the probe time. Safe to call
// repeatedly: it only transitions from up/probing and only resets the
// consecutive counter on the transition.
func (rt *modelRuntime) cbTrip(now time.Time) {
	if rt.cbState.CompareAndSwap(cbServerUp, cbServerDown) ||
		rt.cbState.CompareAndSwap(cbServerProbing, cbServerDown) {
		rt.cbProbeAt.Store(now.Add(cbCooldown).UnixNano())
		rt.cbConsecHardDown.Store(0)
	}
}

// cbOnSuccess closes the breaker on a successful (HTTP 200) round-trip, whether
// or not it carried content. A server that answered at all is reachable, so the
// outage is over: the state is closed and the consecutive-failure counter is
// cleared.
func (rt *modelRuntime) cbOnSuccess() {
	if rt.cbState.CompareAndSwap(cbServerProbing, cbServerUp) ||
		rt.cbState.CompareAndSwap(cbServerDown, cbServerUp) {
		rt.cbConsecHardDown.Store(0)
	}
}

// ---------------------------------------------------------------------------
// Runtime stat snapshot (for the admin API)
// ---------------------------------------------------------------------------

// CallerSnapshot is the per-caller slice of a VLM runtime stat.
type CallerSnapshot struct {
	Served    int64 `json:"served"`
	Queued    int64 `json:"queued"`
	InFlight  int64 `json:"in_flight"`
	Cancelled int64 `json:"cancelled"`
}

// RuntimeStat is a point-in-time view of one model's VLM admission and
// adaptive state.
type RuntimeStat struct {
	ModelID              string                    `json:"model_id"`
	ConfiguredLimit      int                       `json:"configured_limit"`
	EffectiveLimit       int                       `json:"effective_limit"`
	ConfiguredRPM        int                       `json:"configured_rpm"`
	EffectiveRPM         int                       `json:"effective_rpm"`
	Available            bool                      `json:"available"`
	CooldownRemainingSec int                       `json:"cooldown_remaining_sec"`
	CircuitState         string                    `json:"circuit_state"`
	CircuitCooldownSec   int                       `json:"circuit_cooldown_sec"`
	InFlight             int                       `json:"in_flight"`
	SoftInFlight         int                       `json:"soft_in_flight"`
	RetryingInFlight     int                       `json:"retrying_in_flight"`
	RetriedCount         int64                     `json:"retried_count"`
	RateLimitedCount     int64                     `json:"rate_limited_count"`
	SkippedCount         int64                     `json:"skipped_count"`
	FailedCount          int64                     `json:"failed_count"`
	PrefillStallCount    int64                     `json:"prefill_stall_count"`
	GenStallCount        int64                     `json:"gen_stall_count"`
	LastTTFTMs           int64                     `json:"last_ttft_ms"`
	LastTokens           int64                     `json:"last_tokens"`
	LastGenSpeed         float64                   `json:"last_gen_speed"`
	Callers              map[string]CallerSnapshot `json:"callers"`
}

// Snapshot builds a point-in-time view.
func (rt *modelRuntime) Snapshot() RuntimeStat {
	rt.perCallerMu.Lock()
	callers := make(map[string]CallerSnapshot, len(rt.perCaller))
	for c, cs := range rt.perCaller {
		callers[string(c)] = CallerSnapshot{
			Served:    cs.served.Load(),
			Queued:    cs.queued.Load(),
			InFlight:  cs.inFlight.Load(),
			Cancelled: cs.cancelled.Load(),
		}
	}
	rt.perCallerMu.Unlock()

	cd := time.Until(time.Unix(0, rt.cooldownUntil.Load())).Seconds()
	if cd < 0 {
		cd = 0
	}
	cbState := "server_up"
	switch rt.cbState.Load() {
	case cbServerDown:
		cbState = "server_down"
	case cbServerProbing:
		cbState = "server_probing"
	}
	circuitCD := time.Until(time.Unix(0, rt.cbProbeAt.Load())).Seconds()
	if circuitCD < 0 {
		circuitCD = 0
	}
	return RuntimeStat{
		ModelID:              rt.modelID,
		ConfiguredLimit:      rt.configuredLimit,
		EffectiveLimit:       int(rt.effectiveLimit.Load()),
		ConfiguredRPM:        rt.configuredRPM,
		EffectiveRPM:         int(rt.effectiveRPM.Load()),
		Available:            rt.available.Load(),
		CooldownRemainingSec: int(cd),
		CircuitState:         cbState,
		CircuitCooldownSec:   int(circuitCD),
		InFlight:             rt.inFlight,
		SoftInFlight:         int(rt.softInflight.Load()),
		RetryingInFlight:     int(rt.retryingInflight.Load()),
		RetriedCount:         rt.retriedCount.Load(),
		RateLimitedCount:     rt.rateLimitedCount.Load(),
		SkippedCount:         rt.skippedCount.Load(),
		FailedCount:          rt.failedCount.Load(),
		PrefillStallCount:    rt.prefillStallCount.Load(),
		GenStallCount:        rt.genStallCount.Load(),
		LastTTFTMs:           rt.lastTTFTMs.Load(),
		LastTokens:           rt.lastTokens.Load(),
		LastGenSpeed:         math.Float64frombits(rt.lastGenSpeed.Load()),
		Callers:              callers,
	}
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

type vlmRegistry struct {
	mu       sync.Mutex
	runtimes map[string]*modelRuntime
}

var defaultRegistry = &vlmRegistry{runtimes: make(map[string]*modelRuntime)}

// runtimeFor returns the shared per-model runtime, creating it on first use.
// configuredLimit / configuredRPM are the model's own caps (0 → default 32 /
// unlimited); they are only applied at creation, so the adaptive state of an
// existing runtime is never clobbered by a later call.
func (reg *vlmRegistry) runtimeFor(modelID string, configuredLimit, configuredRPM int) *modelRuntime {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	rt, ok := reg.runtimes[modelID]
	if !ok {
		rt = newModelRuntime(modelID, configuredLimit, configuredRPM)
		reg.runtimes[modelID] = rt
	}
	return rt
}

// RuntimeStats returns snapshots for every model currently tracked.
func RuntimeStats() []RuntimeStat {
	defaultRegistry.mu.Lock()
	out := make([]RuntimeStat, 0, len(defaultRegistry.runtimes))
	for _, rt := range defaultRegistry.runtimes {
		out = append(out, rt.Snapshot())
	}
	defaultRegistry.mu.Unlock()
	sortVLMStats(out)
	return out
}

func sortVLMStats(s []RuntimeStat) {
	sort.Slice(s, func(i, j int) bool { return s[i].ModelID < s[j].ModelID })
}
