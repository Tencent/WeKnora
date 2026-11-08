package web_search

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/customsearch/v1"
	"google.golang.org/api/option"
)

func TestGoogleSearchResultLimit(t *testing.T) {
	for _, tc := range []struct{ requested, want int }{
		{-1, 5}, {0, 5}, {1, 1}, {5, 5}, {10, 10}, {11, 10}, {20, 10}, {100, 10},
	} {
		t.Run(strconv.Itoa(tc.requested), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				num, err := strconv.Atoi(r.URL.Query().Get("num"))
				assert.NoError(t, err)
				assert.Equal(t, tc.want, num)
				assert.Equal(t, "test-engine", r.URL.Query().Get("cx"))
				assert.Equal(t, "test query", r.URL.Query().Get("q"))
				if num < 1 || num > 10 {
					http.Error(w, "num must be between 1 and 10", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"items":[{"title":"Result","link":"https://example.com","snippet":"Summary"}]}`)
			}))
			defer server.Close()
			srv, err := customsearch.NewService(t.Context(), option.WithEndpoint(server.URL+"/"), option.WithHTTPClient(server.Client()), option.WithoutAuthentication())
			require.NoError(t, err)
			provider := &GoogleProvider{srv: srv, engineID: "test-engine"}
			results, err := provider.Search(t.Context(), types.WebSearchRequest{Query: "test query", MaxResults: tc.requested})
			require.NoError(t, err)
			require.Len(t, results, 1)
			require.Equal(t, "google", results[0].Source)
		})
	}
}
