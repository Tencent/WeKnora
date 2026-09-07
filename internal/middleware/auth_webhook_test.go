package middleware

import (
	"net/http"
	"testing"
)

func TestIsNoAuthAPI_KnowledgeDownloadTicket(t *testing.T) {
	id := "/api/v1/files/knowledge-download/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	if !isNoAuthAPI(id, http.MethodGet) {
		t.Fatal("GET download ticket path must be no-auth")
	}
	if !isNoAuthAPI(id, http.MethodHead) {
		t.Fatal("HEAD download ticket path must be no-auth")
	}
	if isNoAuthAPI(id, http.MethodPost) {
		t.Fatal("POST on the knowledge id itself must still require auth")
	}
	if !isNoAuthAPI(id+"/renew", http.MethodPost) {
		t.Fatal("POST renew must be no-auth")
	}
	if isNoAuthAPI("/api/v1/files/knowledge-download/foo/bar/renew", http.MethodPost) {
		t.Fatal("nested extra path must not match renew")
	}
}
