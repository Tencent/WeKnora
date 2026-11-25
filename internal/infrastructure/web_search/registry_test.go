package web_search

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestRegistryClientsReportsFetchReader(t *testing.T) {
	search := func(types.WebSearchProviderParameters) (interfaces.WebSearcher, error) { return nil, nil }
	fetch := func(types.WebSearchProviderParameters) (interfaces.WebFetcher, error) { return nil, nil }
	r := NewRegistry()
	r.Add(Spec{Type: "plain", Search: search})
	r.Add(Spec{Type: "full", Filters: true, Search: search, Fetch: fetch})

	for _, tc := range []struct {
		providerType string
		want         types.WebSearchClients
	}{
		{"plain", types.WebSearchClients{Search: "plain", Fetch: LocalFetchSource}},
		{"full", types.WebSearchClients{Search: "full", Fetch: "full"}},
	} {
		got, err := r.Clients(tc.providerType)
		if err != nil || got != tc.want {
			t.Fatalf("%s: clients=%+v err=%v, want %+v", tc.providerType, got, err, tc.want)
		}
	}
	if _, err := r.Clients("missing"); err == nil {
		t.Fatal("missing type: want error")
	}
}
