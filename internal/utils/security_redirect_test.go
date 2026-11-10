package utils

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSSRFSafeClientStripsAuthTokenOnCrossHostRedirect(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,::1,localhost")
	ResetSSRFWhitelistForTest()
	t.Cleanup(ResetSSRFWhitelistForTest)

	var gotToken string
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Auth-Token")
		w.WriteHeader(http.StatusOK)
	}))
	defer attacker.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attacker.URL, http.StatusFound)
	})
	origin := httptest.NewServer(mux)
	defer origin.Close()

	client := NewSSRFSafeHTTPClient(SSRFSafeHTTPClientConfig{
		Timeout:      5 * time.Second,
		MaxRedirects: 5,
	})
	req, err := http.NewRequest(http.MethodGet, origin.URL+"/start", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-Auth-Token", "super-secret-yuque-token")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if gotToken != "" {
		t.Fatalf("X-Auth-Token leaked on cross-host redirect: %q", gotToken)
	}
}
