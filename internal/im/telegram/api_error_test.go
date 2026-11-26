package telegram

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// replyWith makes the shared Bot API client answer every call with body.
func replyWith(t *testing.T, body string) {
	t.Helper()
	previous := httpClient
	t.Cleanup(func() { httpClient = previous })
	httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
}

func TestCallAPIReportsBotAPIErrorDescription(t *testing.T) {
	// A failed Bot API call carries error_code and description, and no result.
	replyWith(t, `{"ok":false,"error_code":400,"description":"Bad Request: message is too long"}`)

	a := &Adapter{botToken: "token"}
	err := a.callAPI(context.Background(), "sendMessage", map[string]string{"chat_id": "1", "text": "hi"})
	if err == nil {
		t.Fatal("expected an error for ok=false")
	}
	for _, want := range []string{"sendMessage", "400", "Bad Request: message is too long"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestCallAPIWithResultDecodesSuccess(t *testing.T) {
	replyWith(t, `{"ok":true,"result":{"file_path":"photos/a.jpg"}}`)

	var info struct {
		FilePath string `json:"file_path"`
	}
	a := &Adapter{botToken: "token"}
	body := map[string]string{"file_id": "f"}
	if err := a.callAPIWithResult(context.Background(), "getFile", body, &info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.FilePath != "photos/a.jpg" {
		t.Errorf("file_path = %q, want %q", info.FilePath, "photos/a.jpg")
	}
}
