package ollama

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ollama/ollama/api"
)

func TestConcurrentAvailabilityChecksAndPulls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"models":[{"name":"example:latest"}]}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	service := &OllamaService{client: api.NewClient(base, server.Client()), isOptional: true}
	start := make(chan struct{})
	done := make(chan error, 8)
	for actor := 0; actor < 8; actor++ {
		go func(pull bool) {
			<-start
			for range 20 {
				if pull {
					if err := service.PullModel(t.Context(), "example"); err != nil {
						done <- err
						return
					}
				} else {
					available, err := service.IsModelAvailable(t.Context(), "example")
					if err != nil {
						done <- err
						return
					}
					if !available {
						done <- fmt.Errorf("installed model reported unavailable")
						return
					}
				}
			}
			done <- nil
		}(actor%2 == 0)
	}
	close(start)
	for range 8 {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}
