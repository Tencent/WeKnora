package web_search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/api/customsearch/v1"
	"google.golang.org/api/option"
)

type maxResultsTransport func(*http.Request) (*http.Response, error)

func (f maxResultsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGoogleSearchClampsNumToTheAPILimit(t *testing.T) {
	for _, tc := range []struct{ requested, num int }{{0, 5}, {3, 3}, {10, 10}, {11, 10}, {50, 10}} {
		var got string
		client := &http.Client{Transport: maxResultsTransport(func(r *http.Request) (*http.Response, error) {
			got = r.URL.Query().Get("num")
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"items":[]}`)),
			}, nil
		})}
		srv, err := customsearch.NewService(context.Background(), option.WithAPIKey("k"), option.WithHTTPClient(client))
		require.NoError(t, err)
		p := &GoogleProvider{srv: srv, apiKey: "k", engineID: "cx"}

		_, err = p.Search(context.Background(), "query", tc.requested, false)
		require.NoError(t, err)
		require.Equal(t, strconv.Itoa(tc.num), got, "requested %d", tc.requested)
	}
}

func TestTavilySearchClampsMaxResultsToTheAPILimit(t *testing.T) {
	for _, tc := range []struct{ requested, sent int }{{0, 5}, {3, 3}, {20, 20}, {21, 20}, {50, 20}} {
		var got struct {
			MaxResults int `json:"max_results"`
		}
		client := &http.Client{Transport: maxResultsTransport(func(r *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(r.Body)
			require.NoError(t, json.Unmarshal(body, &got))
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[]}`))}, nil
		})}
		p := &TavilyProvider{apiKey: "k", client: client, baseURL: defaultTavilySearchURL}

		_, err := p.Search(context.Background(), "query", tc.requested, false)
		require.NoError(t, err)
		require.Equal(t, tc.sent, got.MaxResults, "requested %d", tc.requested)
	}
}
