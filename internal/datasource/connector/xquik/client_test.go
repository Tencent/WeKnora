package xquik

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.Handler) *client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &client{baseURL: server.URL, apiKey: "secret", httpClient: server.Client()}
}

func TestClientValidate(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/credits" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.Header.Get("x-api-key") != "secret" {
			t.Errorf("x-api-key = %q", request.Header.Get("x-api-key"))
		}
		if request.Header.Get("User-Agent") != userAgent {
			t.Errorf("User-Agent = %q", request.Header.Get("User-Agent"))
		}
		_, _ = io.WriteString(writer, `{"balance":42}`)
	}))

	if err := client.validate(context.Background()); err != nil {
		t.Fatalf("validate() error = %v", err)
	}
}

func TestClientSearch(t *testing.T) {
	since := time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC)
	until := since.Add(time.Hour)
	client := testClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		want := map[string]string{
			"q":         "rag lang:en",
			"queryType": "Latest",
			"limit":     "25",
			"cursor":    "page-2",
			"sinceTime": since.Format(time.RFC3339Nano),
			"untilTime": until.Format(time.RFC3339Nano),
		}
		for key, value := range want {
			if query.Get(key) != value {
				t.Errorf("%s = %q, want %q", key, query.Get(key), value)
			}
		}
		_, _ = io.WriteString(writer, `{"tweets":[{"id":"1","text":"hello"}],"hasMore":true,"nextCursor":"next"}`)
	}))

	page, err := client.search(context.Background(), searchRequest{
		Query: "rag lang:en", Cursor: "page-2", SinceTime: since, UntilTime: until, Limit: 25,
	})
	if err != nil {
		t.Fatalf("search() error = %v", err)
	}
	if len(page.Tweets) != 1 || page.Tweets[0].ID != "1" || !page.hasMore() || page.nextCursor() != "next" {
		t.Fatalf("page = %#v", page)
	}
}

func TestClientSanitizesAPIError(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(
			writer,
			`{"error":{"code":"invalid_api_key"},"message":"Key secret failed.\nCreate a new key."}`,
		)
	}))

	err := client.validate(context.Background())
	if err == nil {
		t.Fatal("validate() error = nil")
	}
	message := err.Error()
	if !strings.Contains(message, "HTTP 401 (invalid_api_key): Key [redacted] failed. Create a new key.") {
		t.Fatalf("error = %q", message)
	}
	if strings.Contains(message, "secret") {
		t.Fatalf("error exposed credentials: %q", message)
	}
}

func TestClientRejectsOversizedResponse(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(writer, strings.Repeat("x", maxErrorSize+1))
	}))

	err := client.validate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("validate() error = %v", err)
	}
}
