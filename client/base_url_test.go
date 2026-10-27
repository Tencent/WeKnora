package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClientBaseURLTrailingSlash(t *testing.T) {
	for _, suffix := range []string{"", "/", "///", "/weknora", "/weknora/"} {
		t.Run(suffix, func(t *testing.T) {
			wantPath := "/api/v1/knowledge-bases"
			if suffix == "/weknora" || suffix == "/weknora/" {
				wantPath = "/weknora" + wantPath
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != wantPath {
					t.Errorf("request path = %q, want %q", r.URL.Path, wantPath)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()

			c := NewClient(srv.URL + suffix)
			resp, err := c.Raw(context.Background(), http.MethodGet, "/api/v1/knowledge-bases", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := resp.Body.Close(); err != nil {
					t.Errorf("close response body: %v", err)
				}
			}()
			if resp.StatusCode != http.StatusNoContent {
				t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
			}
		})
	}
}
