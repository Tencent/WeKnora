package vlm

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// fakeResp is one scripted buffered answer for fakeVLM.
type fakeResp struct {
	text string
	err  error
}

// fakeVLM is a fully controllable VLM used to exercise the manager without a
// real provider. It implements both the base VLM interface and the additive
// StreamingVLM interface so the manager can be driven in either mode.
type fakeVLM struct {
	name string
	id   string

	respText string
	respErr  error

	// responses, when non-empty, is consumed one entry per buffered call (the
	// last entry repeats), so a test can script a transient failure followed by
	// a success. calls counts buffered attempts for assertions.
	responses []fakeResp
	calls     atomic.Int64

	streamChunks    []StreamChunk
	firstChunkDelay time.Duration // delays the first streamed token so TTFT is observable
	errDelay        time.Duration // sleeps before each buffered answer (simulates a slow dead endpoint)
}

func (f *fakeVLM) answer() (string, error) {
	if f.errDelay > 0 {
		time.Sleep(f.errDelay)
	}
	n := f.calls.Add(1)
	if len(f.responses) > 0 {
		i := int(n) - 1
		if i >= len(f.responses) {
			i = len(f.responses) - 1
		}
		return f.responses[i].text, f.responses[i].err
	}
	return f.respText, f.respErr
}

func (f *fakeVLM) Predict(_ context.Context, _ [][]byte, _ string) (string, error) {
	return f.answer()
}

func (f *fakeVLM) PredictWithOptions(_ context.Context, _ [][]byte, _ string, _ *PredictOptions) (string, error) {
	return f.answer()
}

func (f *fakeVLM) GetModelName() string { return f.name }
func (f *fakeVLM) GetModelID() string   { return f.id }

func (f *fakeVLM) PredictStream(
	_ context.Context, _ [][]byte, _ string, _ *PredictOptions,
) (<-chan StreamChunk, error) {
	if f.firstChunkDelay > 0 {
		time.Sleep(f.firstChunkDelay)
	}
	ch := make(chan StreamChunk, len(f.streamChunks)+1)
	for _, c := range f.streamChunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

// usageReporterFake captures the last reported usage.
type usageReporterFake struct {
	mu   sync.Mutex
	last types.TokenUsage
}

func (u *usageReporterFake) Report(_ string, usage types.TokenUsage) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.last = usage
}

func newManager(t *testing.T, inner VLM, id string, streaming bool) *managerVLM {
	t.Helper()
	// Ensure the runtime exists with a generous limit so admission is immediate.
	defaultRegistry.runtimeFor(id, 32, 0)
	return &managerVLM{
		inner:                  inner,
		modelID:                id,
		modelName:              "fake",
		configuredLimit:        32,
		configuredRPM:          0,
		innerSupportsPWO:       true,
		innerSupportsStreaming: streaming,
	}
}

func TestManagerPriorityRouting(t *testing.T) {
	m := newManager(t, &fakeVLM{id: "rt-prio"}, "rt-prio", false)
	if got := m.priorityFor(types.CallerKBBackground); got != prioLow {
		t.Errorf("KBBackground -> %v, want prioLow", got)
	}
	if got := m.priorityFor(types.CallerUnknown); got != prioHigh {
		t.Errorf("Unknown -> %v, want prioHigh", got)
	}
	if got := m.priorityFor(types.CallerChat); got != prioHigh {
		t.Errorf("Chat -> %v, want prioHigh", got)
	}
}

// TestManagerRawErrorPassThrough pins two things at once: the manager returns
// the exact inner error unwrapped, and a permanent client error is not retried.
func TestManagerRawErrorPassThrough(t *testing.T) {
	id := "rt-raw-err"
	permErr := &api.HTTPError{StatusCode: http.StatusUnauthorized}
	inner := &fakeVLM{id: id, respErr: permErr}
	m := newManager(t, inner, id, false)

	text, err := m.call(context.Background(), [][]byte{testPNG}, "prompt", nil)
	if text != "" {
		t.Errorf("text = %q, want empty", text)
	}
	if err == nil {
		t.Fatal("err = nil, want the raw inner error")
	}
	// The exact same error object must come back — the manager does not wrap a
	// success/failure verdict around it.
	if !errors.Is(err, permErr) {
		t.Errorf("returned err = %v, want it to passthrough %v", err, permErr)
	}
	if got := inner.calls.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 (a permanent client error must not be retried)", got)
	}
	snap := defaultRegistry.runtimeFor(id, 32, 0).Snapshot()
	if snap.FailedCount != 1 {
		t.Errorf("FailedCount = %d, want 1", snap.FailedCount)
	}
	if !snap.Available {
		t.Error("Available must stay true: a permanent client error is not a provider failure")
	}
}

func TestManagerRetriesTransientThenSucceeds(t *testing.T) {
	id := "rt-retry-ok"
	inner := &fakeVLM{
		id: id,
		responses: []fakeResp{
			{err: &api.HTTPError{StatusCode: http.StatusTooManyRequests}},
			{text: "recovered"},
		},
	}
	m := newManager(t, inner, id, false) // unknown caller -> HIGH -> budget 1
	text, err := m.call(context.Background(), [][]byte{testPNG}, "prompt", nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if text != "recovered" {
		t.Errorf("text = %q, want recovered", text)
	}
	if got := inner.calls.Load(); got != 2 {
		t.Errorf("attempts = %d, want 2 (one in-place retry)", got)
	}
	snap := defaultRegistry.runtimeFor(id, 32, 0).Snapshot()
	if snap.RateLimitedCount != 1 {
		t.Errorf("RateLimitedCount = %d, want 1", snap.RateLimitedCount)
	}
	if snap.RetriedCount != 1 {
		t.Errorf("RetriedCount = %d, want 1", snap.RetriedCount)
	}
}

func TestManagerRetryBudgetExhausted(t *testing.T) {
	id := "rt-retry-exhaust"
	inner := &fakeVLM{id: id, responses: []fakeResp{{err: &api.HTTPError{StatusCode: 500}}}}
	m := newManager(t, inner, id, false) // HIGH budget 1 -> 2 attempts total
	_, err := m.call(context.Background(), [][]byte{testPNG}, "prompt", nil)
	if err == nil {
		t.Fatal("err = nil, want the exhausted error")
	}
	if got := inner.calls.Load(); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
	if snap := defaultRegistry.runtimeFor(id, 32, 0).Snapshot(); snap.RetriedCount != 1 {
		t.Errorf("RetriedCount = %d, want 1", snap.RetriedCount)
	}
}

func TestManagerKBBudgetIsTwo(t *testing.T) {
	id := "rt-kb-budget"
	inner := &fakeVLM{id: id, responses: []fakeResp{{err: &api.HTTPError{StatusCode: 503}}}}
	m := newManager(t, inner, id, false)
	ctx := types.WithVLMCaller(context.Background(), types.CallerKBBackground)
	if _, err := m.call(ctx, [][]byte{testPNG}, "prompt", nil); err == nil {
		t.Fatal("err = nil, want the exhausted error")
	}
	if got := inner.calls.Load(); got != 3 {
		t.Errorf("attempts = %d, want 3 (1 + KB budget 2)", got)
	}
}

func TestManagerNoRetryOnPermanent(t *testing.T) {
	id := "rt-perm"
	inner := &fakeVLM{id: id, responses: []fakeResp{{err: &api.HTTPError{StatusCode: http.StatusUnauthorized}}}}
	m := newManager(t, inner, id, false)
	if _, err := m.call(context.Background(), [][]byte{testPNG}, "prompt", nil); err == nil {
		t.Fatal("err = nil, want the raw error")
	}
	if got := inner.calls.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 (permanent errors are not retried)", got)
	}
	snap := defaultRegistry.runtimeFor(id, 32, 0).Snapshot()
	if !snap.Available || snap.EffectiveLimit != 32 {
		t.Errorf("permanent error shed load: available=%v effectiveLimit=%d, want true/32",
			snap.Available, snap.EffectiveLimit)
	}
}

func TestManagerRetryStopsOnContextCancel(t *testing.T) {
	id := "rt-retry-cancel"
	inner := &fakeVLM{id: id, responses: []fakeResp{{err: &api.HTTPError{StatusCode: 429}}}}
	m := newManager(t, inner, id, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a dead caller context must never be retried
	_, err := m.call(ctx, [][]byte{testPNG}, "prompt", nil)
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if got := inner.calls.Load(); got > 1 {
		t.Errorf("attempts = %d, want <= 1 (must not retry a cancelled context)", got)
	}
}

func TestManagerEmptyContentReturnsNil(t *testing.T) {
	id := "rt-empty"
	// Whitespace-only answer is an empty content reply (HTTP 200, no text).
	m := newManager(t, &fakeVLM{id: id, respText: "   "}, id, false)
	text, err := m.call(context.Background(), [][]byte{testPNG}, "prompt", nil)
	if err != nil {
		t.Errorf("err = %v, want nil (empty content is a valid empty reply)", err)
	}
	if strings.TrimSpace(text) != "" {
		t.Errorf("text = %q, want trimmed-empty", text)
	}
	rt := defaultRegistry.runtimeFor(id, 32, 0)
	if snap := rt.Snapshot(); snap.SkippedCount != 1 {
		t.Errorf("SkippedCount = %d, want 1", snap.SkippedCount)
	}
}

func TestManagerSuccessAndUsageReporting(t *testing.T) {
	id := "rt-ok"
	ur := &usageReporterFake{}
	inner := &fakeVLM{
		id: id,
		streamChunks: []StreamChunk{
			{Text: "hello"},
			{Usage: &types.TokenUsage{TotalTokens: 7}},
			{Done: true},
		},
	}
	m := newManager(t, inner, id, true)
	m.usageReporter = ur

	text, err := m.call(context.Background(), [][]byte{testPNG}, "prompt", nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if text != "hello" {
		t.Errorf("text = %q, want hello", text)
	}
	rt := defaultRegistry.runtimeFor(id, 32, 0)
	if snap := rt.Snapshot(); snap.LastTokens != 7 {
		t.Errorf("LastTokens = %d, want 7", snap.LastTokens)
	}
	ur.mu.Lock()
	defer ur.mu.Unlock()
	if ur.last.TotalTokens != 7 {
		t.Errorf("usage reporter last = %d, want 7", ur.last.TotalTokens)
	}
}

func TestManagerStreamingFirstTokenAndOnChunk(t *testing.T) {
	id := "rt-stream"
	inner := &fakeVLM{
		id:              id,
		firstChunkDelay: 3 * time.Millisecond,
		streamChunks: []StreamChunk{
			{Text: "hel"},
			{Text: "lo"},
			{Done: true},
		},
	}
	m := newManager(t, inner, id, true)

	var mu sync.Mutex
	var forwarded []string
	var doneSeen bool
	opts := &PredictOptions{
		OnChunk: func(chunkText string, done bool, _ error) {
			mu.Lock()
			defer mu.Unlock()
			if !done && chunkText != "" {
				forwarded = append(forwarded, chunkText)
			}
			if done {
				doneSeen = true
			}
		},
	}
	text, err := m.call(context.Background(), [][]byte{testPNG}, "prompt", opts)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if text != "hello" {
		t.Errorf("text = %q, want hello", text)
	}
	mu.Lock()
	if len(forwarded) != 2 || forwarded[0] != "hel" || forwarded[1] != "lo" {
		t.Errorf("forwarded chunks = %v, want [hel lo]", forwarded)
	}
	if !doneSeen {
		t.Error("OnChunk done=true not delivered")
	}
	mu.Unlock()
	rt := defaultRegistry.runtimeFor(id, 32, 0)
	if snap := rt.Snapshot(); snap.LastTTFTMs <= 0 {
		t.Errorf("LastTTFTMs = %d, want > 0 (first token observed via streaming)", snap.LastTTFTMs)
	}
}

func TestIsRetryable(t *testing.T) {
	cases := map[ErrorKind]bool{
		KindRateLimited:       true,
		KindServerError:       true,
		KindStreamInterrupted: true,
		KindClientError:       false,
	}
	for kind, want := range cases {
		if got := isRetryable(kind); got != want {
			t.Errorf("isRetryable(%v) = %v, want %v", kind, got, want)
		}
	}
}

func TestRetryWaitGrowsAndHonoursRetryAfter(t *testing.T) {
	// Exponential base 1s / 2s, plus up to 20% jitter.
	if w := retryWait(0, 0); w < time.Second || w > 1200*time.Millisecond {
		t.Errorf("retryWait(0,0) = %v, want [1s,1.2s]", w)
	}
	if w := retryWait(0, 1); w < 2*time.Second || w > 2400*time.Millisecond {
		t.Errorf("retryWait(0,1) = %v, want [2s,2.4s]", w)
	}
	// A longer Retry-After wins over the base.
	if w := retryWait(5*time.Second, 0); w < 5*time.Second || w > 6*time.Second {
		t.Errorf("retryWait(5s,0) = %v, want [5s,6s]", w)
	}
	// The hard cap holds even for an absurd Retry-After.
	if w := retryWait(time.Hour, 0); w > maxRetryWait {
		t.Errorf("retryWait(1h,0) = %v, want <= %v (hard cap)", w, maxRetryWait)
	}
}

func TestVLMCallerSurvivesCloneContext(t *testing.T) {
	ctx := types.WithVLMCaller(context.Background(), types.CallerKBBackground)
	// Mirror logger.CloneContext's key-preserving logic (without importing the
	// logger package) to prove the caller identity is in the clone allow-list.
	clone := context.Background()
	for _, k := range types.ContextKeysClonedAcrossDetach() {
		if v := ctx.Value(k); v != nil {
			clone = context.WithValue(clone, k, v)
		}
	}
	if got := types.VLMCallerFromContext(clone); got != types.CallerKBBackground {
		t.Errorf("caller after clone = %q, want kb_background", got)
	}
	// And the manager routes the cloned context to the low (blocking) tier.
	m := newManager(t, &fakeVLM{id: "rt-clone"}, "rt-clone", false)
	if got := m.priorityFor(types.VLMCallerFromContext(clone)); got != prioLow {
		t.Errorf("priority after clone = %v, want prioLow", got)
	}
}
