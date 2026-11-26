package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFAQExportFormats(t *testing.T) {
	for _, format := range []string{"csv", "json"} {
		t.Run(format, func(t *testing.T) {
			payload := "\xef\xbb\xbfquestion,answer\r\nhello,world\r\n"
			if format == "json" {
				payload = `[{"standard_question":"你好##世界","answers":["first##second","line\nbreak"]}]`
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/knowledge-bases/kb-1/faq/entries/export" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				wantQuery := ""
				if format == "json" {
					wantQuery = "format=json"
				}
				if r.URL.RawQuery != wantQuery {
					t.Errorf("query = %q, want %q", r.URL.RawQuery, wantQuery)
				}
				_, _ = io.WriteString(w, payload)
			}))
			defer srv.Close()
			c := NewClient(srv.URL)
			export := c.ExportFAQEntries
			if format == "json" {
				export = c.ExportFAQEntriesJSON
			}
			data, err := export(context.Background(), "kb-1")
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != payload {
				t.Fatalf("export = %q, want %q", data, payload)
			}
		})
	}
}

func TestFAQExportJSONHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"denied"}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	for _, export := range []func(context.Context, string) ([]byte, error){c.ExportFAQEntries, c.ExportFAQEntriesJSON} {
		data, err := export(context.Background(), "kb-1")
		var apiErr *APIError
		if data != nil || !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
			t.Fatalf("data = %q, error = %v", data, err)
		}
	}
}

type faqExportTransport struct{ body io.ReadCloser }

func (tr faqExportTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: tr.body}, nil
}

type faqExportBrokenBody struct{ closed bool }

func (*faqExportBrokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (b *faqExportBrokenBody) Close() error           { b.closed = true; return nil }

func TestFAQExportJSONReadErrorClosesBody(t *testing.T) {
	body := &faqExportBrokenBody{}
	c := NewClient("http://example.test", WithTransport(faqExportTransport{body: body}))
	_, err := c.ExportFAQEntriesJSON(context.Background(), "kb-1")
	if !errors.Is(err, io.ErrUnexpectedEOF) || !strings.Contains(err.Error(), "failed to read export response") {
		t.Fatalf("error = %v", err)
	}
	if !body.closed {
		t.Fatal("response body was not closed")
	}
}
