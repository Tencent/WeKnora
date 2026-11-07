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

// A successful body must not be able to grow without bound either: the decoder
// streams it, so an oversized envelope would be buffered up to the cap before
// json.Decoder ever saw the end of it.
func TestParseResponseCapsSuccessBody(t *testing.T) {
	t.Run("oversized body is rejected", func(t *testing.T) {
		// Valid JSON that only ends past the cap, so the decoder keeps reading
		// instead of failing on the first byte.
		body := io.MultiReader(
			strings.NewReader(`{"pad":"`),
			&fillerReader{remaining: maxResponseBodyBytes * 2},
			strings.NewReader(`"}`),
		)
		var got struct {
			Pad string `json:"pad"`
		}

		err := parseResponse(newTestResponse(http.StatusOK, body), &got)

		if !errors.Is(err, errResponseBodyTooLarge) {
			t.Fatalf("parseResponse() = %v, want %v", err, errResponseBodyTooLarge)
		}
	})

	t.Run("body exactly at the cap decodes", func(t *testing.T) {
		pad := strings.Repeat("a", int(maxResponseBodyBytes)-len(`{"pad":""}`))
		var got struct {
			Pad string `json:"pad"`
		}

		err := parseResponse(newTestResponse(http.StatusOK, strings.NewReader(`{"pad":"`+pad+`"}`)), &got)

		if err != nil {
			t.Fatalf("parseResponse() = %v, want nil", err)
		}
		if len(got.Pad) != len(pad) {
			t.Errorf("decoded pad = %d bytes, want %d", len(got.Pad), len(pad))
		}
	})
}

// The cap must not disturb the normal success path: a small body still decodes.
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
