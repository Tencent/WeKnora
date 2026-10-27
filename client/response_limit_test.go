package client

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fillerReader streams n bytes without allocating them, so a test can hand the
// SDK a response body far larger than the cap without spending memory itself.
type fillerReader struct {
	remaining int64
}

func (f *fillerReader) Read(p []byte) (int, error) {
	if f.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > f.remaining {
		p = p[:f.remaining]
	}
	for i := range p {
		p[i] = 'x'
	}
	f.remaining -= int64(len(p))
	return len(p), nil
}

func newTestResponse(status int, body io.Reader) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(body)}
}

// A non-2xx body is copied into APIError.Body and interpolated into
// APIError.Error(), so it must not be able to grow without bound.
func TestParseResponseCapsErrorBody(t *testing.T) {
	t.Run("oversized body is truncated to the cap", func(t *testing.T) {
		const oversized = maxErrorBodyBytes * 4

		err := parseResponse(newTestResponse(http.StatusBadGateway, &fillerReader{remaining: oversized}), nil)

		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("parseResponse() = %v, want *APIError", err)
		}
		if got := len(apiErr.Body); got != maxErrorBodyBytes {
			t.Errorf("len(APIError.Body) = %d, want the %d byte cap", got, maxErrorBodyBytes)
		}
	})

	t.Run("body exactly at the cap is kept whole", func(t *testing.T) {
		body := strings.Repeat("a", maxErrorBodyBytes)

		err := parseResponse(newTestResponse(http.StatusInternalServerError, strings.NewReader(body)), nil)

		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("parseResponse() = %v, want *APIError", err)
		}
		if len(apiErr.Body) != maxErrorBodyBytes {
			t.Errorf("len(APIError.Body) = %d, want %d", len(apiErr.Body), maxErrorBodyBytes)
		}
	})
}

// The cap must not disturb the normal error path: a structured body still
// reaches the caller verbatim and its {"code":N} is still extracted.
func TestParseResponseKeepsSmallErrorBody(t *testing.T) {
	const body = `{"code":1003,"msg":"knowledge not found"}`

	err := parseResponse(newTestResponse(http.StatusNotFound, strings.NewReader(body)), nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("parseResponse() = %v, want *APIError", err)
	}
	if apiErr.Body != body {
		t.Errorf("APIError.Body = %q, want %q", apiErr.Body, body)
	}
	if apiErr.Code != ServerErrNotFound {
		t.Errorf("APIError.Code = %d, want %d", apiErr.Code, ServerErrNotFound)
	}
	if !strings.Contains(apiErr.Error(), body) {
		t.Errorf("APIError.Error() = %q, want it to carry the server body", apiErr.Error())
	}
}

// The cap only applies to failures; a successful response still decodes.
func TestParseResponseStillDecodesSuccessBody(t *testing.T) {
	var got struct {
		Name string `json:"name"`
	}

	if err := parseResponse(newTestResponse(http.StatusOK, strings.NewReader(`{"name":"ok"}`)), &got); err != nil {
		t.Fatalf("parseResponse() = %v, want nil", err)
	}
	if got.Name != "ok" {
		t.Errorf("decoded name = %q, want %q", got.Name, "ok")
	}
}
