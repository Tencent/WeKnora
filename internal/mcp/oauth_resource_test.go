package mcp

import (
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestOAuthResourceTokenRequests(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"exchange", "grant_type=authorization_code&code=test", "https://resource.example/mcp"},
		{"refresh", "grant_type=refresh_token&refresh_token=test", "https://resource.example/mcp"},
		{"preserve", "grant_type=refresh_token&resource=https%3A%2F%2Fother.example%2Fmcp", "https://other.example/mcp"},
		{"unrelated", "token=test", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var received url.Values
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, r.ParseForm())
				received = r.PostForm
				w.WriteHeader(200)
			}))
			defer s.Close()
			original := s.Client()
			transport := original.Transport
			c := oauthResourceClient(original, "https://resource.example/mcp")
			req, err := http.NewRequest(http.MethodPost, s.URL, strings.NewReader(tc.body))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp, err := c.Do(req)
			require.NoError(t, err)
			resp.Body.Close()
			require.Equal(t, tc.want, received.Get("resource"))
			require.Equal(t, transport, original.Transport)
			b, err := req.GetBody()
			require.NoError(t, err)
			raw, err := io.ReadAll(b)
			b.Close()
			require.NoError(t, err)
			require.Equal(t, tc.body, string(raw))
		})
	}
}
