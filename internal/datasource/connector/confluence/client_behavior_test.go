package confluence

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPaginateRejectsLoop(t *testing.T) {
	client := &client{cfg: config{baseURL: "https://confluence.test"}}
	err := client.paginate(context.Background(), "/rest/api/space?limit=100", func(string) (string, error) {
		return "/rest/api/space?limit=100", nil
	})
	if err == nil || !strings.Contains(err.Error(), "looped") {
		t.Fatalf("paginate() error = %v", err)
	}
}

func TestGetIncludesClientErrorBody(t *testing.T) {
	client := &client{
		cfg: config{baseURL: "https://confluence.test", username: "reader", secret: "secret"},
		http: &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBufferString("invalid CQL expression")),
			}, nil
		})},
	}

	err := client.get(context.Background(), "/rest/api/content/search", nil)
	if err == nil || !strings.Contains(err.Error(), "invalid CQL expression") {
		t.Fatalf("get() error = %v", err)
	}
}

func TestPingUsesEditionEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config
		want string
	}{
		{name: "server", cfg: config{baseURL: "https://confluence.test"}, want: "/rest/api/space"},
		{
			name: "cloud",
			cfg:  config{baseURL: "https://team.atlassian.net/wiki", edition: editionCloud},
			want: "/wiki/api/v2/spaces",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var path string
			transport := roundTripper(func(req *http.Request) (*http.Response, error) {
				path = req.URL.Path
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("{}")),
				}, nil
			})
			client := &client{cfg: tc.cfg, http: &http.Client{Transport: transport}}

			if err := client.ping(context.Background()); err != nil {
				t.Fatal(err)
			}
			if path != tc.want {
				t.Fatalf("ping path = %q, want %q", path, tc.want)
			}
		})
	}
}
