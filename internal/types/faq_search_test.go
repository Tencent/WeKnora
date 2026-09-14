package types

import (
	"encoding/json"
	"testing"
)

func TestFAQSearchRequestAcceptsIncludeDisabled(t *testing.T) {
	var request FAQSearchRequest
	if err := json.Unmarshal([]byte(`{"query_text":"refund","include_disabled":true}`), &request); err != nil {
		t.Fatalf("unmarshal FAQ search request: %v", err)
	}
	if !request.IncludeDisabled {
		t.Fatal("include_disabled was not bound")
	}
}

func TestGenericSearchParamsDoesNotBindIncludeDisabled(t *testing.T) {
	var params SearchParams
	if err := json.Unmarshal([]byte(`{"query_text":"refund","include_disabled":true}`), &params); err != nil {
		t.Fatalf("unmarshal generic search params: %v", err)
	}
	if params.IncludeDisabled {
		t.Fatal("generic search must not expose include_disabled")
	}
}
