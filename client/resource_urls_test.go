package client

import (
	"net/url"
	"testing"
)

func TestApplyResourceURLQuery(t *testing.T) {
	q := url.Values{}
	applyResourceURLQuery(q, nil)
	if len(q) != 0 {
		t.Fatalf("nil opts: got %v, want empty", q)
	}

	q = url.Values{}
	applyResourceURLQuery(q, &ResourceURLOptions{ResourceURLs: ResourceURLModeHandle})
	if len(q) != 0 {
		t.Fatalf("handle mode: got %v, want empty", q)
	}

	q = url.Values{}
	applyResourceURLQuery(q, &ResourceURLOptions{ResourceURLs: ResourceURLModePublic})
	if got := q.Get("resource_urls"); got != "public" {
		t.Fatalf("public mode: got %q, want public", got)
	}
}
