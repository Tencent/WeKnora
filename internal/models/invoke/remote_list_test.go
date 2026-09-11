package invoke

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// listProbeAdapter is a minimal ListModelsAdapter fake for entry-level probe
// tests (SSRF gate, result cap); the wire-shape parity lives in the adapters
// package tests. It serves ONLY the listing facet, so the declaration carries
// the Common.ModelListing shard alone (registration lock #2).
type listProbeAdapter struct {
	results []RemoteModel
}

func (a *listProbeAdapter) Provider() string { return "fake" }

func (a *listProbeAdapter) Capabilities() Capabilities {
	return Capabilities{
		Common: CommonCaps{ModelListing: ModelListingCaps{Supported: true}},
	}
}

func (a *listProbeAdapter) BuildListRequest(ep Endpoint) (*Request, error) {
	return &Request{Method: http.MethodGet, URL: ep.BaseURL + "/models"}, nil
}

func (a *listProbeAdapter) ParseListResponse(_ int, _ http.Header, _ []byte) ([]RemoteModel, error) {
	return a.results, nil
}

// TestListEntrySSRF verifies the probe refuses internal targets — the
// endpoint must not become an internal-network scanning channel (v1
// catalog/remote.go gate, now entry-owned).
func TestListEntrySSRF(t *testing.T) {
	for _, target := range []string{
		"http://127.0.0.1:8080",
		"http://169.254.169.254/latest/meta-data",
		"http://localhost:11434",
	} {
		if _, err := List(t.Context(), "fake", &ListOptions{BaseURL: target}); err == nil {
			t.Errorf("List(%q) must fail SSRF validation", target)
		}
	}
}

func TestListEntryRequiresBaseURL(t *testing.T) {
	if _, err := List(t.Context(), "fake", &ListOptions{}); err == nil {
		t.Fatal("List without base URL must fail")
	}
}

// TestListEntryResultCap pins the 500-result cap (design §5.10.2). The stub
// server exists because the entry hands the built request to the REAL
// executor — the probe goes over the wire even against a fake adapter.
func TestListEntryResultCap(t *testing.T) {
	allowLoopbackSSRF(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	results := make([]RemoteModel, maxRemoteModels+50)
	for i := range results {
		results[i] = RemoteModel{ID: fmt.Sprintf("m-%d", i)}
	}
	registerFake(t, &listProbeAdapter{results: results})
	models, err := List(t.Context(), "fake", &ListOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("List = %v", err)
	}
	if len(models) != maxRemoteModels {
		t.Errorf("models = %d, want capped at %d", len(models), maxRemoteModels)
	}
}

func TestProbeTimeoutDefault(t *testing.T) {
	t.Setenv("WEKNORA_REMOTE_CATALOG_TIMEOUT_SECONDS", "")
	if got := probeTimeout(); got != defaultProbeTimeout {
		t.Errorf("probeTimeout default = %v, want %v", got, defaultProbeTimeout)
	}
	t.Setenv("WEKNORA_REMOTE_CATALOG_TIMEOUT_SECONDS", "3")
	if got := probeTimeout(); got != 3*time.Second {
		t.Errorf("probeTimeout(3) = %v, want 3s", got)
	}
	// Invalid values fall back to the default rather than zeroing the probe.
	t.Setenv("WEKNORA_REMOTE_CATALOG_TIMEOUT_SECONDS", "bogus")
	if got := probeTimeout(); got != defaultProbeTimeout {
		t.Errorf("probeTimeout(bogus) = %v, want default", got)
	}
}
