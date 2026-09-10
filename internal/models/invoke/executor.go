package invoke

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/models/limiter"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// Executor is the system's ONLY HTTP egress for model calls (design §6.3). It
// carries per-model concurrency (the limiter package's Redis ZSET distributed
// semaphore, semantics unchanged), timeout defaults per model kind, the
// unified SSRF gate (no vendor exceptions), retry/backoff, and stream
// demultiplexing. Adapters only describe the call; they never touch HTTP.

// Executor performs requests on behalf of the entry layer.
type Executor struct {
	client *http.Client
}

// rpmTpmPlaceholder is the reserved slot for per-model rate limiting (design
// §13.1: RPM/TPM deferred by user ruling). Filling it later means replacing
// this no-op with a token-bucket acquire — the Do call shape stays unchanged.
func rpmTpmPlaceholder(_ context.Context, _ ModelKey) func() {
	return func() {}
}

// NewExecutor builds the executor on the SSRF-safe client (same transport
// posture as v1 chat/transport.go: per-request deadline via context, not
// http.Client.Timeout, so streams are not cut mid-flight).
func NewExecutor() *Executor {
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         secutils.SSRFSafeDialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     90 * time.Second,
		MaxIdleConnsPerHost: 5,
	}
	return &Executor{
		client: secutils.NewSSRFSafeHTTPClientWithTransport(
			secutils.SSRFSafeHTTPClientConfig{Timeout: 0, MaxRedirects: 10},
			transport,
		),
	}
}

// envDurationSeconds reads a seconds-valued env var; parse failure or a
// non-positive value falls back. (Same semantics as v1.)
func envDurationSeconds(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return time.Duration(n) * time.Second
}

// defaultTimeout maps model kinds to the v1 timeout defaults (zero drift; all
// legacy env vars keep their names and values — design §6.3/§11):
//   - chat:      WEKNORA_LLM_CHAT_TIMEOUT_SECONDS   (default 300s)
//   - stream:    WEKNORA_LLM_STREAM_TIMEOUT_SECONDS (default 600s)
//   - asr:       fixed 300s
//   - embedding/rerank: chat timeout (no legacy env of their own)
//
// VLM calls ride the chat facet through Chat(); callers that need the legacy
// VLM_HTTP_TIMEOUT_SECONDS (180s) posture pass a ctx deadline or an adapter
// sets Request.Timeout (P1c migration detail).
func defaultTimeout(kind ModelKind) time.Duration {
	switch kind {
	case ModelKindChatStream:
		return envDurationSeconds("WEKNORA_LLM_STREAM_TIMEOUT_SECONDS", 600*time.Second)
	case ModelKindASR:
		return 300 * time.Second
	default: // chat, embedding, rerank
		return envDurationSeconds("WEKNORA_LLM_CHAT_TIMEOUT_SECONDS", 300*time.Second)
	}
}

// RawResult is the executor's raw response product handed to adapter Parse*
// methods. For Stream requests, Stream is the live body reader whose
// exhaustion (EOF) or Close releases the concurrency slot.
type RawResult struct {
	Status int
	Header http.Header
	Body   []byte
	Stream io.ReadCloser
}

// Do executes one request. It resolves the timeout (Request.Timeout wins, else
// the kind default, and an existing ctx deadline always wins — v1
// withLLMTimeout semantics), holds the per-model concurrency slot for the
// round-trip, retries replayable bodies on retryable failures, and routes
// through the unified SSRF gate with whitelist guidance on rejection.
func (e *Executor) Do(ctx context.Context, key ModelKey, req *Request) (*RawResult, error) {
	timeout := req.Timeout
	if timeout == 0 {
		timeout = defaultTimeout(key.Kind)
	}
	// Respect an upstream deadline; only apply the fallback when none exists.
	// For Stream requests the fallback cancel must NOT fire when Do returns —
	// the body is still being read. It is tied to the stream lifetime via the
	// slotReleasingReader release hook below (bug found by P1b reconciliation).
	var cancel context.CancelFunc
	if _, ok := ctx.Deadline(); !ok {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	release := limiter.GateNamedN(ctx, key.ModelID, key.ModelName, key.ConcurrencyLimit)

	if req.Stream {
		// Stream slots live beyond Do: release on reader EOF/Close instead of
		// a deferred release here, and release immediately when the attempt
		// itself failed (no stream to attach to).
		result, err := e.attemptOnce(ctx, req)
		if err != nil {
			if cancel != nil {
				cancel()
			}
			release()
			return nil, err
		}
		if rr, ok := result.Stream.(*slotReleasingReader); ok {
			rr.attachRelease(func() {
				release()
				if cancel != nil {
					cancel()
				}
			})
		} else {
			defer release()
			defer func() {
				if cancel != nil {
					cancel()
				}
			}()
		}
		return result, nil
	}
	defer release()
	defer func() {
		if cancel != nil {
			cancel()
		}
	}() // non-stream: round-trip ends here
	defer rpmTpmPlaceholder(ctx, key)() // RPM/TPM reserved slot (§13.1), passthrough this cycle
	return WithProviderRetry(ctx, func(ctx context.Context) (*RawResult, error) {
		return e.attemptOnce(ctx, req)
	})
}

// attemptOnce performs one HTTP round-trip and classifies non-2xx responses
// into the unified error model (the retry wrapper above decides by Kind).
func (e *Executor) attemptOnce(ctx context.Context, req *Request) (*RawResult, error) {
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bodyReader(req))
	if err != nil {
		return nil, ClassifyError(err)
	}
	for k, vs := range req.Header {
		for _, v := range vs {
			httpReq.Header.Add(k, v)
		}
	}
	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, ClassifyError(withSSRFWhitelistHint(err, req.URL))
	}

	if req.Stream {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
			return nil, ClassifyStatusBody(resp.StatusCode, string(body))
		}
		return &RawResult{
			Status: resp.StatusCode,
			Header: resp.Header,
			Stream: newSlotReleasingReader(resp.Body),
		}, nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, ClassifyError(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ClassifyStatusBody(resp.StatusCode, string(body))
	}
	return &RawResult{Status: resp.StatusCode, Header: resp.Header, Body: body}, nil
}

func bodyReader(req *Request) io.Reader {
	if req.BodyStream != nil {
		return req.BodyStream
	}
	if len(req.Body) == 0 {
		return nil
	}
	return strings.NewReader(string(req.Body))
}

// withSSRFWhitelistHint annotates SSRF rejections with the whitelist guidance
// the deployment docs reference (design §11: local Ollama hosts must be added
// to SSRF_WHITELIST once when upgrading local deployments).
func withSSRFWhitelistHint(err error, rawURL string) error {
	msg := err.Error()
	if !strings.Contains(msg, "SSRF") {
		return err
	}
	host := rawURL
	if u, perr := url.Parse(rawURL); perr == nil {
		host = u.Host
	}
	return fmt.Errorf("%w (blocked host %q: add it to SSRF_WHITELIST to allow this provider endpoint)", err, host)
}

// slotReleasingReader wraps a stream body so the concurrency slot releases on
// EOF or Close — whichever comes first (v1 concurrency_wrapper.go semantics:
// the consumer abandoning the stream must not leak the slot).
type slotReleasingReader struct {
	rc      io.ReadCloser
	release func()
	once    sync.Once
}

func newSlotReleasingReader(rc io.ReadCloser) *slotReleasingReader {
	return &slotReleasingReader{rc: rc}
}

func (r *slotReleasingReader) attachRelease(release func()) { r.release = release }

func (r *slotReleasingReader) Read(p []byte) (int, error) {
	n, err := r.rc.Read(p)
	if err == io.EOF {
		r.releaseSlot()
	}
	return n, err
}

func (r *slotReleasingReader) Close() error {
	r.releaseSlot()
	return r.rc.Close()
}

func (r *slotReleasingReader) releaseSlot() {
	r.once.Do(func() {
		if r.release != nil {
			r.release()
		}
	})
}

// --- Stream demultiplexing (executor-side shared tools, design §6.3/§6.6) ---

// Demuxer turns a raw stream body into demuxed StreamChunks. SSE frames carry
// their event name; NDJSON lines arrive as Event="".
type Demuxer struct {
	scanner      *bufio.Scanner
	sse          bool
	done         bool
	pendingEvent string
}

// NewDemuxer picks the wire format from the response Content-Type:
// text/event-stream → SSE, anything else → NDJSON (Ollama native).
func NewDemuxer(contentType string, r io.Reader) *Demuxer {
	sse := strings.Contains(contentType, "text/event-stream")
	d := &Demuxer{sse: sse}
	d.scanner = bufio.NewScanner(r)
	buf := make([]byte, 1024*1024) // long thinking chains produce very long lines
	d.scanner.Buffer(buf, 1024*1024)
	return d
}

// Next returns the next demuxed chunk; ok=false ends the stream (EOF or
// unrecoverable read error). The "[DONE]" sentinel becomes Event="done".
func (d *Demuxer) Next() (StreamChunk, bool) {
	for d.scanner.Scan() {
		line := d.scanner.Text()
		if d.sse {
			switch {
			case line == "":
				d.pendingEvent = ""
				continue
			case line == "data: [DONE]":
				d.done = true
				return StreamChunk{Event: "done"}, true
			case strings.HasPrefix(line, "data: "):
				return StreamChunk{Event: d.pendingEvent, Data: []byte(line[6:])}, true
			case strings.HasPrefix(line, "data:"):
				return StreamChunk{Event: d.pendingEvent, Data: []byte(line[5:])}, true
			case strings.HasPrefix(line, "event:"):
				// The event name decorates the NEXT data frame.
				d.pendingEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
				continue
			default:
				continue // id:, retry:, comments
			}
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		return StreamChunk{Data: []byte(line)}, true
	}
	return StreamChunk{}, false
}

// Err reports a scanner-level read failure (nil on clean EOF).
func (d *Demuxer) Err() error { return d.scanner.Err() }

// decodeJSON is a tiny helper adapters share for chunk payloads.
func decodeJSON(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decode stream chunk: %w", err)
	}
	return nil
}
