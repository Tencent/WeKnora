package cohererank

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/require"
)

// A 200 containing a different JSON document must not become a successful
// empty ranking: callers retain the original candidates only on an error.
func TestRejectsResponsesWithoutAResultsArray(t *testing.T) {
	for name, payload := range map[string]string{
		"null response":  `null`,
		"empty object":   `{}`,
		"error object":   `{"error":{"message":"model is not loaded"}}`,
		"wrong envelope": `{"data":[]}`,
		"null results":   `{"results":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			server, _, _ := serve(t, payload)
			defer server.Close()
			c := newClient(t, server.URL, api.RerankSettings{
				ImageInput: api.ImageInput{ImageField: "image"},
			})

			got, err := c.Rerank(context.Background(), "q", []string{"original candidate"})
			require.ErrorContains(t, err, "results")
			require.Nil(t, got)

			got, err = c.RerankImages(context.Background(), "q", []api.EmbedImage{
				{Data: []byte{1}, MIMEType: "image/png"},
			})
			require.ErrorContains(t, err, "results")
			require.Nil(t, got)
		})
	}
}

func TestAcceptsExplicitlyEmptyResultsArrays(t *testing.T) {
	for _, payload := range []string{`{"results":[]}`, ` [] `} {
		t.Run(payload, func(t *testing.T) {
			server, _, _ := serve(t, payload)
			defer server.Close()
			c := newClient(t, server.URL, api.RerankSettings{})

			got, err := c.Rerank(context.Background(), "q", []string{"d"})
			require.NoError(t, err)
			require.Empty(t, got)
		})
	}
}
