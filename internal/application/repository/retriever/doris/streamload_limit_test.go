package doris

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// streamLoadWithBody drives one Stream Load against a server that answers with
// the given status and body, and returns the error the caller sees.
func streamLoadWithBody(t *testing.T, status int, body string) error {
	t.Helper()
	repo, _, _, cleanup := newTestRepo(t)
	defer cleanup()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	repo.feHTTPBase = srv.URL

	return repo.streamLoadOnce(context.Background(), "t", []string{"id"},
		[]map[string]any{{"id": "x"}})
}

// A successful control response is a few hundred bytes of JSON. Anything past
// the cap is refused instead of being buffered, so a misbehaving FE/BE (or a
// proxy that rewrites the endpoint) cannot decide how much memory one load
// allocates.
func TestStreamLoadOnceRejectsOversizedResponse(t *testing.T) {
	// A JSON document just past the cap: still valid JSON, so the only reason to
	// refuse it is its size.
	oversized := `{"Status":"Success","Label":"` + strings.Repeat("x", maxStreamLoadResponseBytes) + `"}`

	err := streamLoadWithBody(t, http.StatusOK, oversized)

	require.Error(t, err, "a response past the cap must be refused")
	assert.Contains(t, err.Error(), fmt.Sprintf("exceeds %d bytes", maxStreamLoadResponseBytes))
}

// A failed load echoes the vendor's message, but only as a bounded preview: the
// whole body used to be concatenated into the returned error, which callers log.
func TestStreamLoadOnceBoundsFailedResponsePreview(t *testing.T) {
	failure := strings.Repeat("y", 8*maxStreamLoadErrorPreviewBytes)

	err := streamLoadWithBody(t, http.StatusInternalServerError, failure)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stream load HTTP 500")
	assert.LessOrEqual(t, len(err.Error()), maxStreamLoadErrorPreviewBytes+len("stream load HTTP 500: "))
}

// The preview is cut at a rune boundary, so a multi-byte vendor message does not
// put invalid UTF-8 into the error and the log line it ends up in.
func TestStreamLoadOnceKeepsErrorPreviewValidUTF8(t *testing.T) {
	// Two-byte characters: a byte-count cut at an odd offset would split one.
	failure := strings.Repeat("中", 3*maxStreamLoadErrorPreviewBytes/2)

	err := streamLoadWithBody(t, http.StatusBadGateway, failure)

	require.Error(t, err)
	assert.True(t, utf8.ValidString(err.Error()), "error message is not valid UTF-8: %q", err.Error())
}

// A body that does not decode is reported with a bounded raw preview, not the
// whole body.
func TestStreamLoadOnceBoundsUndecodableResponsePreview(t *testing.T) {
	undecodable := strings.Repeat("z", 8*maxStreamLoadErrorPreviewBytes)

	err := streamLoadWithBody(t, http.StatusOK, undecodable)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode stream load response")
	assert.LessOrEqual(t, len(err.Error()), maxStreamLoadErrorPreviewBytes+256,
		"the raw preview in a decode error must stay bounded")
}

// The ordinary answers must keep working: a small success body parses, and a
// small failure body still reads as a Doris status failure rather than a
// transport problem.
func TestStreamLoadOnceKeepsSmallResponsesIntact(t *testing.T) {
	repo, _, _, cleanup := newTestRepo(t)
	defer cleanup()

	success := map[string]any{"Status": "Success", "Label": "lbl", "NumberLoadedRows": 1}
	body, err := json.Marshal(success)
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	repo.feHTTPBase = srv.URL

	require.NoError(t, repo.streamLoadOnce(context.Background(), "t", []string{"id"},
		[]map[string]any{{"id": "x"}}))

	failed := map[string]any{"Status": "Fail", "Message": "label exists"}
	body, err = json.Marshal(failed)
	require.NoError(t, err)
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})

	err = repo.streamLoadOnce(context.Background(), "t", []string{"id"},
		[]map[string]any{{"id": "x"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stream load failed")
	assert.Contains(t, err.Error(), "label exists")
}
