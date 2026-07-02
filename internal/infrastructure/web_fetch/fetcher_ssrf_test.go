package web_fetch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchURLContentBlocksRedirectToLoopback(t *testing.T) {
	internalHit := make(chan struct{}, 1)
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		internalHit <- struct{}{}
		fmt.Fprint(w, "<html><body>WEKNORA_INTERNAL_CANARY_178193</body></html>")
	}))
	defer internal.Close()

	if _, err := FetchURLContent(context.Background(), internal.URL); err == nil {
		t.Fatal("direct loopback URL was not rejected")
	}

	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer attacker.Close()

	got, err := FetchURLContent(context.Background(), attacker.URL+"/entry")
	if err == nil {
		if strings.Contains(got, "WEKNORA_INTERNAL_CANARY_178193") {
			t.Fatalf("redirect followed to loopback, got %q", got)
		}
		t.Fatal("expected redirect to loopback to be blocked")
	}
	select {
	case <-internalHit:
		t.Fatal("internal loopback server was reached via redirect")
	default:
	}
}
