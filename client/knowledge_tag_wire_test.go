package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type tagTransport func(*http.Request) (*http.Response, error)

func (f tagTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestKnowledgeTagWireFormat(t *testing.T) {
	for _, tags := range [][]string{nil, {"red", "blue"}} {
		want := strings.Join(tags, ",")
		if want == "" {
			want = "red"
		}
		transport := tagTransport(func(req *http.Request) (*http.Response, error) {
			body := `{"success":true,"data":[]}`
			if req.Method == http.MethodPost {
				var input struct {
					Tags []string `json:"tag_ids"`
				}
				if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
					t.Fatal(err)
				}
				if strings.Join(input.Tags, ",") != want {
					t.Errorf("URL import tags = %v, want %s", input.Tags, want)
				}
				body = `{"success":true,"data":{"id":"doc"}}`
			} else if got := req.URL.Query().Get("tag_ids"); got != want {
				t.Errorf("list tag_ids = %q, want %q", got, want)
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})
		c := NewClient("https://example.test", WithTransport(transport))
		request := CreateKnowledgeFromURLRequest{
			URL: "https://example.test/document", TagID: "red", TagIDs: tags,
		}
		_, err := c.CreateKnowledgeFromURL(context.Background(), "kb", request)
		if err != nil {
			t.Fatal(err)
		}
		filter := KnowledgeListFilter{TagID: "red", TagIDs: tags}
		_, _, err = c.ListKnowledgeWithFilter(context.Background(), "kb", 1, 10, filter)
		if err != nil {
			t.Fatal(err)
		}
	}
}
