package middleware

import (
	"net/http"
	"testing"
)

func TestHealthEndpointsArePublicForGetOnly(t *testing.T) {
	for _, path := range []string{"/health", "/health/live", "/health/ready"} {
		if !isNoAuthAPI(path, http.MethodGet) {
			t.Errorf("GET %s must be public for orchestrator probes", path)
		}
		if isNoAuthAPI(path, http.MethodPost) {
			t.Errorf("POST %s must not be public", path)
		}
	}
}
