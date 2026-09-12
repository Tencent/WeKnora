package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListModelUsageEncodesIntervalAndModels(t *testing.T) {
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models/usage" || r.URL.Query().Get("model_ids") != "a,b" {
			t.Fatalf("request URL = %s", r.URL.String())
		}
		if r.URL.Query().Get("from") != from.Format(time.RFC3339) ||
			r.URL.Query().Get("to") != to.Format(time.RFC3339) {
			t.Fatalf("interval query = %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"from":"2026-08-01T00:00:00Z","to":"2026-09-01T00:00:00Z",
				"items":[{"model_id":"a","call_count":2,"costs":[],
				"latency":{"p50_ms":12,"p95_ms":20,"p99_ms":22,"reported_calls":2},
				"provider_cache":{"hit_rate":null},
				"application_cache":{"lookup_count":3,"bypass_lookup_count":1,"hit_rate":0.5}}]
			}
		}`))
	}))
	defer server.Close()

	report, err := NewClient(server.URL).ListModelUsage(context.Background(), ModelUsageOptions{
		From: &from, To: &to, ModelIDs: []string{"a", "b"},
	})
	if err != nil {
		t.Fatalf("ListModelUsage() error = %v", err)
	}
	if len(report.Items) != 1 || report.Items[0].ApplicationCache.HitRate == nil {
		t.Fatalf("report = %#v", report)
	}
	if report.Items[0].Latency.P95Ms == nil || *report.Items[0].Latency.P95Ms != 20 ||
		report.Items[0].ApplicationCache.BypassLookupCount != 1 {
		t.Fatalf("statistics details = %#v", report.Items[0])
	}
}

func TestPutModelPriceUsesImmutablePriceEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/models/model-1/pricing" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var request PutModelPriceRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Currency != "USD" || request.InputMicrounitsPerMillion != 1_000_000 {
			t.Fatalf("request = %#v", request)
		}
		if request.CachePricing == nil || request.CachePricing.Version != 1 ||
			request.CachePricing.ReadMicrounitsPerMillion == nil ||
			*request.CachePricing.ReadMicrounitsPerMillion != 0 ||
			request.CachePricing.Write5mMicrounitsPerMillion != nil {
			t.Fatalf("cache pricing lost zero/unknown distinction: %#v", request.CachePricing)
		}
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"id":"price-1","model_id":"model-1","currency":"USD",
				"valid_from":"2026-09-01T00:00:00Z","created_at":"2026-09-01T00:00:00Z",
				"input_microunits_per_million":1000000,"output_microunits_per_million":2000000,
				"cache_pricing":{"version":1,"read_microunits_per_million":0,
                "write_5m_microunits_per_million":null,"write_1h_microunits_per_million":null}
			}
		}`))
	}))
	defer server.Close()

	zero := int64(0)
	price, err := NewClient(server.URL).PutModelPrice(context.Background(), "model-1", PutModelPriceRequest{
		CachePricing: &ModelCachePricing{Version: 1, ReadMicrounitsPerMillion: &zero},
		ValidFrom:    time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Currency: "USD",
		InputMicrounitsPerMillion: 1_000_000, OutputMicrounitsPerMillion: 2_000_000,
	})
	if err != nil || price.ID != "price-1" {
		t.Fatalf("price = %#v, err = %v", price, err)
	}
	if price.CachePricing == nil || price.CachePricing.ReadMicrounitsPerMillion == nil ||
		*price.CachePricing.ReadMicrounitsPerMillion != 0 || price.CachePricing.Write1hMicrounitsPerMillion != nil {
		t.Fatalf("decoded cache pricing lost zero/unknown distinction: %#v", price.CachePricing)
	}
}
