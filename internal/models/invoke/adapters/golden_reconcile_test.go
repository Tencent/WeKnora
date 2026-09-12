package adapters

// golden_reconcile_test.go — P1b strangler reconciliation harness (design
// §10.1 hard gate): the same scenario inputs recorded against the v1 funnel
// (internal/models/chat golden_*_test.go) are replayed through the invoke
// adapters, and every outbound request (path / query / allowlisted headers /
// body bytes) must match the v1 baseline in testdata/golden byte-for-byte.
//
// Documented, non-gated deltas (report to Main, same seams AdapterAO flags):
//   - Error.Message: v1 extracted error.message out of the vendor envelope and
//     prefixed the action ("create chat completion: ..."); the executor's
//     ClassifyStatusBody keeps the raw body snippet. Kind/Status reconcile;
//     Message does not.
//   - Stream client view: the entry's mapStreamEvent splits v1's Done+usage
//     chunk and cannot attach partial tool calls to an interrupted stream.
//   - ctx session-ID fallback for the prompt-cache key is caller-side now.

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

const goldenDir = "../testdata/golden"

// goldenCapturedRequest / goldenDoc mirror the v1 recording structs
// (chat/golden_wire_test.go:43-60).
type goldenCapturedRequest struct {
	Path    string            `json:"path"`
	Query   string            `json:"query,omitempty"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body,omitempty"`
}

type goldenDoc struct {
	Scenario string                  `json:"scenario"`
	Behavior []string                `json:"behavior"`
	Requests []goldenCapturedRequest `json:"requests"`
	Response json.RawMessage         `json:"response,omitempty"`
	SSE      []string                `json:"sse_response,omitempty"`
	Client   json.RawMessage         `json:"client,omitempty"`
	Error    json.RawMessage         `json:"error,omitempty"`
}

func loadGoldenDoc(t *testing.T, name string) goldenDoc {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(goldenDir, name+".json"))
	require.NoError(t, err, "golden baseline missing: %s", name)
	var doc goldenDoc
	require.NoError(t, json.Unmarshal(data, &doc))
	return doc
}

// header normalization — same allowlist/mask as the v1 recorder
// (chat/golden_wire_test.go:62-97) so both sides filter identically.
var goldenHeaderAllowlist = map[string]bool{
	"Authorization":      true,
	"X-Api-Key":          true,
	"Api-Key":            true,
	"Anthropic-Version":  true,
	"Content-Type":       true,
	"Accept":             true,
	"X-Appid":            true,
	"X-Session-Affinity": true,
	"X-Dashscope-Sse":    true, // DashScope native streaming (aliyun.go)
}

var goldenHeaderMask = map[string]string{
	"X-Request-Id": "<request-id>",
	"X-Timestamp":  "<unix-seconds>",
	"X-Nonce":      "<nonce>",
	"X-Signature":  "<hmac-md5>",
}

func normalizeGoldenHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for k, vs := range h {
		if v, ok := goldenHeaderMask[k]; ok {
			out[k] = v
			continue
		}
		if !goldenHeaderAllowlist[k] {
			continue
		}
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}

// reconcileServer captures outbound requests exactly like the v1 recorder.
type reconcileServer struct {
	Server *httptest.Server

	mu       sync.Mutex
	requests []goldenCapturedRequest
	rawHeads []http.Header
	calls    int
}

func newReconcileServer(t *testing.T, handler http.HandlerFunc) *reconcileServer {
	t.Helper()
	g := &reconcileServer{}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		g.mu.Lock()
		g.requests = append(g.requests, goldenCapturedRequest{
			Path:    r.URL.Path,
			Query:   r.URL.RawQuery,
			Headers: normalizeGoldenHeaders(r.Header),
			Body:    json.RawMessage(body),
		})
		g.rawHeads = append(g.rawHeads, r.Header.Clone())
		g.calls++
		g.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(g.Server.Close)
	return g
}

func (g *reconcileServer) Calls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

// assertRequestsMatchGolden is the byte-exact hard gate: request count, path,
// query, allowlisted headers and the body bytes (compacted — the golden file
// stores re-indented JSON, so whitespace is normalized on both sides while
// key order, numbers and content stay exact).
func assertRequestsMatchGolden(t *testing.T, name string, g *reconcileServer) {
	t.Helper()
	want := loadGoldenDoc(t, name).Requests
	g.mu.Lock()
	got := append([]goldenCapturedRequest(nil), g.requests...)
	g.mu.Unlock()
	require.Len(t, got, len(want), "request count mismatch for %s", name)
	for i := range want {
		req, exp := got[i], want[i]
		require.Equal(t, exp.Path, req.Path, "%s request[%d] path", name, i)
		require.Equal(t, exp.Query, req.Query, "%s request[%d] query", name, i)
		require.Equal(t, exp.Headers, req.Headers, "%s request[%d] headers", name, i)
		require.True(t, compactJSONEqual(exp.Body, req.Body),
			"%s request[%d] body:\n want: %s\n  got: %s", name, i, exp.Body, req.Body)
	}
}

// assertErrorKind reconciles the classification (Kind/Status). Message text
// drifts by design — see the file header note on the executor seam.
func assertErrorKind(t *testing.T, name string, err error) {
	t.Helper()
	require.Error(t, err)
	var wantErr struct {
		Kind   string `json:"Kind"`
		Status int    `json:"Status"`
	}
	require.NoError(t, json.Unmarshal(loadGoldenDoc(t, name).Error, &wantErr))
	pe, ok := err.(*invoke.ProviderError)
	if !ok {
		pe = invoke.ClassifyError(err)
	}
	require.Equal(t, wantErr.Kind, string(pe.Kind), "%s error kind", name)
	require.Equal(t, wantErr.Status, pe.Status, "%s error status", name)
}

func compactJSONEqual(want, got []byte) bool {
	cw, err1 := compactJSON(want)
	cg, err2 := compactJSON(got)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(cw) == string(cg)
}

func compactJSON(in []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, in); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// newGoldenModelConfig builds the ModelConfig the invoke entry dispatches on
// (mirror of v1 newGoldenRemoteChat's ChatConfig).
func newGoldenModelConfig(
	t *testing.T, baseURL, providerName, model string, mutate func(*invoke.ModelConfig),
) *invoke.ModelConfig {
	t.Helper()
	m := &invoke.ModelConfig{
		Provider:    providerName,
		ModelID:     "golden-" + model,
		ModelName:   model,
		BaseURL:     baseURL,
		Credentials: invoke.Credentials{APIKey: "test-key"},
	}
	if mutate != nil {
		mutate(m)
	}
	return m
}

func allowLoopbackSSRF(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

// --- canned handlers (v1 golden_wire_test.go:230-269) ---

func jsonHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}
}

func sseHandler(lines ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, line := range lines {
			_, _ = fmt.Fprint(w, line+"\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func sequencingHandler(responses ...http.HandlerFunc) (http.HandlerFunc, func() int) {
	var mu sync.Mutex
	i := 0
	h := func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		idx := i
		i++
		mu.Unlock()
		if idx >= len(responses) {
			idx = len(responses) - 1
		}
		responses[idx](w, r)
	}
	return h, func() int { mu.Lock(); defer mu.Unlock(); return i }
}

// drainStream empties a ChatStream channel with a watchdog (no golden compare
// on the stream client view — see the file header delta note).
func drainStream(t *testing.T, ch <-chan types.StreamResponse) {
	t.Helper()
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("stream stalled while draining")
		}
	}
}

// signAssertion ports chat/golden_wire_test.go:275-308: recompute the
// WeKnoraCloud HMAC from the captured signing headers + body and require it
// to match X-Signature (signature correctness over the downgraded body).
func signAssertion(t *testing.T, g *reconcileServer, idx int, appID, appSecret string) {
	t.Helper()
	head := g.rawHeads[idx]
	requestID := head.Get("X-Request-Id")
	timestamp := head.Get("X-Timestamp")
	nonce := head.Get("X-Nonce")
	require.NotEmpty(t, requestID)
	require.Len(t, nonce, 16)
	require.Regexp(t, `^\d+$`, timestamp)

	g.mu.Lock()
	body := string(g.requests[idx].Body)
	g.mu.Unlock()
	bodyMD5 := fmt.Sprintf("%x", md5.Sum([]byte(body)))
	params := map[string]string{
		"x-appid":      appID,
		"x-api-key":    appSecret,
		"x-request-id": requestID,
		"x-timestamp":  timestamp,
		"x-nonce":      nonce,
		"body":         bodyMD5,
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, rfc3986EncodeForTest(k)+"="+rfc3986EncodeForTest(params[k]))
	}
	signature := fmt.Sprintf("%x", md5.Sum([]byte(strings.Join(parts, "&"))))
	require.Equal(t, signature, head.Get("X-Signature"), "recomputed HMAC must match captured X-Signature")
	require.Equal(t, appID, head.Get("X-Appid"))
	require.Equal(t, appSecret, head.Get("X-Api-Key"))
}

// rfc3986EncodeForTest mirrors internal/models/invoke rfc3986Encode.
func rfc3986EncodeForTest(s string) string {
	var buf strings.Builder
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' ||
			r == '.' || r == '~' {
			buf.WriteRune(r)
		} else {
			fmt.Fprintf(&buf, "%%%02X", r)
		}
	}
	return buf.String()
}

// --- SSE payload builders (v1 golden_wire_test.go:325-334) ---

const openaiChunkFormat = `{"id":%q,"object":"chat.completion.chunk","created":1700000000,` +
	`"model":"golden-model","choices":[{"index":0,"delta":%s,"finish_reason":null}]}`

func openaiChunk(id string, delta string) string {
	return fmt.Sprintf(openaiChunkFormat, id, delta)
}

const openaiUsageChunkFormat = `{"id":%q,"object":"chat.completion.chunk","created":1700000000,` +
	`"model":"golden-model","choices":[],"usage":%s}`

func openaiUsageChunk(id string, usage string) string {
	return fmt.Sprintf(openaiUsageChunkFormat, id, usage)
}
