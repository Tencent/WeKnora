package dingtalk

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A successful control-plane reply must be decoded only after its body has been
// bounded. Before the cap, a platform that answered the access-token call with
// a multi-megabyte JSON string was buffered whole and accepted as a token.
func TestGetAccessTokenRejectsOversizedResponse(t *testing.T) {
	useTestHTTPClient(t)

	oversized := strings.Repeat("a", 4<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"accessToken":%q,"expireIn":7200}`, oversized)
	}))
	defer srv.Close()

	orig := apiBaseURL
	apiBaseURL = srv.URL
	defer func() { apiBaseURL = orig }()

	a := &Adapter{clientID: "cid", clientSecret: "sec"}
	token, err := a.getAccessToken(context.Background())
	if err == nil {
		t.Fatalf("getAccessToken accepted an oversized response and returned a %d byte token", len(token))
	}
	if !strings.Contains(err.Error(), "IM response body too large") {
		t.Fatalf("getAccessToken error = %v, want the response cap to fire", err)
	}
}
