package nimrerank

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/require"
)

func TestRejectsMalformedRankings(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr string
	}{
		{"null response", `null`, "rankings"},
		{"empty object", `{}`, "rankings"},
		{"error envelope", `{"error":{"message":"model is not loaded"}}`, "rankings"},
		{"wrong envelope", `{"data":[]}`, "rankings"},
		{"null rankings", `{"rankings":null}`, "rankings"},
		{"null row", `{"rankings":[null]}`, "index or logit"},
		{"empty row", `{"rankings":[{}]}`, "index or logit"},
		{"missing index", `{"rankings":[{"logit":0.5}]}`, "index or logit"},
		{"null index", `{"rankings":[{"index":null,"logit":0.5}]}`, "index or logit"},
		{"missing logit", `{"rankings":[{"index":0}]}`, "index or logit"},
		{"null logit", `{"rankings":[{"index":0,"logit":null}]}`, "index or logit"},
		{
			"malformed row after valid row",
			`{"rankings":[{"index":1,"logit":1.2},{"index":0}]}`,
			"index or logit",
		},
		{
			"duplicate index",
			`{"rankings":[{"index":0,"logit":1.2},{"index":0,"logit":0.5}]}`,
			"duplicate index",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SSRF_WHITELIST", "127.0.0.1")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.payload))
			}))
			defer server.Close()
			c := newClient(t, server.URL, api.RerankSettings{})

			got, err := c.Rerank(context.Background(), "q", []string{"d0", "d1"})
			require.ErrorContains(t, err, tt.wantErr)
			require.Nil(t, got)

			got, err = c.RerankImages(context.Background(), "q", []api.EmbedImage{
				{Data: []byte{1}, MIMEType: "image/png"},
				{Data: []byte{2}, MIMEType: "image/png"},
			})
			require.ErrorContains(t, err, tt.wantErr)
			require.Nil(t, got)
		})
	}
}

func TestAcceptsValidRankings(t *testing.T) {
	for name, payload := range map[string]string{
		"empty rankings":           `{"rankings":[]}`,
		"zero and negative logits": `{"rankings":[{"index":0,"logit":0},{"index":1,"logit":-1.5}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SSRF_WHITELIST", "127.0.0.1")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(payload))
			}))
			defer server.Close()
			c := newClient(t, server.URL, api.RerankSettings{})
			want := []api.RerankResult{}
			if name == "zero and negative logits" {
				want = []api.RerankResult{
					{Index: 0, Score: 0, Text: "d0"},
					{Index: 1, Score: -1.5, Text: "d1"},
				}
			}

			got, err := c.Rerank(context.Background(), "q", []string{"d0", "d1"})
			require.NoError(t, err)
			require.Equal(t, want, got)

			for i := range want {
				want[i].Text = ""
			}
			got, err = c.RerankImages(context.Background(), "q", []api.EmbedImage{
				{Data: []byte{1}, MIMEType: "image/png"},
				{Data: []byte{2}, MIMEType: "image/png"},
			})
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}
