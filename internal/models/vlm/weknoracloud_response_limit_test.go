package vlm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// fillerReader streams size copies of a byte without allocating them, so a
// test can offer a body far larger than the response cap without spending the
// memory itself.
type fillerReader struct {
	remaining int64
	value     byte
}

func (f *fillerReader) Read(p []byte) (int, error) {
	if f.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > f.remaining {
		p = p[:f.remaining]
	}
	for i := range p {
		p[i] = f.value
	}
	f.remaining -= int64(len(p))
	return len(p), nil
}

func newTestWeKnoraCloudVLM(serverURL string, client *http.Client) *WeKnoraCloudVLM {
	return &WeKnoraCloudVLM{
		modelName: "test-model",
		appID:     "test-app",
		apiKey:    "test-secret",
		baseURL:   serverURL,
		client:    client,
	}
}

// A bounded read must not change how an ordinary reply is parsed.
func TestPredictAcceptsNormalResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"extracted text"}}]}`)
	}))
	defer server.Close()

	vlm := newTestWeKnoraCloudVLM(server.URL, server.Client())
	content, err := vlm.Predict(context.Background(), nil, "describe this page")
	if err != nil {
		t.Fatalf("Predict() error = %v", err)
	}
	if content != "extracted text" {
		t.Errorf("content = %q, want %q", content, "extracted text")
	}
}

// A successful reply is valid JSON followed by enough padding to exceed the
// provider response cap. Truncating the padding would leave valid JSON, so the
// response has to be refused rather than partially decoded into a short answer.
func TestPredictRejectsOversizedSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
		_, _ = io.Copy(w, &fillerReader{remaining: api.MaxResponseBytes, value: ' '})
	}))
	defer server.Close()

	vlm := newTestWeKnoraCloudVLM(server.URL, server.Client())
	content, err := vlm.Predict(context.Background(), nil, "describe this page")
	if !errors.Is(err, api.ErrResponseTooLarge) {
		t.Fatalf("Predict() = (%q, %v), want %v", content, err, api.ErrResponseTooLarge)
	}
}

// A failed call echoes the vendor body into the returned error. The preview has
// to be bounded so a broken endpoint cannot push an arbitrarily large blob into
// that error and the caller's logs.
func TestPredictBoundsFailedResponsePreview(t *testing.T) {
	const bodySize = 256 << 10
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, strings.Repeat("e", bodySize))
	}))
	defer server.Close()

	vlm := newTestWeKnoraCloudVLM(server.URL, server.Client())
	_, err := vlm.Predict(context.Background(), nil, "describe this page")
	if err == nil {
		t.Fatal("Predict() = nil error, want a failure")
	}
	if !strings.Contains(err.Error(), "status 502") {
		t.Errorf("error = %q, want the HTTP status", err)
	}
	if len(err.Error()) >= bodySize/2 {
		t.Errorf("error echoed %d bytes, want a bounded preview", len(err.Error()))
	}
}

// The preview stops on a byte count, so a multi-byte body can end mid-rune.
// That must not put invalid UTF-8 into the error string and the logs that copy
// it.
func TestPredictTrimsPartialUTF8FromFailedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		// 4096 is not a multiple of the rune's three bytes, so a byte-bounded
		// preview ends inside the next rune.
		_, _ = io.WriteString(w, strings.Repeat("错", 3000))
	}))
	defer server.Close()

	vlm := newTestWeKnoraCloudVLM(server.URL, server.Client())
	_, err := vlm.Predict(context.Background(), nil, "describe this page")
	if err == nil {
		t.Fatal("Predict() = nil error, want a failure")
	}
	if !utf8.ValidString(err.Error()) {
		t.Fatalf("error is not valid UTF-8: %q", err)
	}
}
